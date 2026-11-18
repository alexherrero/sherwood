package rules

import (
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
)

// Set is one strategy's parsed rules. The rule runner and the back-tester
// both decide a strategy's stocks through it, so the two combine rules the
// same way.
type Set struct {
	// All is every rule in the strategy file's order.
	All []Rule
	// Stops are the stop rules, in the file's order.
	Stops []StopRule
	// Sizing is the strategy's sizing rule, or nil if it has none.
	// Validate allows at most one.
	Sizing SizingRule
}

// ParseSet parses a strategy's rules. It does not run Validate's checks
// across rules; call Validate on the whole file first.
func ParseSet(def config.StrategyDef) (Set, error) {
	var s Set
	for _, entry := range def.Rules {
		r, err := Parse(entry)
		if err != nil {
			return Set{}, err
		}
		s.All = append(s.All, r)
		switch x := r.(type) {
		case StopRule:
			s.Stops = append(s.Stops, x)
		case SizingRule:
			s.Sizing = x
		}
	}
	return s, nil
}

// Action is a stock's combined outcome. A lower value wins.
type Action int

// The combined actions, in priority order.
const (
	// ActionStopSell is a stop rule's sell: the holding closed at or below
	// its stop. It is the protective sell.
	ActionStopSell Action = iota
	// ActionSell is any other rule's sell.
	ActionSell
	// ActionTrim is any rule's trim.
	ActionTrim
	// ActionEntry is any rule's buy (not held) or add (held).
	ActionEntry
	// ActionNone is no change.
	ActionNone
	// actionUnset is below every real action, so the first decision of any
	// kind is kept.
	actionUnset
)

// actionOf is the action a non-stop rule's outcome asks for.
func actionOf(o Outcome) Action {
	switch o {
	case Sell:
		return ActionSell
	case Trim:
		return ActionTrim
	case Buy, Add:
		return ActionEntry
	default:
		return ActionNone
	}
}

// Verdict is one stock's combined decision for a trading date.
type Verdict struct {
	Symbol string
	Action Action
	// Outcome is the winning rule's own outcome: buy or add for an entry,
	// sell for either kind of sell, trim, or no change.
	Outcome Outcome
	// Rule is the type of the first rule, in the file's order, that gave the
	// winning action. It is empty only when no rule gave any decision.
	Rule string
	// Reason is that rule's reason as "<rule type>: <reason>", or empty when
	// Rule is.
	Reason string
	// Stop is the highest stop the stop rules give a held stock, or nil.
	Stop *money.Price
}

func (v *Verdict) offer(a Action, ruleType string, d Decision) {
	if a < v.Action {
		v.Action, v.Outcome, v.Rule, v.Reason = a, d.Outcome, ruleType, ruleType+": "+d.Reason
	}
}

// Combine decides each of a strategy's stocks for one trading date. inputs
// holds every stock of the strategy, so a ranking rule ranks them all; a
// stock with no bar on the date should still be there, ending on its last
// bar, so it is ranked as unranked rather than left out. decide picks the
// inputs to give a verdict, by index; nil means all of them. The verdicts come
// back in input order, one per decided input.
//
// Each decided stock gets one action, first match wins: a stop rule's sell,
// any other sell, a trim, a buy or add, no change. Its reason is that of the
// first rule in the file's order giving the winning action. Stop rules run
// only for held stocks, and the highest stop among them is the verdict's
// Stop.
func (s Set) Combine(inputs []Input, decide func(i int) bool) ([]Verdict, error) {
	verdicts := make([]*Verdict, len(inputs))
	for i, in := range inputs {
		if decide == nil || decide(i) {
			verdicts[i] = &Verdict{Symbol: in.Symbol, Action: actionUnset, Outcome: NoChange}
		}
	}
	for _, r := range s.All {
		switch x := r.(type) {
		case StopRule:
			for i, in := range inputs {
				v := verdicts[i]
				if v == nil || in.Holding == nil {
					continue
				}
				d, err := x.Stop(in)
				if err != nil {
					return nil, err
				}
				v.Stop = higher(v.Stop, d.Stop)
				a := ActionNone
				if d.Outcome == Sell {
					a = ActionStopSell
				}
				v.offer(a, x.Type(), d)
			}
		case SignalRule:
			for i, in := range inputs {
				v := verdicts[i]
				if v == nil {
					continue
				}
				d, err := x.Decide(in)
				if err != nil {
					return nil, err
				}
				v.offer(actionOf(d.Outcome), x.Type(), d)
			}
		case RankingRule:
			decs, err := x.Rank(inputs)
			if err != nil {
				return nil, err
			}
			for i, v := range verdicts {
				if v != nil {
					v.offer(actionOf(decs[i].Outcome), x.Type(), decs[i])
				}
			}
		}
	}
	out := make([]Verdict, 0, len(inputs))
	for _, v := range verdicts {
		if v == nil {
			continue
		}
		if v.Action == actionUnset {
			v.Action = ActionNone
		}
		out = append(out, *v)
	}
	return out, nil
}

// InitialStop is the highest stop the stop rules give a new holding bought
// at cost on the last bar's date, or nil if none gives one. size_by_risk
// sizes a buy from it.
func (s Set) InitialStop(in Input, cost money.Price) (*money.Price, error) {
	if len(in.Bars) == 0 {
		return nil, nil
	}
	in.Holding = &Holding{Cost: cost, Opened: in.Bars[len(in.Bars)-1].Date}
	var top *money.Price
	for _, r := range s.Stops {
		d, err := r.Stop(in)
		if err != nil {
			return nil, err
		}
		top = higher(top, d.Stop)
	}
	return top, nil
}

// higher returns a copy of the higher of two optional stops.
func higher(a, b *money.Price) *money.Price {
	if b == nil || (a != nil && b.Cmp(*a) <= 0) {
		return a
	}
	p := *b
	return &p
}
