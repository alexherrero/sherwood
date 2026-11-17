package rules

import (
	"errors"
	"testing"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
)

type sizeCase struct {
	name     string
	in       SizeInput
	quantity string
	reason   string
	err      error
}

func runSize(t *testing.T, r Rule, cases []sizeCase) {
	t.Helper()
	sr, ok := r.(SizingRule)
	if !ok {
		t.Fatalf("%s is not a SizingRule", r.Type())
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sr.Size(c.in)
			if c.err != nil {
				if !errors.Is(err, c.err) {
					t.Fatalf("err = %v, want %v", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Quantity.String() != c.quantity {
				t.Errorf("quantity = %s, want %s", got.Quantity, c.quantity)
			}
			if got.Reason != c.reason {
				t.Errorf("reason = %q\n                want %q", got.Reason, c.reason)
			}
			again, _ := sr.Size(c.in)
			if !again.Quantity.Equal(got.Quantity) || again.Reason != got.Reason {
				t.Errorf("second run differs: %+v", again)
			}
		})
	}
}

func TestSizeFixedAmount(t *testing.T) {
	r := mustParse(t, config.RuleSizeFixedAmount, "amount", "100.00")
	whole, frac := money.WholeShares, money.FractionalShares
	runSize(t, r, []sizeCase{
		{"exact", SizeInput{Price: price("25.00"), Policy: whole}, "4", "100.00 at 25.00 a share buys 4 shares", nil},
		{"rounds down", SizeInput{Price: price("30.00"), Policy: whole}, "3", "100.00 at 30.00 a share buys 3 shares", nil},
		{"fractional rounds down", SizeInput{Price: price("30.00"), Policy: frac}, "3.333333",
			"100.00 at 30.00 a share buys 3.333333 shares", nil},
		{"one share", SizeInput{Price: price("60.00"), Policy: whole}, "1", "100.00 at 60.00 a share buys 1 share", nil},
		{"too dear for a share", SizeInput{Price: price("250.00"), Policy: whole}, "0",
			"100.00 at 250.00 a share buys 0 shares", nil},
		{"zero price", SizeInput{Policy: whole}, "", "", ErrNoPrice},
		{"no policy chosen", SizeInput{Price: price("25.00")}, "", "", money.ErrPolicy},
	})
}

func TestSizeByRisk(t *testing.T) {
	r := mustParse(t, config.RuleSizeByRisk, "risk_per_trade", "0.01")
	whole, frac := money.WholeShares, money.FractionalShares
	capital := money.MustParseMoney("50000.00")
	in := func(p, stop string, policy money.QuantityPolicy) SizeInput {
		s := SizeInput{Price: price(p), Capital: capital, Policy: policy}
		if stop != "" {
			s.Stop = pricePtr(stop)
		}
		return s
	}
	runSize(t, r, []sizeCase{
		{"exact", in("100.00", "95.00", whole), "100",
			"risking 500.00 (1% of 50000.00) at 5.00 a share (price 100.00, stop 95.00) buys 100 shares", nil},
		{"rounds down", in("100.00", "93.00", whole), "71",
			"risking 500.00 (1% of 50000.00) at 7.00 a share (price 100.00, stop 93.00) buys 71 shares", nil},
		{"fractional rounds down", in("100.00", "93.00", frac), "71.428571",
			"risking 500.00 (1% of 50000.00) at 7.00 a share (price 100.00, stop 93.00) buys 71.428571 shares", nil},
		{"stop at the price", in("100.00", "100.00", whole), "0",
			"its stop of 100.00 is not below the price of 100.00, so it buys no shares", nil},
		{"no capital", SizeInput{Price: price("100.00"), Stop: pricePtr("95.00"), Policy: whole}, "0",
			"risking 0.00 (1% of 0.00) at 5.00 a share (price 100.00, stop 95.00) buys 0 shares", nil},
		{"no stop", in("100.00", "", whole), "", "", ErrNoStop},
		{"zero price", in("0.00", "0.00", whole), "", "", ErrNoPrice},
		{"no policy chosen", in("100.00", "95.00", 0), "", "", money.ErrPolicy},
	})
}
