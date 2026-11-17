package rules

import (
	"errors"
	"fmt"
	"time"

	"github.com/alexherrero/sherwood/internal/money"
)

// Bar is one day of a stock's split-adjusted prices. The rule library defines
// its own small bar so it does not depend on where prices come from; the rule
// runner fills it from the price store.
type Bar struct {
	// Date is the trading date. Only its year, month and day are read.
	Date  time.Time
	High  money.Price
	Low   money.Price
	Close money.Price
}

// Holding is the part of a holding the rules read.
type Holding struct {
	// Quantity is the number of shares held.
	Quantity money.Quantity
	// Cost is the average cost per share.
	Cost money.Price
	// Opened is the trading date the holding was first bought. Only its
	// year, month and day are read.
	Opened time.Time
	// Stop is the stop in force now, if any. A trailing stop is never set
	// below it.
	Stop *money.Price
}

// Input is everything a rule may read about one stock on one trading date.
type Input struct {
	Symbol string
	// Bars are the stock's daily bars, oldest first, one per trading date.
	// The last bar is the trading date being decided.
	Bars []Bar
	// Holding is nil when the stock is not held.
	Holding *Holding
}

// Outcome is what a rule says to do with a stock today.
type Outcome string

// The outcomes. Buy is for a stock not held and Add for one that is; trim
// and sell are only ever given for a held stock.
const (
	NoChange Outcome = "no_change"
	Buy      Outcome = "buy"
	Add      Outcome = "add"
	Trim     Outcome = "trim"
	Sell     Outcome = "sell"
)

// Decision is a rule's outcome for one stock, with the rule's own reason.
type Decision struct {
	Symbol  string
	Outcome Outcome
	// Reason is a short mechanical sentence fragment written by the rule,
	// such as "closed at 105.00, above its 50-day high of 104.00".
	Reason string
	// Stop is the stop price for the holding. Only stop rules set it, and
	// only for a stock that is held.
	Stop *money.Price
}

// SizeInput is what a sizing rule reads for one order.
type SizeInput struct {
	Symbol string
	// Price is the price the order is worked out at, normally its limit.
	Price money.Price
	// Stop is the stop the strategy's stop rule sets for the new shares.
	// size_by_risk needs it; size_fixed_amount ignores it.
	Stop *money.Price
	// Capital is the strategy's capital: its portfolio_share of the
	// portfolio's value. size_by_risk risks a share of it; size_fixed_amount
	// ignores it.
	Capital money.Money
	// Policy is the strategy file's share policy. Counts are rounded down.
	Policy money.QuantityPolicy
}

// Size is a sizing rule's share count, with its reason.
type Size struct {
	Quantity money.Quantity
	Reason   string
}

// Kind says which of the four shapes a rule has.
type Kind string

// The rule kinds.
const (
	KindSignal  Kind = "signal"
	KindRanking Kind = "ranking"
	KindStop    Kind = "stop"
	KindSizing  Kind = "sizing"
)

// Rule is a parsed rule with its settings. Every Rule is also exactly one of
// SignalRule, RankingRule, StopRule or SizingRule, as its Kind says.
type Rule interface {
	// Type is the rule type's name in the strategy file.
	Type() string
	Kind() Kind
}

// SignalRule decides one stock on its own.
type SignalRule interface {
	Rule
	Decide(in Input) (Decision, error)
}

// RankingRule decides all of a strategy's stocks together. It returns one
// decision per input, in the order given.
type RankingRule interface {
	Rule
	Rank(in []Input) ([]Decision, error)
}

// StopRule gives a holding's stop price.
type StopRule interface {
	Rule
	Stop(in Input) (Decision, error)
}

// SizingRule gives an order's share count.
type SizingRule interface {
	Rule
	Size(in SizeInput) (Size, error)
}

// ErrBars is wrapped by the error a rule returns when its bars are out of
// order or repeat a date. Too few bars is not an error: the rule gives no
// change and says how many it needs.
var ErrBars = errors.New("rules: bars out of order")

// dayKey turns a date into a number that orders like the date, reading only
// its year, month and day as written.
func dayKey(t time.Time) int {
	y, m, d := t.Date()
	return y*10000 + int(m)*100 + d
}

// checkBars refuses bars that are not strictly in date order.
func checkBars(in Input) error {
	for i := 1; i < len(in.Bars); i++ {
		if dayKey(in.Bars[i].Date) <= dayKey(in.Bars[i-1].Date) {
			return fmt.Errorf("%w: %s bar %d (%s) is not after bar %d (%s)", ErrBars,
				in.Symbol, i, in.Bars[i].Date.Format(time.DateOnly), i-1, in.Bars[i-1].Date.Format(time.DateOnly))
		}
	}
	return nil
}

// noChange is a decision with no change and the given reason.
func noChange(in Input, reason string) Decision {
	return Decision{Symbol: in.Symbol, Outcome: NoChange, Reason: reason}
}

// tooFew is the no-change decision for a stock without enough history.
func tooFew(in Input, need int) Decision {
	days := "days"
	if need == 1 {
		days = "day"
	}
	return noChange(in, fmt.Sprintf("needs %d %s of prices and has %d", need, days, len(in.Bars)))
}

// entry is Buy for a stock not held and Add for one that is.
func entry(in Input) Outcome {
	if in.Holding != nil {
		return Add
	}
	return Buy
}
