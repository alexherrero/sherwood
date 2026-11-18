package fill

import (
	"errors"
	"testing"

	"github.com/alexherrero/sherwood/internal/money"
)

func day(open, high, low string) Day {
	return Day{Open: money.MustParsePrice(open), High: money.MustParsePrice(high), Low: money.MustParsePrice(low)}
}

func TestApply(t *testing.T) {
	ten := money.MustParseQuantity("10")
	for _, tc := range []struct {
		name   string
		order  Order
		day    Day
		filled bool
		price  string
		value  string
		reason string
	}{
		{
			name:   "buy: open below the limit fills at the open",
			order:  Order{Kind: BuyLimit, Quantity: ten, Price: money.MustParsePrice("10.00")},
			day:    day("9.90", "10.40", "9.80"),
			filled: true, price: "9.90", value: "99.00",
			reason: "opened at 9.90, at or below its limit of 10.00",
		},
		{
			name:   "buy: open at the limit fills at the open",
			order:  Order{Kind: BuyLimit, Quantity: ten, Price: money.MustParsePrice("10.00")},
			day:    day("10.00", "10.40", "10.00"),
			filled: true, price: "10.00", value: "100.00",
			reason: "opened at 10.00, at or below its limit of 10.00",
		},
		{
			name:   "buy: opened above, traded through the limit, fills at the limit",
			order:  Order{Kind: BuyLimit, Quantity: ten, Price: money.MustParsePrice("10.00")},
			day:    day("10.20", "10.40", "9.95"),
			filled: true, price: "10.00", value: "100.00",
			reason: "traded down to 9.95, through its limit of 10.00",
		},
		{
			name:   "buy: low only touched the limit, no fill",
			order:  Order{Kind: BuyLimit, Quantity: ten, Price: money.MustParsePrice("10.00")},
			day:    day("10.20", "10.40", "10.00"),
			reason: "never traded below its limit of 10.00 (low 10.00)",
		},
		{
			name:   "buy: never reached the limit",
			order:  Order{Kind: BuyLimit, Quantity: ten, Price: money.MustParsePrice("10.00")},
			day:    day("10.20", "10.40", "10.05"),
			reason: "never traded below its limit of 10.00 (low 10.05)",
		},
		{
			name:   "stop: hit intraday fills at the stop",
			order:  Order{Kind: SellStop, Quantity: ten, Price: money.MustParsePrice("9.00")},
			day:    day("9.50", "9.60", "8.80"),
			filled: true, price: "9.00", value: "90.00",
			reason: "traded down to 8.80, reaching its stop of 9.00",
		},
		{
			name:   "stop: low exactly at the stop fills at the stop",
			order:  Order{Kind: SellStop, Quantity: ten, Price: money.MustParsePrice("9.00")},
			day:    day("9.50", "9.60", "9.00"),
			filled: true, price: "9.00", value: "90.00",
			reason: "traded down to 9.00, reaching its stop of 9.00",
		},
		{
			name:   "stop: gap below fills at the open",
			order:  Order{Kind: SellStop, Quantity: ten, Price: money.MustParsePrice("9.00")},
			day:    day("8.50", "8.70", "8.40"),
			filled: true, price: "8.50", value: "85.00",
			reason: "opened at 8.50, at or below its stop of 9.00",
		},
		{
			name:   "stop: opened at the stop fills at the open",
			order:  Order{Kind: SellStop, Quantity: ten, Price: money.MustParsePrice("9.00")},
			day:    day("9.00", "9.30", "9.00"),
			filled: true, price: "9.00", value: "90.00",
			reason: "opened at 9.00, at or below its stop of 9.00",
		},
		{
			name:   "stop: never reached",
			order:  Order{Kind: SellStop, Quantity: ten, Price: money.MustParsePrice("9.00")},
			day:    day("9.50", "9.60", "9.01"),
			reason: "stayed above its stop of 9.00 (low 9.01)",
		},
		{
			name:   "sell at the open",
			order:  Order{Kind: SellAtOpen, Quantity: ten},
			day:    day("9.50", "9.60", "9.01"),
			filled: true, price: "9.50", value: "95.00",
			reason: "sold at the open of 9.50",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, ok, err := Apply(tc.order, tc.day, money.WholeShares)
			if err != nil {
				t.Fatal(err)
			}
			if ok != tc.filled {
				t.Fatalf("filled = %v, want %v (%s)", ok, tc.filled, f.Reason)
			}
			if f.Reason != tc.reason {
				t.Errorf("reason = %q, want %q", f.Reason, tc.reason)
			}
			if !ok {
				if !f.Quantity.IsZero() || !f.Value.IsZero() {
					t.Errorf("an unfilled order has quantity %s and value %s", f.Quantity, f.Value)
				}
				return
			}
			if f.Price.String() != tc.price || f.Value.String() != tc.value || !f.Quantity.Equal(ten) {
				t.Errorf("fill = %s × %s = %s, want %s × %s = %s", f.Quantity, f.Price, f.Value, ten, tc.price, tc.value)
			}
		})
	}
}

func TestApplyFractionalShares(t *testing.T) {
	q := money.MustParseQuantity("2.5")
	o := Order{Kind: BuyLimit, Quantity: q, Price: money.MustParsePrice("10.00")}
	f, ok, err := Apply(o, day("9.99", "10.10", "9.90"), money.FractionalShares)
	if err != nil || !ok {
		t.Fatalf("ok %v, err %v", ok, err)
	}
	if f.Value.String() != "24.975" {
		t.Errorf("value = %s, want the exact 24.975", f.Value)
	}
	if _, _, err := Apply(o, day("9.99", "10.10", "9.90"), money.WholeShares); !errors.Is(err, ErrOrder) {
		t.Errorf("a fractional count under the whole-share policy gave %v, want ErrOrder", err)
	}
}

func TestApplyRefuses(t *testing.T) {
	ten := money.MustParseQuantity("10")
	good := day("10.00", "10.50", "9.50")
	for name, tc := range map[string]struct {
		order  Order
		day    Day
		policy money.QuantityPolicy
		want   error
	}{
		"no shares":       {Order{Kind: BuyLimit, Price: money.MustParsePrice("10.00")}, good, money.WholeShares, ErrOrder},
		"no limit":        {Order{Kind: BuyLimit, Quantity: ten}, good, money.WholeShares, ErrOrder},
		"no stop":         {Order{Kind: SellStop, Quantity: ten}, good, money.WholeShares, ErrOrder},
		"unknown kind":    {Order{Kind: "sell_limit", Quantity: ten, Price: money.MustParsePrice("10.00")}, good, money.WholeShares, ErrOrder},
		"no policy":       {Order{Kind: SellAtOpen, Quantity: ten}, good, 0, ErrOrder},
		"low above open":  {Order{Kind: SellAtOpen, Quantity: ten}, day("10.00", "10.50", "10.10"), money.WholeShares, ErrDay},
		"high below open": {Order{Kind: SellAtOpen, Quantity: ten}, day("10.00", "9.90", "9.50"), money.WholeShares, ErrDay},
	} {
		if _, ok, err := Apply(tc.order, tc.day, tc.policy); !errors.Is(err, tc.want) || ok {
			t.Errorf("%s: ok %v, err %v, want %v", name, ok, err, tc.want)
		}
	}
}
