package rules

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
)

// maxDays is the longest look-back any rule setting may ask for: a little
// more than a year of trading days.
const maxDays = 300

// sumCloses is the exact sum of the n closes ending at bar end.
func sumCloses(bars []Bar, end, n int) decimal.Decimal {
	sum := decimal.Zero
	for i := end - n + 1; i <= end; i++ {
		sum = sum.Add(bars[i].Close.Decimal())
	}
	return sum
}

// vsAverage compares bar end's close with the average of the n closes ending
// there: -1 below, 0 level, +1 above. It compares close*n with the sum, so it
// is exact and divides by nothing.
func vsAverage(bars []Bar, end, n int) int {
	return bars[end].Close.Decimal().Mul(decimal.NewFromInt(int64(n))).Cmp(sumCloses(bars, end, n))
}

// highestHigh is the highest high of bars[from:to].
func highestHigh(bars []Bar, from, to int) money.Price {
	best := bars[from].High
	for _, b := range bars[from+1 : to] {
		if b.High.Cmp(best) > 0 {
			best = b.High
		}
	}
	return best
}

// lowestLow is the lowest low of bars[from:to].
func lowestLow(bars []Bar, from, to int) money.Price {
	best := bars[from].Low
	for _, b := range bars[from+1 : to] {
		if b.Low.Cmp(best) < 0 {
			best = b.Low
		}
	}
	return best
}

// highestClose is the highest close of bars[from:to].
func highestClose(bars []Bar, from, to int) money.Price {
	best := bars[from].Close
	for _, b := range bars[from+1 : to] {
		if b.Close.Cmp(best) > 0 {
			best = b.Close
		}
	}
	return best
}

// side names a comparison's result as a word.
func side(c int) string {
	switch {
	case c > 0:
		return "above"
	case c < 0:
		return "below"
	}
	return "level with"
}

// MovingAverageCross signals when a stock's fast moving average of closes
// crosses its slow one: a buy (or an add) on the day the fast average moves
// above the slow, and a sell of a holding on the day it moves below.
type MovingAverageCross struct {
	FastDays int
	SlowDays int
}

var movingAverageCrossSettings = []Setting{
	{Name: "fast_days", Type: WholeNumber, Min: 2, Max: maxDays - 1, Doc: "days in the fast average"},
	{Name: "slow_days", Type: WholeNumber, Min: 3, Max: maxDays, Doc: "days in the slow average; more than fast_days"},
}

func newMovingAverageCross(v *values) Rule {
	r := MovingAverageCross{FastDays: v.whole["fast_days"], SlowDays: v.whole["slow_days"]}
	if r.FastDays != 0 && r.SlowDays != 0 && r.SlowDays <= r.FastDays {
		v.addf("slow_days is %d, but it must be more than fast_days (%d)", r.SlowDays, r.FastDays)
	}
	return r
}

// Type returns "moving_average_cross".
func (MovingAverageCross) Type() string { return config.RuleMovingAverageCross }

// Kind returns KindSignal.
func (MovingAverageCross) Kind() Kind { return KindSignal }

// state compares the fast average with the slow at bar end: fast/f against
// slow/s, worked out as fast*s against slow*f.
func (r MovingAverageCross) state(bars []Bar, end int) int {
	fast := sumCloses(bars, end, r.FastDays).Mul(decimal.NewFromInt(int64(r.SlowDays)))
	slow := sumCloses(bars, end, r.SlowDays).Mul(decimal.NewFromInt(int64(r.FastDays)))
	return fast.Cmp(slow)
}

// Decide gives the stock's outcome on its last bar.
func (r MovingAverageCross) Decide(in Input) (Decision, error) {
	if err := checkBars(in); err != nil {
		return Decision{}, err
	}
	need := r.SlowDays + 1
	if len(in.Bars) < need {
		return tooFew(in, need), nil
	}
	t := len(in.Bars) - 1
	now, before := r.state(in.Bars, t), r.state(in.Bars, t-1)
	averages := func(verb string) string {
		return fmt.Sprintf("its %d-day average %s its %d-day average", r.FastDays, verb, r.SlowDays)
	}
	switch {
	case now > 0 && before <= 0:
		return Decision{Symbol: in.Symbol, Outcome: entry(in), Reason: averages("crossed above")}, nil
	case now < 0 && before >= 0:
		if in.Holding != nil {
			return Decision{Symbol: in.Symbol, Outcome: Sell, Reason: averages("crossed below")}, nil
		}
		return noChange(in, averages("crossed below")+"; not held"), nil
	}
	return noChange(in, averages("is "+side(now))), nil
}

// Breakout signals a buy (or an add) on the day a stock first closes above
// the highest high of the previous Days days, and a sell of a holding on the
// day it first closes below the lowest low of the previous ExitDays days.
type Breakout struct {
	Days     int
	ExitDays int
}

var breakoutSettings = []Setting{
	{Name: "days", Type: WholeNumber, Min: 2, Max: maxDays, Doc: "days of highs a close must clear to buy"},
	{Name: "exit_days", Type: WholeNumber, Min: 2, Max: maxDays, Doc: "days of lows a close must fall through to sell"},
}

func newBreakout(v *values) Rule {
	return Breakout{Days: v.whole["days"], ExitDays: v.whole["exit_days"]}
}

// Type returns "breakout".
func (Breakout) Type() string { return config.RuleBreakout }

// Kind returns KindSignal.
func (Breakout) Kind() Kind { return KindSignal }

// Decide gives the stock's outcome on its last bar.
func (r Breakout) Decide(in Input) (Decision, error) {
	if err := checkBars(in); err != nil {
		return Decision{}, err
	}
	need := max(r.Days, r.ExitDays) + 2
	if len(in.Bars) < need {
		return tooFew(in, need), nil
	}
	bars := in.Bars
	t := len(bars) - 1
	high := func(i int) money.Price { return highestHigh(bars, i-r.Days, i) }
	low := func(i int) money.Price { return lowestLow(bars, i-r.ExitDays, i) }
	above := func(i int) bool { return bars[i].Close.Cmp(high(i)) > 0 }
	below := func(i int) bool { return bars[i].Close.Cmp(low(i)) < 0 }
	closed := bars[t].Close
	switch {
	case above(t) && !above(t-1):
		reason := fmt.Sprintf("closed at %s, above its %d-day high of %s", closed, r.Days, high(t))
		return Decision{Symbol: in.Symbol, Outcome: entry(in), Reason: reason}, nil
	case below(t) && !below(t-1):
		reason := fmt.Sprintf("closed at %s, below its %d-day low of %s", closed, r.ExitDays, low(t))
		if in.Holding != nil {
			return Decision{Symbol: in.Symbol, Outcome: Sell, Reason: reason}, nil
		}
		return noChange(in, reason+"; not held"), nil
	}
	return noChange(in, fmt.Sprintf("closed at %s; its %d-day high is %s and its %d-day low is %s",
		closed, r.Days, high(t), r.ExitDays, low(t))), nil
}

// Pullback buys a dip within an uptrend: a buy (or an add) on the day a stock
// first closes at least Pullback below its highest close of the last HighDays
// days while still closing above its TrendDays-day average, and a sell of a
// holding on the day it first closes below that average.
type Pullback struct {
	TrendDays int
	HighDays  int
	Pullback  decimal.Decimal
}

var pullbackSettings = []Setting{
	{Name: "trend_days", Type: WholeNumber, Min: 2, Max: maxDays, Doc: "days in the average a close must stay above"},
	{Name: "high_days", Type: WholeNumber, Min: 2, Max: maxDays, Doc: "days of closes the pullback is measured from"},
	{Name: "pullback", Type: Fraction, Doc: "how far below that high close a close must be to buy"},
}

func newPullback(v *values) Rule {
	return Pullback{TrendDays: v.whole["trend_days"], HighDays: v.whole["high_days"], Pullback: v.fraction["pullback"]}
}

// Type returns "pullback".
func (Pullback) Type() string { return config.RulePullback }

// Kind returns KindSignal.
func (Pullback) Kind() Kind { return KindSignal }

// Decide gives the stock's outcome on its last bar.
func (r Pullback) Decide(in Input) (Decision, error) {
	if err := checkBars(in); err != nil {
		return Decision{}, err
	}
	need := max(r.TrendDays, r.HighDays) + 1
	if len(in.Bars) < need {
		return tooFew(in, need), nil
	}
	bars := in.Bars
	t := len(bars) - 1
	keep := decimal.NewFromInt(1).Sub(r.Pullback)
	high := func(i int) money.Price { return highestClose(bars, i-r.HighDays+1, i+1) }
	dipped := func(i int) bool {
		return bars[i].Close.Decimal().Cmp(high(i).Decimal().Mul(keep)) <= 0
	}
	buy := func(i int) bool { return vsAverage(bars, i, r.TrendDays) > 0 && dipped(i) }
	below := func(i int) bool { return vsAverage(bars, i, r.TrendDays) < 0 }
	closed := bars[t].Close
	switch {
	case buy(t) && !buy(t-1):
		reason := fmt.Sprintf("closed at %s, %s or more below its %d-day high close of %s, while above its %d-day average",
			closed, percent(r.Pullback), r.HighDays, high(t), r.TrendDays)
		return Decision{Symbol: in.Symbol, Outcome: entry(in), Reason: reason}, nil
	case below(t) && !below(t-1):
		reason := fmt.Sprintf("closed at %s, below its %d-day average", closed, r.TrendDays)
		if in.Holding != nil {
			return Decision{Symbol: in.Symbol, Outcome: Sell, Reason: reason}, nil
		}
		return noChange(in, reason+"; not held"), nil
	}
	return noChange(in, fmt.Sprintf("closed at %s, %s its %d-day average; its %d-day high close is %s",
		closed, side(vsAverage(bars, t, r.TrendDays)), r.TrendDays, r.HighDays, high(t))), nil
}
