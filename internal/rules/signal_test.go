package rules

import (
	"errors"
	"testing"

	"github.com/alexherrero/sherwood/internal/config"
)

type signalCase struct {
	name    string
	bars    []Bar
	holding *Holding
	want    want
}

func runSignal(t *testing.T, r Rule, cases []signalCase) {
	t.Helper()
	sr, ok := r.(SignalRule)
	if !ok {
		t.Fatalf("%s is not a SignalRule", r.Type())
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := Input{Symbol: "TEST", Bars: c.bars, Holding: c.holding}
			got, err := sr.Decide(in)
			if err != nil {
				t.Fatal(err)
			}
			check(t, got, "TEST", c.want)
			again, err := sr.Decide(in)
			if err != nil || again.Outcome != got.Outcome || again.Reason != got.Reason {
				t.Errorf("second run differs: %+v, %v", again, err)
			}
		})
	}
}

func TestMovingAverageCross(t *testing.T) {
	r := mustParse(t, config.RuleMovingAverageCross, "fast_days", "2", "slow_days", "3")
	const up = "its 2-day average crossed above its 3-day average"
	const down = "its 2-day average crossed below its 3-day average"
	runSignal(t, r, []signalCase{
		{"cross above, not held", closes("10.00", "10.00", "10.00", "12.00"), nil, want{Buy, up, "none"}},
		{"cross above, held", closes("10.00", "10.00", "10.00", "12.00"), held("10.00"), want{Add, up, "none"}},
		{"cross below, held", closes("10.00", "10.00", "10.00", "8.00"), held("10.00"), want{Sell, down, ""}},
		{"cross below, not held", closes("10.00", "10.00", "10.00", "8.00"), nil, want{NoChange, down + "; not held", ""}},
		{"stays above", closes("10.00", "10.00", "12.00", "13.00"), nil,
			want{NoChange, "its 2-day average is above its 3-day average", ""}},
		{"stays below", closes("13.00", "13.00", "12.00", "11.00"), held("10.00"),
			want{NoChange, "its 2-day average is below its 3-day average", ""}},
		{"flat", closes("10.00", "10.00", "10.00", "10.00"), nil,
			want{NoChange, "its 2-day average is level with its 3-day average", ""}},
		// fast 10.005 vs slow 10.00333 the day before, fast 10.00 vs slow
		// 10.00333 today: a cross below decided by a third of a cent.
		{"cross decided by a fraction of a cent", closes("10.00", "10.01", "10.00", "10.00"), held("10.00"),
			want{Sell, down, ""}},
		{"too few bars", closes("10.00", "10.00", "12.00"), nil, want{NoChange, "needs 4 days of prices and has 3", ""}},
	})
}

func TestBreakout(t *testing.T) {
	r := mustParse(t, config.RuleBreakout, "days", "3", "exit_days", "2")
	flat := [3]string{"10.00", "9.00", "9.50"}
	runSignal(t, r, []signalCase{
		{"breaks out", hlc(flat, flat, flat, flat, [3]string{"11.00", "10.00", "10.50"}), nil,
			want{Buy, "closed at 10.50, above its 3-day high of 10.00", ""}},
		{"breaks out, held", hlc(flat, flat, flat, flat, [3]string{"11.00", "10.00", "10.50"}), held("9.50"),
			want{Add, "closed at 10.50, above its 3-day high of 10.00", ""}},
		{"second day above is not a new signal",
			hlc(flat, flat, flat, flat, [3]string{"11.00", "10.00", "10.50"}, [3]string{"12.00", "11.00", "11.50"}), nil,
			want{NoChange, "closed at 11.50; its 3-day high is 11.00 and its 2-day low is 9.00", ""}},
		{"closes at the high, not above", hlc(flat, flat, flat, flat, [3]string{"10.50", "9.50", "10.00"}), nil,
			want{NoChange, "closed at 10.00; its 3-day high is 10.00 and its 2-day low is 9.00", ""}},
		{"breaks down, held", hlc(flat, flat, flat, flat, [3]string{"9.00", "8.00", "8.50"}), held("9.50"),
			want{Sell, "closed at 8.50, below its 2-day low of 9.00", ""}},
		{"breaks down, not held", hlc(flat, flat, flat, flat, [3]string{"9.00", "8.00", "8.50"}), nil,
			want{NoChange, "closed at 8.50, below its 2-day low of 9.00; not held", ""}},
		{"too few bars", hlc(flat, flat, flat, flat), nil, want{NoChange, "needs 5 days of prices and has 4", ""}},
	})
}

func TestPullback(t *testing.T) {
	r := mustParse(t, config.RulePullback, "trend_days", "3", "high_days", "3", "pullback", "0.05")
	const entryAt95 = "closed at 95.00, 5% or more below its 3-day high close of 100.00, while above its 3-day average"
	runSignal(t, r, []signalCase{
		{"pulls back in an uptrend", closes("50.00", "120.00", "60.00", "100.00"), nil,
			want{Buy, "closed at 100.00, 5% or more below its 3-day high close of 120.00, while above its 3-day average", ""}},
		{"exactly the pullback", closes("70.00", "100.00", "80.00", "95.00"), nil, want{Buy, entryAt95, ""}},
		{"exactly the pullback, held", closes("70.00", "100.00", "80.00", "95.00"), held("90.00"), want{Add, entryAt95, ""}},
		{"a cent short of the pullback", closes("70.00", "100.00", "80.00", "95.01"), nil,
			want{NoChange, "closed at 95.01, above its 3-day average; its 3-day high close is 100.00", ""}},
		{"falls below the average, held", closes("100.00", "100.00", "100.00", "90.00"), held("95.00"),
			want{Sell, "closed at 90.00, below its 3-day average", ""}},
		{"falls below the average, not held", closes("100.00", "100.00", "100.00", "90.00"), nil,
			want{NoChange, "closed at 90.00, below its 3-day average; not held", ""}},
		{"flat", closes("100.00", "100.00", "100.00", "100.00"), nil,
			want{NoChange, "closed at 100.00, level with its 3-day average; its 3-day high close is 100.00", ""}},
		{"too few bars", closes("100.00", "100.00", "100.00"), nil, want{NoChange, "needs 4 days of prices and has 3", ""}},
	})
}

func TestBarsOutOfOrder(t *testing.T) {
	bars := closes("10.00", "10.00", "10.00", "12.00")
	bars[2].Date = bars[1].Date
	for _, r := range []Rule{
		mustParse(t, config.RuleMovingAverageCross, "fast_days", "2", "slow_days", "3"),
		mustParse(t, config.RuleBreakout, "days", "2", "exit_days", "2"),
		mustParse(t, config.RulePullback, "trend_days", "2", "high_days", "2", "pullback", "0.05"),
	} {
		if _, err := r.(SignalRule).Decide(Input{Symbol: "TEST", Bars: bars}); !errors.Is(err, ErrBars) {
			t.Errorf("%s: err = %v, want ErrBars", r.Type(), err)
		}
	}
	for _, r := range []Rule{
		mustParse(t, config.RuleFixedStop, "distance", "0.1"),
		mustParse(t, config.RuleTrailingStop, "distance", "0.1"),
	} {
		if _, err := r.(StopRule).Stop(Input{Symbol: "TEST", Bars: bars, Holding: held("10.00")}); !errors.Is(err, ErrBars) {
			t.Errorf("%s: err = %v, want ErrBars", r.Type(), err)
		}
	}
	m := mustParse(t, config.RuleMomentumRanking, "days", "2", "top", "1", "exit_rank", "1").(RankingRule)
	if _, err := m.Rank([]Input{{Symbol: "TEST", Bars: bars}}); !errors.Is(err, ErrBars) {
		t.Errorf("momentum_ranking: err = %v, want ErrBars", err)
	}
}
