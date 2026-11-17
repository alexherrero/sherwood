package rules

import (
	"strings"
	"testing"

	"github.com/alexherrero/sherwood/internal/config"
)

// The three test stocks over four days, ranked by their 2-day change:
//
//	AAA: 10, 10, 10, 13  yesterday 1.0 (rank 3), today 1.3 (rank 1)
//	BBB: 10, 12, 12, 12  yesterday 1.2 (rank 1), today 1.0 (rank 2, ties CCC on symbol)
//	CCC: 10, 11, 11, 11  yesterday 1.1 (rank 2), today 1.0 (rank 3)
func momentumInputs(holding map[string]*Holding) []Input {
	return []Input{
		{Symbol: "AAA", Bars: closes("10.00", "10.00", "10.00", "13.00"), Holding: holding["AAA"]},
		{Symbol: "BBB", Bars: closes("10.00", "12.00", "12.00", "12.00"), Holding: holding["BBB"]},
		{Symbol: "CCC", Bars: closes("10.00", "11.00", "11.00", "11.00"), Holding: holding["CCC"]},
	}
}

func rankAll(t *testing.T, r Rule, ins []Input) map[string]Decision {
	t.Helper()
	ds, err := r.(RankingRule).Rank(ins)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != len(ins) {
		t.Fatalf("got %d decisions for %d inputs", len(ds), len(ins))
	}
	out := map[string]Decision{}
	for i, d := range ds {
		if d.Symbol != ins[i].Symbol {
			t.Fatalf("decision %d is for %s, input is %s", i, d.Symbol, ins[i].Symbol)
		}
		out[d.Symbol] = d
	}
	return out
}

func TestMomentumRanking(t *testing.T) {
	r := mustParse(t, config.RuleMomentumRanking, "days", "2", "top", "1", "exit_rank", "2")
	cases := []struct {
		name    string
		holding map[string]*Holding
		want    map[string]want
	}{
		{"nothing held", nil, map[string]want{
			"AAA": {Buy, "rose to rank 1 of 3 by its 2-day change, into the top 1", "none"},
			"BBB": {NoChange, "is at rank 2 of 3 by its 2-day change", "none"},
			"CCC": {NoChange, "is at rank 3 of 3 by its 2-day change", "none"},
		}},
		{"all held", map[string]*Holding{"AAA": held("10.00"), "BBB": held("10.00"), "CCC": held("10.00")}, map[string]want{
			"AAA": {Add, "rose to rank 1 of 3 by its 2-day change, into the top 1", "none"},
			"BBB": {Trim, "fell to rank 2 of 3 by its 2-day change, out of the top 1", "none"},
			"CCC": {Sell, "fell to rank 3 of 3 by its 2-day change, below rank 2", "none"},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rankAll(t, r, momentumInputs(c.holding))
			for sym, w := range c.want {
				check(t, got[sym], sym, w)
			}
		})
	}
}

func TestMomentumRankingIgnoresInputOrder(t *testing.T) {
	r := mustParse(t, config.RuleMomentumRanking, "days", "2", "top", "1", "exit_rank", "2")
	h := map[string]*Holding{"BBB": held("10.00"), "CCC": held("10.00")}
	first := rankAll(t, r, momentumInputs(h))
	ins := momentumInputs(h)
	reversed := []Input{ins[2], ins[0], ins[1]}
	second := rankAll(t, r, reversed)
	for sym, d := range first {
		if second[sym].Outcome != d.Outcome || second[sym].Reason != d.Reason {
			t.Errorf("%s: %v / %q in one order, %v / %q in another", sym, d.Outcome, d.Reason, second[sym].Outcome, second[sym].Reason)
		}
	}
}

func TestMomentumRankingUnrankable(t *testing.T) {
	r := mustParse(t, config.RuleMomentumRanking, "days", "2", "top", "1", "exit_rank", "2")
	ins := momentumInputs(nil)
	short := Input{Symbol: "DDD", Bars: closes("10.00", "10.00", "10.00")}
	// EEE's prices stop a day early.
	late := Input{Symbol: "EEE", Bars: closes("9.00", "10.00", "10.00", "10.00")}
	for i := range late.Bars {
		late.Bars[i].Date = day(i - 1)
	}
	zero := Input{Symbol: "FFF", Bars: closes("0.00", "10.00", "10.00", "10.00")}
	got := rankAll(t, r, append(ins, short, late, zero))
	check(t, got["DDD"], "DDD", want{NoChange, "needs 4 days of prices and has 3", "none"})
	check(t, got["EEE"], "EEE", want{NoChange, "has no price for the latest trading date; its prices end on 2026-01-07", "none"})
	check(t, got["FFF"], "FFF", want{NoChange, "has a zero close 2 days back, so its change cannot be ranked", "none"})
	// The three rankable stocks still rank among themselves only.
	check(t, got["AAA"], "AAA", want{Buy, "rose to rank 1 of 3 by its 2-day change, into the top 1", "none"})
}

func TestMomentumRankingRefusesRepeatedSymbol(t *testing.T) {
	r := mustParse(t, config.RuleMomentumRanking, "days", "2", "top", "1", "exit_rank", "2").(RankingRule)
	ins := momentumInputs(nil)
	_, err := r.Rank(append(ins, ins[0]))
	if err == nil || !strings.Contains(err.Error(), "AAA appears twice") {
		t.Fatalf("err = %v, want a repeated-symbol error", err)
	}
}

func TestMomentumRankingEmpty(t *testing.T) {
	r := mustParse(t, config.RuleMomentumRanking, "days", "2", "top", "1", "exit_rank", "2").(RankingRule)
	ds, err := r.Rank(nil)
	if err != nil || len(ds) != 0 {
		t.Fatalf("Rank(nil) = %v, %v", ds, err)
	}
}
