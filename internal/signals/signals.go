package signals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/rules"
)

// ErrInput is wrapped by Evaluate's errors about its inputs.
var ErrInput = errors.New("signals: bad input")

// Side is an order's side.
type Side string

// The sides.
const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

// Kind is a signal's kind.
type Kind string

// The signal kinds. StopSell is the protective sell, made by a stop rule.
const (
	KindBuy      Kind = "buy"
	KindAdd      Kind = "add"
	KindTrim     Kind = "trim"
	KindSell     Kind = "sell"
	KindStopSell Kind = "stop_sell"
)

// Side is the side an order for this kind of signal takes.
func (k Kind) Side() Side {
	if k == KindBuy || k == KindAdd {
		return SideBuy
	}
	return SideSell
}

// Suggestion is what the rules suggest doing with a holding.
type Suggestion string

// The suggestions.
const (
	Hold        Suggestion = "hold"
	SuggestAdd  Suggestion = "add"
	SuggestTrim Suggestion = "trim"
	SuggestSell Suggestion = "sell"
)

// Signal is one stock's signal from one strategy on one trading date.
type Signal struct {
	// Key is the idempotency key: Key(Strategy, Symbol, Side, Kind, Date).
	Key      string
	Strategy string
	Symbol   string
	// Date is the trading date, midnight Eastern.
	Date time.Time
	Side Side
	Kind Kind
	// Rule is the type of the rule whose reason is kept.
	Rule string
	// Reason is "<rule type>: <reason>".
	Reason string
	// StrategyHash is the hex SHA-256 of the strategy file.
	StrategyHash string
}

// HoldingSuggestion is the rules' suggestion for one holding a strategy
// trades.
type HoldingSuggestion struct {
	Strategy   string
	Symbol     string
	Date       time.Time
	Suggestion Suggestion
	// Reason is "<rule type>: <reason>", or empty for a hold no rule gave a
	// reason for.
	Reason string
	// Stop is the highest stop the strategy's stop rules give, or nil.
	Stop         *money.Price
	StrategyHash string
}

// Key is a signal's idempotency key: the hex SHA-256 of its strategy, stock,
// side, kind and trading date, encoded as a JSON array of strings so no two
// different sets of fields can give the same text.
func Key(strategy, symbol string, side Side, kind Kind, date time.Time) string {
	b, err := json.Marshal([]string{strategy, symbol, string(side), string(kind),
		clock.DateOf(date).Format(clock.DateLayout)})
	if err != nil {
		panic("signals: encoding a key: " + err.Error()) // a slice of strings always encodes
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Inputs is everything one rules run reads.
type Inputs struct {
	// Date is the trading date; only its Eastern calendar date is read.
	Date time.Time
	// Strategy is the loaded strategy file. Evaluate validates its rules.
	Strategy *config.Strategy
	// Day is the date's price check, or nil if the date was never checked.
	Day *prices.Day
	// Bars are each stock's stored bars from the day's primary source,
	// oldest first. Bars after Date are ignored.
	Bars map[string][]prices.Bar
	// Holdings are the holdings now.
	Holdings []holdings.Holding
}

// Plan is what one rules run decided.
type Plan struct {
	// Date is the trading date, midnight Eastern.
	Date time.Time
	// Missing says why the date gave nothing at all, or is empty when its
	// prices were usable.
	Missing string
	// Excluded are the strategies' stocks that got no signal because their
	// prices were not usable on the date, sorted and without repeats.
	Excluded []string
	// Signals are in the strategy file's order of strategies, then by symbol.
	Signals []Signal
	// Suggestions are in the same order.
	Suggestions []HoldingSuggestion
}

var hexHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Evaluate runs every strategy's rules for one trading date. It reads nothing
// but its inputs, so the same inputs always give the same plan.
func Evaluate(in Inputs) (*Plan, error) {
	if in.Strategy == nil {
		return nil, fmt.Errorf("%w: no strategy file", ErrInput)
	}
	if !hexHash.MatchString(in.Strategy.Hash) {
		return nil, fmt.Errorf("%w: the strategy file's hash %q is not a hex SHA-256", ErrInput, in.Strategy.Hash)
	}
	if err := rules.Validate(in.Strategy); err != nil {
		return nil, err
	}
	date := clock.DateOf(in.Date)
	ds := date.Format(clock.DateLayout)
	plan := &Plan{Date: date}
	switch {
	case in.Day == nil:
		plan.Missing = "the prices for " + ds + " were not checked"
		return plan, nil
	case in.Day.DateString() != ds:
		return nil, fmt.Errorf("%w: the price check is for %s, not %s", ErrInput, in.Day.DateString(), ds)
	case !in.Day.Usable:
		plan.Missing = "the prices for " + ds + " are missing: " + in.Day.Detail
		return plan, nil
	}

	held := make(map[string]*rules.Holding, len(in.Holdings))
	for _, h := range in.Holdings {
		cost, err := h.CostPrice()
		if err != nil {
			return nil, fmt.Errorf("%w: holding %s: %w", ErrInput, h.Symbol, err)
		}
		held[h.Symbol] = &rules.Holding{Quantity: h.Quantity, Cost: cost, Opened: h.Opened}
	}

	excluded := map[string]bool{}
	for _, def := range in.Strategy.Strategies {
		set, err := rules.ParseSet(def)
		if err != nil {
			return nil, err
		}
		symbols := slices.Compact(slices.Sorted(slices.Values(def.Stocks)))
		inputs := make([]rules.Input, len(symbols))
		active := make([]bool, len(symbols))
		for i, sym := range symbols {
			active[i] = !in.Day.Excluded(sym)
			if !active[i] {
				excluded[sym] = true
			}
			bars, err := barsFor(in.Bars[sym], sym, ds, active[i])
			if err != nil {
				return nil, err
			}
			inputs[i] = rules.Input{Symbol: sym, Bars: bars, Holding: held[sym]}
		}
		verdicts, err := set.Combine(inputs, func(i int) bool { return active[i] })
		if err != nil {
			return nil, fmt.Errorf("signals: strategy %q: %w", def.Name, err)
		}
		for _, v := range verdicts {
			_, isHeld := held[v.Symbol]
			if kind, ok := kindOf(v.Action, isHeld); ok {
				plan.Signals = append(plan.Signals, Signal{
					Key:      Key(def.Name, v.Symbol, kind.Side(), kind, date),
					Strategy: def.Name, Symbol: v.Symbol, Date: date,
					Side: kind.Side(), Kind: kind, Rule: v.Rule, Reason: v.Reason,
					StrategyHash: in.Strategy.Hash,
				})
			}
			if isHeld {
				plan.Suggestions = append(plan.Suggestions, HoldingSuggestion{
					Strategy: def.Name, Symbol: v.Symbol, Date: date,
					Suggestion: suggestionOf(v.Action), Reason: v.Reason, Stop: v.Stop,
					StrategyHash: in.Strategy.Hash,
				})
			}
		}
	}
	plan.Excluded = slices.Sorted(maps.Keys(excluded))
	return plan, nil
}

// barsFor turns one stock's stored bars into the rules' bars, ending on the
// trading date for a stock whose prices were usable that day and before it
// for one whose were not.
func barsFor(stored []prices.Bar, sym, date string, active bool) ([]rules.Bar, error) {
	out := make([]rules.Bar, 0, len(stored))
	for _, b := range stored {
		d := b.DateString()
		if d > date || (!active && d == date) {
			continue
		}
		if b.Symbol != sym {
			return nil, fmt.Errorf("%w: a bar for %s is filed under %s", ErrInput, b.Symbol, sym)
		}
		out = append(out, rules.Bar{Date: b.Date, High: b.High, Low: b.Low, Close: b.Close})
	}
	if active && (len(out) == 0 || out[len(out)-1].Date.In(clock.Eastern).Format(clock.DateLayout) != date) {
		return nil, fmt.Errorf("%w: %s's prices agreed on %s, but its primary bar for that date is not stored", ErrInput, sym, date)
	}
	return out, nil
}

// kindOf is the signal a verdict makes, if any.
func kindOf(a rules.Action, held bool) (Kind, bool) {
	switch a {
	case rules.ActionStopSell:
		return KindStopSell, true
	case rules.ActionSell:
		return KindSell, true
	case rules.ActionTrim:
		return KindTrim, true
	case rules.ActionEntry:
		if held {
			return KindAdd, true
		}
		return KindBuy, true
	default:
		return "", false
	}
}

func suggestionOf(a rules.Action) Suggestion {
	switch a {
	case rules.ActionStopSell, rules.ActionSell:
		return SuggestSell
	case rules.ActionTrim:
		return SuggestTrim
	case rules.ActionEntry:
		return SuggestAdd
	default:
		return Hold
	}
}
