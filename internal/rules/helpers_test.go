package rules

import (
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
)

// Every price in these tests is made up.

// day0 is the first test trading date. Bars step one calendar day at a time;
// the rules only need dates in order.
var day0 = time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)

func day(i int) time.Time { return day0.AddDate(0, 0, i) }

func price(s string) money.Price { return money.MustParsePrice(s) }

func pricePtr(s string) *money.Price {
	p := price(s)
	return &p
}

// closes builds one bar per close, with the high and low equal to the close.
func closes(cs ...string) []Bar {
	bars := make([]Bar, len(cs))
	for i, c := range cs {
		p := price(c)
		bars[i] = Bar{Date: day(i), High: p, Low: p, Close: p}
	}
	return bars
}

// hlc builds bars from high, low, close triples.
func hlc(triples ...[3]string) []Bar {
	bars := make([]Bar, len(triples))
	for i, t := range triples {
		bars[i] = Bar{Date: day(i), High: price(t[0]), Low: price(t[1]), Close: price(t[2])}
	}
	return bars
}

// held is a holding of 10 shares at cost, opened on the first test date.
func held(cost string) *Holding {
	return &Holding{Quantity: money.MustParseQuantity("10"), Cost: price(cost), Opened: day0}
}

// settings builds a settings map from name, value pairs.
func settings(kv ...string) map[string]config.Scalar {
	out := map[string]config.Scalar{}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = config.NewScalar(kv[i+1])
	}
	return out
}

// mustParse parses a rule entry or fails the test.
func mustParse(t *testing.T, ruleType string, kv ...string) Rule {
	t.Helper()
	r, err := Parse(config.Rule{Type: ruleType, Settings: settings(kv...)})
	if err != nil {
		t.Fatalf("parse %s: %v", ruleType, err)
	}
	return r
}

// stopText formats an optional stop for comparison.
func stopText(p *money.Price) string {
	if p == nil {
		return "none"
	}
	return p.String()
}

// want is an expected decision.
type want struct {
	outcome Outcome
	reason  string
	stop    string // "none" when no stop is expected; ignored when ""
}

func check(t *testing.T, got Decision, symbol string, w want) {
	t.Helper()
	if got.Symbol != symbol {
		t.Errorf("symbol = %q, want %q", got.Symbol, symbol)
	}
	if got.Outcome != w.outcome {
		t.Errorf("outcome = %s, want %s (reason %q)", got.Outcome, w.outcome, got.Reason)
	}
	if got.Reason != w.reason {
		t.Errorf("reason = %q\n                want %q", got.Reason, w.reason)
	}
	if w.stop != "" && stopText(got.Stop) != w.stop {
		t.Errorf("stop = %s, want %s", stopText(got.Stop), w.stop)
	}
}
