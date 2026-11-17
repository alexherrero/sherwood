package rules

import (
	"fmt"
	"sort"
	"time"

	"github.com/alexherrero/sherwood/internal/config"
)

// MomentumRanking ranks a strategy's stocks by their change in close over
// the last Days days, best first, ties broken by symbol. A stock that moves
// into the top Top ranks is a buy (or an add, if held). A holding that falls
// out of the top Top is a trim, and one that falls below rank ExitRank is a
// sell. Each is given on the day the move happens.
type MomentumRanking struct {
	Days     int
	Top      int
	ExitRank int
}

var momentumRankingSettings = []Setting{
	{Name: "days", Type: WholeNumber, Min: 2, Max: maxDays, Doc: "days the change in close is measured over"},
	{Name: "top", Type: WholeNumber, Min: 1, Max: config.MaxHoldings, Doc: "ranks that buy; fewer than the strategy's stocks"},
	{Name: "exit_rank", Type: WholeNumber, Min: 1, Max: 100, Doc: "the lowest rank a holding may fall to before it is sold; at least top"},
}

func newMomentumRanking(v *values) Rule {
	r := MomentumRanking{Days: v.whole["days"], Top: v.whole["top"], ExitRank: v.whole["exit_rank"]}
	if r.Top != 0 && r.ExitRank != 0 && r.ExitRank < r.Top {
		v.addf("exit_rank is %d, but it must be at least top (%d)", r.ExitRank, r.Top)
	}
	return r
}

// Type returns "momentum_ranking".
func (MomentumRanking) Type() string { return config.RuleMomentumRanking }

// Kind returns KindRanking.
func (MomentumRanking) Kind() Kind { return KindRanking }

// ranks ranks the inputs at idx by their change over r.Days days to the bar
// back bars from their last, returning each index's rank from 1.
func (r MomentumRanking) ranks(ins []Input, idx []int, back int) map[int]int {
	order := append([]int(nil), idx...)
	end := func(i int) int { return len(ins[i].Bars) - 1 - back }
	// a beats b when close_a/base_a > close_b/base_b, worked out as
	// close_a*base_b > close_b*base_a (every base is above zero).
	sort.SliceStable(order, func(x, y int) bool {
		a, b := ins[order[x]], ins[order[y]]
		ea, eb := end(order[x]), end(order[y])
		left := a.Bars[ea].Close.Decimal().Mul(b.Bars[eb-r.Days].Close.Decimal())
		right := b.Bars[eb].Close.Decimal().Mul(a.Bars[ea-r.Days].Close.Decimal())
		if c := left.Cmp(right); c != 0 {
			return c > 0
		}
		return a.Symbol < b.Symbol
	})
	out := make(map[int]int, len(order))
	for rank, i := range order {
		out[i] = rank + 1
	}
	return out
}

// Rank gives one decision per input, in the order given. Every input is
// ranked against the others that have enough prices and a bar for the latest
// trading date among them; the rest get no change, with the reason. The order
// of the inputs does not change any outcome.
func (r MomentumRanking) Rank(ins []Input) ([]Decision, error) {
	seen := map[string]bool{}
	latest := 0
	for _, in := range ins {
		if seen[in.Symbol] {
			return nil, fmt.Errorf("rules: %s appears twice in one ranking", in.Symbol)
		}
		seen[in.Symbol] = true
		if err := checkBars(in); err != nil {
			return nil, err
		}
		if n := len(in.Bars); n > 0 {
			latest = max(latest, dayKey(in.Bars[n-1].Date))
		}
	}
	need := r.Days + 2
	out := make([]Decision, len(ins))
	var ranked []int
	for i, in := range ins {
		n := len(in.Bars)
		switch {
		case n < need:
			out[i] = tooFew(in, need)
		case dayKey(in.Bars[n-1].Date) != latest:
			out[i] = noChange(in, "has no price for the latest trading date; its prices end on "+
				in.Bars[n-1].Date.Format(time.DateOnly))
		case in.Bars[n-1-r.Days].Close.IsZero() || in.Bars[n-2-r.Days].Close.IsZero():
			out[i] = noChange(in, fmt.Sprintf("has a zero close %d days back, so its change cannot be ranked", r.Days))
		default:
			ranked = append(ranked, i)
		}
	}
	today, before := r.ranks(ins, ranked, 0), r.ranks(ins, ranked, 1)
	of := len(ranked)
	for _, i := range ranked {
		in := ins[i]
		now, was := today[i], before[i]
		at := func(verb string) string {
			return fmt.Sprintf("%s rank %d of %d by its %d-day change", verb, now, of, r.Days)
		}
		held := in.Holding != nil
		switch {
		case held && now > r.ExitRank && was <= r.ExitRank:
			out[i] = Decision{Symbol: in.Symbol, Outcome: Sell, Reason: at("fell to") + fmt.Sprintf(", below rank %d", r.ExitRank)}
		case held && now > r.Top && was <= r.Top:
			out[i] = Decision{Symbol: in.Symbol, Outcome: Trim, Reason: at("fell to") + fmt.Sprintf(", out of the top %d", r.Top)}
		case now <= r.Top && was > r.Top:
			out[i] = Decision{Symbol: in.Symbol, Outcome: entry(in), Reason: at("rose to") + fmt.Sprintf(", into the top %d", r.Top)}
		default:
			out[i] = noChange(in, at("is at"))
		}
	}
	return out, nil
}
