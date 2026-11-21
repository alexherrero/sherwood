package brokertest

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/money"
)

// Cash names the CashReader's call, for Break. It is not an Adapter method.
const Cash Method = "cash"

// Harness sets up one adapter for the conformance suite. Run asks its factory
// for a fresh Harness in every subtest, so state never leaks between them.
// Everything a harness arranges is made up: no harness reaches a broker.
type Harness interface {
	// Adapter is the adapter under test.
	Adapter() broker.Adapter
	// Order returns a well-formed order of type typ that the broker will
	// take, with a fresh reference and quantity and price the broker
	// accepts. Each call returns a different reference.
	Order(t *testing.T, typ broker.Type) broker.Order
	// Refused returns a well-formed order the broker will refuse.
	Refused(t *testing.T) broker.Order
	// Placed tells the harness the caller recorded r for o, as the execution
	// layer records each answer; Cancelled tells it the caller recorded an
	// order called off. An adapter whose book is the caller's own records
	// (the paper broker) needs them; any other ignores them.
	Placed(t *testing.T, o broker.Order, r broker.Report)
	Cancelled(t *testing.T, ref string)
	// Unknown returns a reference the broker has no order for.
	Unknown(t *testing.T) string
	// Hold makes the broker hold ps and cash, and returns the account the
	// adapter should report for them.
	Hold(t *testing.T, cash money.Money, ps []broker.Position) broker.Account
	// Break makes the next call of m fail, as a broker call whose outcome
	// is unknown does. It reports false when m cannot fail at this adapter
	// (the paper broker's Place reads and writes nothing), and the check is
	// skipped.
	Break(t *testing.T, m Method) bool
}

// HealthHarness is a Harness whose adapter reports its health and whose
// health the harness can set.
type HealthHarness interface {
	Harness
	SetHealthy(t *testing.T, ok bool)
}

// Run runs the conformance suite: what every broker.Adapter must do, whatever
// broker is behind it. Each subtest calls newHarness for a fresh harness.
func Run(t *testing.T, newHarness func(t *testing.T) Harness) {
	t.Helper()
	ctx := context.Background()

	t.Run("place accepts each order type and lookup finds it", func(t *testing.T) {
		h := newHarness(t)
		a := h.Adapter()
		for _, typ := range []broker.Type{broker.DayLimitBuy, broker.Stop, broker.SellAtOpen} {
			o := h.Order(t, typ)
			r, err := a.Place(ctx, o)
			if err != nil {
				t.Fatalf("Place(%s): %v", typ, err)
			}
			wantReport(t, "Place "+string(typ), r, o.Ref, broker.StatusAccepted)
			if !r.Filled.IsZero() || r.FillPrice != nil {
				t.Errorf("Place(%s) reports a fill before any could happen: %+v", typ, r)
			}
			h.Placed(t, o, r)
			got, err := a.Lookup(ctx, o.Ref)
			if err != nil {
				t.Fatalf("Lookup(%s): %v", typ, err)
			}
			wantReport(t, "Lookup "+string(typ), got, o.Ref, broker.StatusAccepted)
		}
	})

	t.Run("a malformed order is refused, not an error", func(t *testing.T) {
		h := newHarness(t)
		good := h.Order(t, broker.DayLimitBuy)
		price := money.MustParsePrice("1.00")
		cases := map[string]func(o *broker.Order){
			"no reference": func(o *broker.Order) { o.Ref = "" },
			"no symbol":    func(o *broker.Order) { o.Symbol = "" },
			"no shares":    func(o *broker.Order) { o.Quantity = money.Quantity{} },
			"a sell limit": func(o *broker.Order) { o.Side = broker.Sell },
			"no price":     func(o *broker.Order) { o.Price = nil },
			"unknown type": func(o *broker.Order) { o.Type = "trailing_stop" },
			"a priced sell at the open": func(o *broker.Order) {
				o.Type, o.Side, o.Price = broker.SellAtOpen, broker.Sell, &price
			},
		}
		for name, spoil := range cases {
			o := good
			spoil(&o)
			r, err := h.Adapter().Place(ctx, o)
			if err != nil {
				t.Errorf("%s: Place returned an error, want a refusal: %v", name, err)
				continue
			}
			wantReport(t, name, r, o.Ref, broker.StatusRejected)
			if r.Detail == "" {
				t.Errorf("%s: the refusal gives no reason", name)
			}
		}
	})

	t.Run("a refusal is a report with its reason", func(t *testing.T) {
		h := newHarness(t)
		o := h.Refused(t)
		if err := o.Check(); err != nil {
			t.Fatalf("the harness's refused order is malformed: %v", err)
		}
		r, err := h.Adapter().Place(ctx, o)
		if err != nil {
			t.Fatalf("Place: %v, want a refusal", err)
		}
		wantReport(t, "Place", r, o.Ref, broker.StatusRejected)
		if r.Detail == "" {
			t.Error("the refusal gives no reason")
		}
	})

	t.Run("cancel calls an order off, and again harmlessly", func(t *testing.T) {
		h := newHarness(t)
		a := h.Adapter()
		keep, drop := h.Order(t, broker.Stop), h.Order(t, broker.DayLimitBuy)
		for _, o := range []broker.Order{keep, drop} {
			r, err := a.Place(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			h.Placed(t, o, r)
		}
		r, err := a.Cancel(ctx, drop.Ref)
		if err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		wantReport(t, "Cancel", r, drop.Ref, broker.StatusCancelled)
		h.Cancelled(t, drop.Ref)
		r, err = a.Cancel(ctx, drop.Ref)
		if err != nil {
			t.Fatalf("a second Cancel: %v", err)
		}
		wantReport(t, "a second Cancel", r, drop.Ref, broker.StatusCancelled)
		r, err = a.Lookup(ctx, drop.Ref)
		if err != nil {
			t.Fatalf("Lookup after Cancel: %v", err)
		}
		wantReport(t, "Lookup after Cancel", r, drop.Ref, broker.StatusCancelled)

		open, err := a.OpenOrders(ctx)
		if err != nil {
			t.Fatalf("OpenOrders: %v", err)
		}
		var refs []string
		for _, r := range open {
			refs = append(refs, r.Ref)
			if r.Status != broker.StatusAccepted && r.Status != broker.StatusPartiallyFilled {
				t.Errorf("OpenOrders lists %s as %s", r.Ref, r.Status)
			}
		}
		if !slices.Equal(refs, []string{keep.Ref}) {
			t.Errorf("OpenOrders lists %v, want only the order still open, %s", refs, keep.Ref)
		}
	})

	t.Run("an order the broker does not have is an error", func(t *testing.T) {
		h := newHarness(t)
		ref := h.Unknown(t)
		if _, err := h.Adapter().Lookup(ctx, ref); err == nil {
			t.Error("Lookup of an unknown order succeeded")
		}
		if _, err := h.Adapter().Cancel(ctx, ref); err == nil {
			t.Error("Cancel of an unknown order succeeded")
		}
	})

	t.Run("nothing open, held or in cash", func(t *testing.T) {
		h := newHarness(t)
		want := h.Hold(t, money.Money{}, nil)
		open, err := h.Adapter().OpenOrders(ctx)
		if err != nil || len(open) != 0 {
			t.Errorf("OpenOrders = %v, %v; want none", open, err)
		}
		held, err := h.Adapter().Holdings(ctx)
		if err != nil || len(held) != 0 {
			t.Errorf("Holdings = %v, %v; want none", held, err)
		}
		acct, err := h.Adapter().Account(ctx)
		if err != nil || !acct.Cash.Equal(want.Cash) || !acct.Value.Equal(want.Value) {
			t.Errorf("Account = %+v, %v; want %+v", acct, err, want)
		}
	})

	t.Run("holdings, cash and the account", func(t *testing.T) {
		h := newHarness(t)
		cash := money.MustParseMoney("1234.56")
		ps := []broker.Position{
			{Symbol: "ZZA", Quantity: money.MustParseQuantity("3"), AverageCost: money.MustParseMoney("41.50")},
			{Symbol: "ZZB", Quantity: money.MustParseQuantity("10"), AverageCost: money.MustParseMoney("12.34")},
		}
		want := h.Hold(t, cash, ps)
		a := h.Adapter()
		held, err := a.Holdings(ctx)
		if err != nil {
			t.Fatalf("Holdings: %v", err)
		}
		slices.SortFunc(held, func(x, y broker.Position) int { return strings.Compare(x.Symbol, y.Symbol) })
		if len(held) != len(ps) {
			t.Fatalf("Holdings = %v, want %v", held, ps)
		}
		for i := range ps {
			g, w := held[i], ps[i]
			if g.Symbol != w.Symbol || !g.Quantity.Equal(w.Quantity) || !g.AverageCost.Equal(w.AverageCost) {
				t.Errorf("holding %d = %+v, want %+v", i, g, w)
			}
		}
		acct, err := a.Account(ctx)
		if err != nil {
			t.Fatalf("Account: %v", err)
		}
		if !acct.Cash.Equal(want.Cash) || !acct.Value.Equal(want.Value) || !acct.Cash.Equal(cash) {
			t.Errorf("Account = %+v, want %+v with cash %s", acct, want, cash)
		}
		if c, ok := a.(broker.CashReader); ok {
			got, err := c.Cash(ctx)
			if err != nil || !got.Equal(cash) {
				t.Errorf("Cash = %s, %v; want %s", got, err, cash)
			}
		}
	})

	for _, m := range []Method{Place, Cancel, Lookup, OpenOrders, Holdings, Account, Cash} {
		t.Run("a failed "+string(m)+" call is an error", func(t *testing.T) {
			h := newHarness(t)
			a := h.Adapter()
			c, isCashReader := a.(broker.CashReader)
			if m == Cash && !isCashReader {
				t.Skip("the adapter reads cash only through Account")
			}
			placed, another := h.Order(t, broker.DayLimitBuy), h.Order(t, broker.DayLimitBuy)
			if m == Cancel || m == Lookup {
				r, err := a.Place(ctx, placed)
				if err != nil {
					t.Fatal(err)
				}
				h.Placed(t, placed, r)
			}
			if !h.Break(t, m) {
				t.Skipf("%s cannot fail at this adapter", m)
			}
			var err error
			switch m {
			case Place:
				var r broker.Report
				r, err = a.Place(ctx, another)
				if err != nil && r.Status != "" {
					t.Errorf("a failed Place also reported %+v", r)
				}
			case Cancel:
				_, err = a.Cancel(ctx, placed.Ref)
			case Lookup:
				_, err = a.Lookup(ctx, placed.Ref)
			case OpenOrders:
				_, err = a.OpenOrders(ctx)
			case Holdings:
				_, err = a.Holdings(ctx)
			case Account:
				_, err = a.Account(ctx)
			case Cash:
				_, err = c.Cash(ctx)
			}
			if err == nil {
				t.Errorf("%s succeeded after the harness broke it", m)
			}
		})
	}

	t.Run("health", func(t *testing.T) {
		h := newHarness(t)
		hh, ok := h.Adapter().(broker.Health)
		if !ok {
			t.Skip("the adapter has no health of its own, so it counts as healthy")
		}
		setter, ok := h.(HealthHarness)
		if !ok {
			t.Fatal("the adapter reports its health, but the harness cannot set it")
		}
		setter.SetHealthy(t, false)
		if hh.Healthy() {
			t.Error("Healthy after the harness made it unwell")
		}
		setter.SetHealthy(t, true)
		if !hh.Healthy() {
			t.Error("not Healthy after the harness made it well")
		}
	})
}

// wantReport checks a report is about ref and has status want.
func wantReport(t *testing.T, what string, r broker.Report, ref string, want broker.Status) {
	t.Helper()
	if r.Ref != ref {
		t.Errorf("%s: report is about %q, want %q", what, r.Ref, ref)
	}
	if r.Status != want {
		t.Errorf("%s: status %q (detail %q), want %q", what, r.Status, r.Detail, want)
	}
}
