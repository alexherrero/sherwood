package execution

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/broker/brokertest"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/signals"
)

// Every symbol, price and amount here is made up.

// agree sets the fake broker to agree with the harness's records: the given
// holdings, 10000.00 cash, and the given open orders.
func (h *harness) agree(held map[string]string, open ...orders.Order) {
	h.fake.Positions = nil
	for sym, n := range held {
		h.fake.Positions = append(h.fake.Positions, broker.Position{Symbol: sym, Quantity: q(n)})
	}
	h.fake.Acct = broker.Account{Cash: money.MustParseMoney("10000.00"), Value: money.MustParseMoney("10000.00")}
	h.fake.Open = nil
	for _, o := range open {
		h.fake.Open = append(h.fake.Open, broker.Report{Ref: o.Key, Status: broker.StatusAccepted})
	}
}

func (h *harness) reconcile(t *testing.T) *Reconciled {
	t.Helper()
	h.clock.Set(sendTime.Add(-35 * time.Minute)) // 09:00
	res, err := h.ex.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRecordsThatAgreeDoNotHalt(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	stop := h.resting(t, "AAA", "10", "9.00")
	h.agree(map[string]string{"AAA": "10"}, stop)
	res := h.reconcile(t)
	if len(res.Differences) != 0 || h.halted(t).On() {
		t.Fatalf("differences %q, halts %+v; want none", res.Differences, h.halted(t).Active)
	}
	for _, m := range []brokertest.Method{brokertest.Holdings, brokertest.Account, brokertest.OpenOrders} {
		if h.fake.Count(m) != 1 {
			t.Errorf("%d %s calls, want 1", h.fake.Count(m), m)
		}
	}
}

func TestAReconciliationDifferenceHaltsTrading(t *testing.T) {
	cases := map[string]struct {
		setup func(t *testing.T, h *harness)
		want  string
	}{
		"holdings": {
			setup: func(_ *testing.T, h *harness) { h.agree(map[string]string{"AAA": "9"}) },
			want:  "AAA: sherwood holds 10 shares, the broker 9.",
		},
		"a stock only the broker holds": {
			setup: func(_ *testing.T, h *harness) { h.agree(map[string]string{"AAA": "10", "BBB": "1"}) },
			want:  "BBB: sherwood holds 0 shares, the broker 1.",
		},
		"cash": {
			setup: func(_ *testing.T, h *harness) {
				h.agree(map[string]string{"AAA": "10"})
				h.fake.Acct.Cash = money.MustParseMoney("9990.00")
			},
			want: "Cash: sherwood has recorded $10000.00, the broker $9990.00.",
		},
		"an open order only the broker has": {
			setup: func(_ *testing.T, h *harness) {
				h.agree(map[string]string{"AAA": "10"})
				h.fake.Open = []broker.Report{{Ref: "made-up-reference", Status: broker.StatusAccepted}}
			},
			want: "The broker has an open order (reference made-up-reference) that sherwood does not have open.",
		},
		"an open order the broker does not list": {
			setup: func(t *testing.T, h *harness) {
				h.resting(t, "AAA", "10", "9.00")
				h.agree(map[string]string{"AAA": "10"})
			},
			want: "but the broker does not list it as open.",
		},
		"an open order's fills": {
			setup: func(t *testing.T, h *harness) {
				stop := h.resting(t, "AAA", "10", "9.00")
				h.agree(map[string]string{"AAA": "10"})
				h.fake.Open = []broker.Report{{Ref: stop.Key, Status: broker.StatusPartiallyFilled, Filled: q("4")}}
			},
			want: "the broker reports 4 shares filled, sherwood has recorded 0.",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, passAll{})
			h.hold(t, map[string]string{"AAA": "10"})
			c.setup(t, h)
			res := h.reconcile(t)
			if len(res.Differences) == 0 || !strings.Contains(strings.Join(res.Differences, " "), c.want) {
				t.Fatalf("differences %q, want one containing %q", res.Differences, c.want)
			}
			st := h.halted(t)
			if !st.Has(halt.Reconciliation) || !strings.Contains(st.Active[len(st.Active)-1].Evidence, c.want) {
				t.Fatalf("halts = %+v, want a reconciliation halt naming %q", st.Active, c.want)
			}
			// Sherwood's own records are left as they are.
			if h.fake.Count(brokertest.Place) != 0 || h.fake.Count(brokertest.Cancel) != 0 {
				t.Errorf("reconciliation placed or cancelled an order: %+v", h.fake.Calls())
			}
		})
	}
}

func TestAnUnknownOrderIsSettledByLookupWithoutAResend(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, nil)
	unknown := h.order(t, signals.KindBuy, "AAA", "5", p("10.10"), session,
		orders.AwaitingApproval, orders.Approved, orders.Submitted, orders.Unknown)
	stuck := h.order(t, signals.KindBuy, "BBB", "2", p("20.20"), session,
		orders.AwaitingApproval, orders.Approved, orders.Submitted)
	h.fake.Answer(brokertest.Lookup, broker.Report{Ref: unknown.Key, Status: broker.StatusAccepted}, nil)
	h.fake.Answer(brokertest.Lookup, broker.Report{Ref: stuck.Key, Status: broker.StatusRejected}, nil)
	h.agree(nil, unknown)

	res := h.reconcile(t)
	if len(res.Settled) != 2 || len(res.Differences) != 0 {
		t.Fatalf("settled %d, differences %q; want 2 settled and none", len(res.Settled), res.Differences)
	}
	if o := h.get(t, unknown.ID); o.State != orders.Accepted || h.lastActor(t, o.ID) != orders.Reconciliation {
		t.Errorf("the unknown order is %s by %s, want accepted by reconciliation", o.State, h.lastActor(t, o.ID))
	}
	if o := h.get(t, stuck.ID); o.State != orders.Rejected {
		t.Errorf("the stuck order is %s, want rejected", o.State)
	}
	if h.fake.Count(brokertest.Lookup) != 2 || h.fake.Count(brokertest.Place) != 0 {
		t.Errorf("%d lookups and %d places, want one lookup each and no resend",
			h.fake.Count(brokertest.Lookup), h.fake.Count(brokertest.Place))
	}
}

func TestAFillFoundByLookupIsRecordedAtTheBrokersPrice(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, nil)
	unknown := h.order(t, signals.KindBuy, "AAA", "5", p("10.10"), session,
		orders.AwaitingApproval, orders.Approved, orders.Submitted, orders.Unknown)
	h.fake.Answer(brokertest.Lookup, broker.Report{Ref: unknown.Key, Status: broker.StatusFilled, Filled: q("5"),
		FillPrice: p("10.05")}, nil)
	// The broker holds the shares sherwood has not recorded in its holdings
	// yet, so the comparison still finds that difference for review.
	h.agree(map[string]string{"AAA": "5"})
	res := h.reconcile(t)
	o := h.get(t, unknown.ID)
	if o.State != orders.Filled || h.lastActor(t, o.ID) != orders.Reconciliation {
		t.Fatalf("order is %s by %s, want filled by reconciliation", o.State, h.lastActor(t, o.ID))
	}
	fs, err := orders.Fills(context.Background(), h.store.Reader(), o.ID)
	if err != nil || len(fs) != 1 || !fs[0].Price.Equal(*p("10.05")) || !fs[0].Quantity.Equal(q("5")) {
		t.Fatalf("fills = %+v, %v; want 5 at 10.05", fs, err)
	}
	if len(res.Differences) != 1 || !strings.Contains(res.Differences[0], "AAA: sherwood holds 0 shares, the broker 5.") {
		t.Errorf("differences %q, want the holdings difference", res.Differences)
	}
	if h.fake.Count(brokertest.Place) != 0 {
		t.Error("the order was sent again")
	}
}

func TestAFillWithoutAPriceIsADifferenceNotAGuess(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, nil)
	unknown := h.order(t, signals.KindBuy, "AAA", "5", p("10.10"), session,
		orders.AwaitingApproval, orders.Approved, orders.Submitted, orders.Unknown)
	h.fake.Answer(brokertest.Lookup, broker.Report{Ref: unknown.Key, Status: broker.StatusFilled, Filled: q("5")}, nil)
	h.agree(nil, unknown)
	res := h.reconcile(t)
	if o := h.get(t, unknown.ID); o.State != orders.Unknown {
		t.Errorf("order is %s, want still unknown", o.State)
	}
	if len(res.Differences) != 1 || !strings.Contains(res.Differences[0], "without a fill price") {
		t.Errorf("differences %q", res.Differences)
	}
	if !h.halted(t).Has(halt.Reconciliation) {
		t.Error("no reconciliation halt")
	}
}

func TestAFailedLookupHaltsAndIsNeverRetried(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, nil)
	stuck := h.order(t, signals.KindBuy, "AAA", "5", p("10.10"), session,
		orders.AwaitingApproval, orders.Approved, orders.Submitted)
	h.fake.Fail(brokertest.Lookup, errors.New("made-up connection reset"))
	h.clock.Set(sendTime)
	_, err := h.ex.Reconcile(context.Background())
	if !errors.Is(err, ErrHalted) {
		t.Fatalf("Reconcile = %v, want ErrHalted", err)
	}
	if o := h.get(t, stuck.ID); o.State != orders.Unknown {
		t.Errorf("order is %s, want unknown", o.State)
	}
	if !h.halted(t).Has(halt.UnknownOrder) {
		t.Errorf("halts = %+v, want unknown_order", h.halted(t).Active)
	}
	if h.fake.Count(brokertest.Lookup) != 1 || h.fake.Count(brokertest.Place) != 0 || h.fake.Count(brokertest.Holdings) != 0 {
		t.Errorf("calls = %+v, want one lookup and nothing else", h.fake.Calls())
	}
}

func TestAFailedListingHalts(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, nil)
	h.fake.Fail(brokertest.Holdings, errors.New("made-up time-out"))
	h.clock.Set(sendTime)
	if _, err := h.ex.Reconcile(context.Background()); !errors.Is(err, ErrHalted) {
		t.Fatalf("Reconcile = %v, want ErrHalted", err)
	}
	if !h.halted(t).Has(halt.BrokerError) {
		t.Errorf("halts = %+v, want broker_error", h.halted(t).Active)
	}
}
