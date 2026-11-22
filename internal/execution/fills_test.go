package execution

import (
	"context"
	"strings"
	"testing"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/broker/brokertest"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/signals"
)

// Live fills reach the holdings and cash: at place time, when the broker
// fills an order at once, and when a lookup finds one filled. Every symbol,
// price and amount here is made up; the harness starts with 10000.00 cash.

// wantBook checks the newest holdings and cash: shares of each stock held,
// the cash, and that both came from fills.
func (h *harness) wantBook(t *testing.T, held map[string]string, cash string) {
	t.Helper()
	ctx := context.Background()
	set, err := holdings.Current(ctx, h.store.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if set.Source != holdings.SourceFills || len(set.Holdings) != len(held) {
		t.Errorf("holdings = %+v, want %v from fills", set, held)
	}
	for sym, n := range held {
		if got, ok := set.Find(sym); !ok || got.Quantity.String() != n {
			t.Errorf("%s: holding %+v, want %s shares", sym, got, n)
		}
	}
	c, err := holdings.LatestCash(ctx, h.store.Reader())
	if err != nil || c.Amount.String() != cash || c.Source != holdings.SourceFills {
		t.Errorf("cash = %+v, %v; want %s from fills", c, err, cash)
	}
}

func (h *harness) fills(t *testing.T, id int64) []orders.Fill {
	t.Helper()
	fs, err := orders.Fills(context.Background(), h.store.Reader(), id)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestABuyFilledWhenPlacedIsRecordedWithTheHoldingsAndDoesNotHalt(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, nil)
	buy := h.approved(t, signals.KindBuy, "AAA", "2", p("10.10"))
	h.fake.Answer(brokertest.Place, broker.Report{Ref: buy.Key, Status: broker.StatusFilled, Filled: q("2"),
		FillPrice: p("10.05")}, nil)
	res := h.send(t)
	if len(res.Sent) != 1 || res.Sent[0].State != orders.Filled {
		t.Fatalf("sent %+v, want the buy filled", res.Sent)
	}
	if o := h.get(t, buy.ID); o.State != orders.Filled || h.lastActor(t, o.ID) != orders.Broker {
		t.Errorf("the buy is %s by %s, want filled by the broker", o.State, h.lastActor(t, o.ID))
	}
	if fs := h.fills(t, buy.ID); len(fs) != 1 || fs[0].Price.String() != "10.05" ||
		!strings.HasPrefix(fs[0].Reason, "Filled when it was placed") {
		t.Errorf("fills = %+v, want 2 at 10.05", fs)
	}
	h.wantBook(t, map[string]string{"AAA": "2"}, "9979.90")
	if h.halted(t).On() || h.fake.Count(brokertest.Place) != 1 {
		t.Errorf("halts %+v and %d places; want no halt and one place", h.halted(t).Active, h.fake.Count(brokertest.Place))
	}
}

func TestASellFilledWhenPlacedLowersTheHoldings(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10", "BBB": "4"})
	sell := h.approved(t, signals.KindSell, "AAA", "10", nil)
	h.fake.Answer(brokertest.Place, broker.Report{Ref: sell.Key, Status: broker.StatusFilled, Filled: q("10"),
		FillPrice: p("11.00")}, nil)
	h.send(t)
	if o := h.get(t, sell.ID); o.State != orders.Filled {
		t.Errorf("the sell is %s, want filled", o.State)
	}
	h.wantBook(t, map[string]string{"BBB": "4"}, "10110.00")
	if h.halted(t).On() {
		t.Errorf("halts %+v, want none", h.halted(t).Active)
	}
}

func TestAPartFillWhenPlacedLeavesTheOrderOpen(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, nil)
	buy := h.approved(t, signals.KindBuy, "AAA", "5", p("10.10"))
	h.fake.Answer(brokertest.Place, broker.Report{Ref: buy.Key, Status: broker.StatusPartiallyFilled, Filled: q("2"),
		FillPrice: p("10.00")}, nil)
	h.send(t)
	if o := h.get(t, buy.ID); o.State != orders.PartiallyFilled {
		t.Errorf("the buy is %s, want partially_filled", o.State)
	}
	h.wantBook(t, map[string]string{"AAA": "2"}, "9980.00")
	if h.halted(t).On() {
		t.Errorf("halts %+v, want none", h.halted(t).Active)
	}
}

func TestAFillWhenPlacedThatCannotBeRecordedLeavesTheOrderUnknown(t *testing.T) {
	for name, rep := range map[string]broker.Report{
		"no price":                 {Status: broker.StatusFilled, Filled: q("2")},
		"more than ordered":        {Status: broker.StatusFilled, Filled: q("3"), FillPrice: p("10.00")},
		"a filled status for part": {Status: broker.StatusFilled, Filled: q("1"), FillPrice: p("10.00")},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, passAll{})
			h.hold(t, nil)
			buy := h.approved(t, signals.KindBuy, "AAA", "2", p("10.10"))
			rep.Ref = buy.Key
			h.fake.Answer(brokertest.Place, rep, nil)
			h.clock.Set(sendTime)
			if _, err := h.ex.Send(context.Background(), session); err == nil {
				t.Fatal("Send succeeded, want a halt")
			}
			if o := h.get(t, buy.ID); o.State != orders.Unknown {
				t.Errorf("the buy is %s, want unknown", o.State)
			}
			if fs := h.fills(t, buy.ID); len(fs) != 0 {
				t.Errorf("fills = %+v, want none", fs)
			}
			if !h.halted(t).Has(halt.UnknownOrder) {
				t.Errorf("halts %+v, want unknown_order", h.halted(t).Active)
			}
		})
	}
}

func TestTheFifteenMinuteCheckPutsAFillInTheHoldings(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	stop := h.resting(t, "AAA", "10", "9.00")
	buy := h.order(t, signals.KindBuy, "BBB", "2", p("20.20"), session,
		orders.AwaitingApproval, orders.Approved, orders.Submitted, orders.Accepted)
	h.agree(nil, stop)
	h.fake.Answer(brokertest.Lookup, broker.Report{Ref: buy.Key, Status: broker.StatusFilled, Filled: q("2"),
		FillPrice: p("20.10")}, nil)
	if res := h.openCheck(t); len(res.Differences) != 0 {
		t.Fatalf("differences %q, want none", res.Differences)
	}
	h.wantBook(t, map[string]string{"AAA": "10", "BBB": "2"}, "9959.80")
}

func TestAStopFilledAtTheBrokerLowersTheHoldings(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	stop := h.resting(t, "AAA", "10", "9.00")
	h.agree(nil)
	h.fake.Acct.Cash = h.fake.Acct.Cash.Add(p("8.95").Mul(q("10")))
	h.fake.Answer(brokertest.Lookup, broker.Report{Ref: stop.Key, Status: broker.StatusFilled, Filled: q("10"),
		FillPrice: p("8.95")}, nil)
	res := h.reconcile(t)
	if len(res.Differences) != 0 || h.halted(t).On() {
		t.Fatalf("differences %q, halts %+v; want none", res.Differences, h.halted(t).Active)
	}
	h.wantBook(t, nil, "10089.50")
}

func TestASellOfSharesNotHeldIsADifferenceAndRecordsNothing(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	sell := h.order(t, signals.KindSell, "BBB", "3", nil, session,
		orders.AwaitingApproval, orders.Approved, orders.Submitted, orders.Unknown)
	h.agree(map[string]string{"AAA": "10"})
	h.fake.Answer(brokertest.Lookup, broker.Report{Ref: sell.Key, Status: broker.StatusFilled, Filled: q("3"),
		FillPrice: p("20.00")}, nil)
	res := h.reconcile(t)
	if !strings.Contains(strings.Join(res.Differences, " "), "which sherwood's holdings cannot take") {
		t.Errorf("differences %q, want the sell that cannot be taken", res.Differences)
	}
	if o := h.get(t, sell.ID); o.State != orders.Unknown || len(h.fills(t, sell.ID)) != 0 {
		t.Errorf("the sell is %s with fills; want unknown with none", o.State)
	}
	if !h.halted(t).Has(halt.Reconciliation) {
		t.Errorf("halts %+v, want reconciliation", h.halted(t).Active)
	}
}

// A resting stop the broker cancels or expires halts trading for review,
// and the next 09:35 send places a new stop, even while halted, at no less
// than the lost stop's price.
func TestALostStopIsReplacedAtTheNextSendNeverLower(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	stop := h.resting(t, "AAA", "10", "9.00")
	h.suggest(t, "AAA", "8.50") // lower than the lost stop
	h.agree(map[string]string{"AAA": "10"})
	h.fake.Answer(brokertest.Lookup, broker.Report{Ref: stop.Key, Status: broker.StatusExpired}, nil)
	res := h.reconcile(t)
	if len(res.Differences) != 1 || !strings.Contains(res.Differences[0], "is expired at the broker") {
		t.Fatalf("differences %q, want the lost stop", res.Differences)
	}
	if o := h.get(t, stop.ID); o.State != orders.Expired || h.lastActor(t, o.ID) != orders.Reconciliation {
		t.Errorf("the stop is %s by %s, want expired by reconciliation", o.State, h.lastActor(t, o.ID))
	}
	if !h.halted(t).Has(halt.Reconciliation) {
		t.Fatalf("halts %+v, want reconciliation", h.halted(t).Active)
	}

	h.clock.Set(sendTime)
	if _, err := h.ex.Send(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	stops := h.openStops(t)
	if len(stops) != 1 || stops[0].Price.String() != "9.00" || stops[0].Quantity.String() != "10" {
		t.Fatalf("resting stops %+v, want one of 10 shares at 9.00", stops)
	}
	lost, err := lostStops(context.Background(), h.store.Reader())
	if err != nil || len(lost) != 0 {
		t.Errorf("lost stops after the new one = %v, %v; want none", lost, err)
	}
}

// A stop sherwood itself called off is not lost: the next send uses the
// suggestion alone.
func TestAStopSherwoodCalledOffIsNotLost(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	h.order(t, signals.KindStopSell, "AAA", "10", p("9.00"), prev,
		orders.Approved, orders.Submitted, orders.Accepted, orders.Cancelled)
	lost, err := lostStops(context.Background(), h.store.Reader())
	if err != nil || len(lost) != 0 {
		t.Errorf("lost stops = %v, %v; want none", lost, err)
	}
}
