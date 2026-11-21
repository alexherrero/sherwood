package brokertest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/money"
)

// fakeHarness runs the conformance suite against a tracking Fake. Every
// symbol, price and amount is made up.
type fakeHarness struct {
	f *Fake
	n int
}

func (h *fakeHarness) Adapter() broker.Adapter { return h.f }

func (h *fakeHarness) Order(_ *testing.T, typ broker.Type) broker.Order {
	h.n++
	o := broker.Order{Ref: fmt.Sprintf("key-%d", h.n), Symbol: "ZZA", Side: broker.Buy, Type: typ,
		Quantity: money.MustParseQuantity("2"), Session: time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)}
	if typ != broker.DayLimitBuy {
		o.Side = broker.Sell
	}
	if typ != broker.SellAtOpen {
		p := money.MustParsePrice("12.34")
		o.Price = &p
	}
	return o
}

func (h *fakeHarness) Refused(t *testing.T) broker.Order {
	o := h.Order(t, broker.DayLimitBuy)
	h.f.Answer(Place, broker.Report{Ref: o.Ref, Status: broker.StatusRejected, Detail: "made-up refusal"}, nil)
	return o
}

func (h *fakeHarness) Placed(*testing.T, broker.Order, broker.Report) {}
func (h *fakeHarness) Cancelled(*testing.T, string)                   {}
func (h *fakeHarness) Unknown(*testing.T) string                      { return "never-placed" }

func (h *fakeHarness) Hold(_ *testing.T, cash money.Money, ps []broker.Position) broker.Account {
	h.f.Positions = ps
	h.f.Acct = broker.Account{Cash: cash, Value: cash.Add(money.MustParseMoney("100.00"))}
	return h.f.Acct
}

func (h *fakeHarness) Break(_ *testing.T, m Method) bool {
	h.f.Fail(m, errors.New("made-up broker failure"))
	return true
}

func (h *fakeHarness) SetHealthy(_ *testing.T, ok bool) { h.f.SetHealthy(ok) }

func TestFakeConforms(t *testing.T) {
	Run(t, func(*testing.T) Harness { return &fakeHarness{f: &Fake{Track: true}} })
}

func TestUntrackedFakeKeepsItsOldDefaults(t *testing.T) {
	f := &Fake{}
	ctx := t.Context()
	if r, err := f.Lookup(ctx, "any"); err != nil || r.Status != broker.StatusAccepted {
		t.Errorf("Lookup = %+v, %v; want accepted", r, err)
	}
	if r, err := f.Cancel(ctx, "any"); err != nil || r.Status != broker.StatusCancelled {
		t.Errorf("Cancel = %+v, %v; want cancelled", r, err)
	}
	if open, err := f.OpenOrders(ctx); err != nil || len(open) != 0 {
		t.Errorf("OpenOrders = %v, %v; want none", open, err)
	}
	if f.Count(Lookup) != 1 || f.Count(Cancel) != 1 || f.Count(OpenOrders) != 1 {
		t.Errorf("calls = %v", f.Calls())
	}
}
