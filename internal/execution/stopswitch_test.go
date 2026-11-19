package execution

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alexherrero/sherwood/internal/broker/brokertest"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/risk"
	"github.com/alexherrero/sherwood/internal/signals"
)

// Every symbol, price and amount here is made up.

// lastActor is the actor of the order's newest history row.
func (h *harness) lastActor(t *testing.T, id int64) orders.Actor {
	t.Helper()
	hist, err := orders.History(context.Background(), h.store.Reader(), id)
	if err != nil {
		t.Fatal(err)
	}
	return hist[len(hist)-1].Actor
}

func TestTheSwitchDeclinesWaitingBuysAndCancelsOpenBuysButKeepsTheStops(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"BBB": "4"})
	waiting := h.order(t, signals.KindBuy, "AAA", "5", p("10.10"), session, orders.AwaitingApproval)
	approved := h.approved(t, signals.KindAdd, "BBB", "1", p("20.20"))
	open := h.order(t, signals.KindBuy, "AAA", "3", p("10.10"), prev,
		orders.AwaitingApproval, orders.Approved, orders.Submitted, orders.Accepted)
	trim := h.order(t, signals.KindTrim, "BBB", "2", nil, session, orders.AwaitingApproval)
	stop := h.resting(t, "BBB", "4", "18.00")
	log, _ := loggingtest.New(t)

	if _, _, err := h.sw.Throw(ctx, halt.Operator, "test: made-up press"); err != nil {
		t.Fatal(err)
	}
	stopped, err := StopBuys(ctx, h.store, h.sw, h.clock, log, "test: switch thrown")
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped.Declined) != 1 || len(stopped.Cancelled) != 1 {
		t.Fatalf("StopBuys = %+v, want one declined and one cancelled", stopped)
	}
	got, err := h.ex.CancelOpen(ctx, CancelRequest{Why: "test: switch thrown"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Cancelled) != 1 || got.Cancelled[0].ID != open.ID || got.Unknown != 0 {
		t.Fatalf("CancelOpen = %+v, want the open buy cancelled", got)
	}

	for _, c := range []struct {
		id    int64
		state orders.State
		actor orders.Actor
	}{
		{waiting.ID, orders.Declined, orders.StopSwitch},
		{approved.ID, orders.Cancelled, orders.StopSwitch},
		{open.ID, orders.Cancelled, orders.StopSwitch},
		{trim.ID, orders.AwaitingApproval, orders.Operator}, // a sell is left to expire
		{stop.ID, orders.Accepted, orders.Operator},         // the stop stays
	} {
		if o := h.get(t, c.id); o.State != c.state || h.lastActor(t, c.id) != c.actor {
			t.Errorf("order %d (%s) is %s by %s, want %s by %s", c.id, o.Kind, o.State, h.lastActor(t, c.id), c.state, c.actor)
		}
	}
	if calls := h.fake.Calls(); len(calls) != 1 || calls[0].Method != brokertest.Cancel || calls[0].Ref != open.Key {
		t.Fatalf("broker calls = %+v, want one cancel of the open buy", calls)
	}

	// The second, separate confirmation cancels the resting stop.
	got, err = h.ex.CancelOpen(ctx, CancelRequest{Stops: true, Why: "test: stops confirmed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Cancelled) != 1 || got.Cancelled[0].ID != stop.ID {
		t.Fatalf("CancelOpen with stops = %+v, want the stop cancelled", got)
	}
	if h.fake.Count(brokertest.Cancel) != 2 {
		t.Errorf("%d cancels, want 2", h.fake.Count(brokertest.Cancel))
	}

	// Running both again changes nothing and calls nothing.
	if stopped, err = StopBuys(ctx, h.store, h.sw, h.clock, log, "test: again"); err != nil ||
		len(stopped.Declined)+len(stopped.Cancelled) != 0 {
		t.Fatalf("StopBuys again = %+v, %v", stopped, err)
	}
	if got, err = h.ex.CancelOpen(ctx, CancelRequest{Why: "test: again"}); err != nil || len(got.Cancelled) != 0 {
		t.Fatalf("CancelOpen again = %+v, %v", got, err)
	}
	if h.fake.Count(brokertest.Cancel) != 2 {
		t.Errorf("%d cancels after the repeat, want still 2", h.fake.Count(brokertest.Cancel))
	}
}

func TestAFailedCancelOnTheSwitchIsUnknownHaltsAndIsNeverRetried(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, passAll{})
	h.hold(t, nil)
	first := h.order(t, signals.KindBuy, "AAA", "3", p("10.10"), prev,
		orders.AwaitingApproval, orders.Approved, orders.Submitted, orders.Accepted)
	second := h.order(t, signals.KindBuy, "BBB", "1", p("20.20"), prev,
		orders.AwaitingApproval, orders.Approved, orders.Submitted, orders.Accepted)
	h.fake.Fail(brokertest.Cancel, errors.New("made-up connection reset"))

	got, err := h.ex.CancelOpen(ctx, CancelRequest{Why: "test: switch thrown"})
	if !errors.Is(err, ErrHalted) {
		t.Fatalf("CancelOpen error = %v, want ErrHalted", err)
	}
	if got.Unknown != 1 || len(got.Cancelled) != 1 || got.Cancelled[0].ID != second.ID {
		t.Fatalf("CancelOpen = %+v, want the first unknown and the second cancelled", got)
	}
	if o := h.get(t, first.ID); o.State != orders.Unknown {
		t.Errorf("first buy is %s, want unknown", o.State)
	}
	if h.fake.Count(brokertest.Cancel) != 2 || h.fake.Count(brokertest.Place) != 0 {
		t.Errorf("%d cancels and %d places, want one cancel per order and nothing placed",
			h.fake.Count(brokertest.Cancel), h.fake.Count(brokertest.Place))
	}
	if st := h.halted(t); !st.Has(halt.UnknownOrder) {
		t.Errorf("halts = %+v, want unknown_order", st.Active)
	}
}

func TestNoStopIsPlacedWhileTheStopsAreCancelled(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	h.suggest(t, "AAA", "9.00")
	approvedStop := h.approved(t, signals.KindStopSell, "AAA", "10", p("9.00"))
	if _, _, err := h.sw.Throw(ctx, halt.Operator, "test: made-up press"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.sw.Throw(ctx, halt.StopsCancelled, "test: made-up confirmation"); err != nil {
		t.Fatal(err)
	}
	got, err := h.ex.CancelOpen(ctx, CancelRequest{Stops: true, Why: "test: stops confirmed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.SetAside) != 1 || got.SetAside[0].ID != approvedStop.ID {
		t.Fatalf("CancelOpen = %+v, want the approved stop set aside", got)
	}
	h.send(t)
	if n := h.fake.Count(brokertest.Place); n != 0 {
		t.Errorf("%d orders placed while the stops are cancelled, want none", n)
	}
	if len(h.openStops(t)) != 0 {
		t.Errorf("stops rest: %+v", h.openStops(t))
	}
}

func TestATighteningFromThePhoneIsEnforcedAtSend(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, risk.Standard{})
	h.hold(t, nil)
	if _, err := limits.Tighten(ctx, h.store, h.strategy, h.clock.Now(), limits.OrderValue, "50.00"); err != nil {
		t.Fatal(err)
	}
	over := h.approved(t, signals.KindBuy, "AAA", "5", p("10.10"))  // 50.50: within the file's 100.00, over 50.00
	under := h.approved(t, signals.KindBuy, "BBB", "2", p("20.20")) // 40.40
	h.send(t)
	if o := h.get(t, over.ID); o.State != orders.Blocked || !strings.Contains(o.Verdict, "Refused: "+risk.CheckOrderValue) {
		t.Errorf("the 50.50 buy is %s with verdict %q, want blocked on order value", o.State, o.Verdict)
	}
	if o := h.get(t, under.ID); o.State != orders.Accepted {
		t.Errorf("the 40.40 buy is %s, want accepted", o.State)
	}
}
