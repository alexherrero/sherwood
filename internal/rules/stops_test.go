package rules

import (
	"testing"

	"github.com/alexherrero/sherwood/internal/config"
)

type stopCase struct {
	name    string
	bars    []Bar
	holding *Holding
	want    want
}

func runStop(t *testing.T, r Rule, cases []stopCase) {
	t.Helper()
	sr, ok := r.(StopRule)
	if !ok {
		t.Fatalf("%s is not a StopRule", r.Type())
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := Input{Symbol: "TEST", Bars: c.bars, Holding: c.holding}
			got, err := sr.Stop(in)
			if err != nil {
				t.Fatal(err)
			}
			check(t, got, "TEST", c.want)
			again, err := sr.Stop(in)
			if err != nil || again.Outcome != got.Outcome || again.Reason != got.Reason || stopText(again.Stop) != stopText(got.Stop) {
				t.Errorf("second run differs: %+v, %v", again, err)
			}
		})
	}
}

func TestFixedStop(t *testing.T) {
	r := mustParse(t, config.RuleFixedStop, "distance", "0.10")
	runStop(t, r, []stopCase{
		{"above the stop", closes("95.00"), held("100.00"),
			want{NoChange, "its stop is 90.00, 10% under its cost of 100.00", "90.00"}},
		{"closes at the stop", closes("95.00", "90.00"), held("100.00"),
			want{Sell, "closed at 90.00, at or below its stop of 90.00", "90.00"}},
		{"closes below the stop", closes("85.00"), held("100.00"),
			want{Sell, "closed at 85.00, at or below its stop of 90.00", "90.00"}},
		// 33.33 * 0.9 = 29.997, rounded to the cent.
		{"stop rounds to the cent", closes("31.00"), held("33.33"),
			want{NoChange, "its stop is 30.00, 10% under its cost of 33.33", "30.00"}},
		{"not held", closes("95.00"), nil, want{NoChange, "not held", "none"}},
		{"no bars", nil, held("100.00"), want{NoChange, "needs 1 day of prices and has 0", "none"}},
	})
	half := mustParse(t, config.RuleFixedStop, "distance", "0.05")
	// 10.10 * 0.95 = 9.595: exactly half a cent, which rounds away from zero.
	runStop(t, half, []stopCase{
		{"half a cent rounds up", closes("10.00"), held("10.10"),
			want{NoChange, "its stop is 9.60, 5% under its cost of 10.10", "9.60"}},
	})
}

func TestTrailingStop(t *testing.T) {
	r := mustParse(t, config.RuleTrailingStop, "distance", "0.10")
	// Bought on day 1; day 0's high close of 200 is from before and is ignored.
	sinceDay1 := func() *Holding { h := held("100.00"); h.Opened = day(1); return h }
	withStop := func(s string) *Holding { h := sinceDay1(); h.Stop = pricePtr(s); return h }
	runStop(t, r, []stopCase{
		{"rises with the highest close", closes("200.00", "100.00", "120.00", "115.00"), sinceDay1(),
			want{NoChange, "its trailing stop is 108.00, 10% under its highest close since it was bought, 120.00", "108.00"}},
		{"never lowered", closes("200.00", "100.00", "120.00", "115.00"), withStop("110.00"),
			want{NoChange, "its trailing stop is 110.00, kept, as a trailing stop is never lowered", "110.00"}},
		{"raised above the stop in force", closes("200.00", "100.00", "120.00", "115.00"), withStop("95.00"),
			want{NoChange, "its trailing stop is 108.00, 10% under its highest close since it was bought, 120.00", "108.00"}},
		{"closes at the stop", closes("200.00", "100.00", "120.00", "108.00"), sinceDay1(),
			want{Sell, "closed at 108.00, at or below its trailing stop of 108.00", "108.00"}},
		{"closes below the stop in force", closes("200.00", "100.00", "120.00", "109.00"), withStop("110.00"),
			want{Sell, "closed at 109.00, at or below its trailing stop of 110.00", "110.00"}},
		{"no prices since bought", closes("100.00"), withStop("90.00"),
			want{NoChange, "has no prices since it was bought on 2026-01-06; its trailing stop stays at 90.00", "90.00"}},
		{"no prices and no stop", closes("100.00"), sinceDay1(),
			want{NoChange, "has no prices since it was bought on 2026-01-06", "none"}},
		{"not held", closes("100.00"), nil, want{NoChange, "not held", "none"}},
	})
}
