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
	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/signals"
)

// Every symbol, price, amount and limit here is made up. The example file's
// daily loss limit is 50.00; the harness holds 10 AAA at a 10.00 close and
// 10000.00 cash, so the previous close's value is 10100.00.

// checkTime is mid-morning of the session, inside market hours.
var checkTime = sendTime.Add(70 * time.Minute) // 10:45

// sessionExpired is the helper's answer when the broker session needs a new
// login.
var sessionExpired = &broker.HelperError{Op: "account", Code: broker.CodeLoginNeeded, Message: "made-up: no usable session"}

func (h *harness) lossCheck(t *testing.T, value string) (Loss, error) {
	t.Helper()
	h.clock.Set(checkTime)
	h.fake.Acct.Value = money.MustParseMoney(value)
	return h.ex.CheckDailyLoss(context.Background(), session)
}

// pushes counts the trading-halted pushes sent.
func (h *harness) pushes() int {
	n := 0
	for _, a := range h.alerts.Sent() {
		if a == notify.TradingHalted() {
			n++
		}
	}
	return n
}

func TestALossUnderTheLimitDoesNotHalt(t *testing.T) {
	for _, value := range []string{"10100.00", "10200.00", "10050.01"} {
		h := newHarness(t, passAll{})
		h.hold(t, map[string]string{"AAA": "10"})
		l, err := h.lossCheck(t, value)
		if err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		if l.Reached || h.halted(t).On() {
			t.Errorf("%s: reached %v, halts %+v; want no halt", value, l.Reached, h.halted(t).Active)
		}
		if !l.Previous.Equal(money.MustParseMoney("10100.00")) || !l.Limit.Equal(money.MustParseMoney("50.00")) {
			t.Errorf("%s: measured %+v", value, l)
		}
		if h.fake.Count(brokertest.Account) != 1 || len(h.fake.Calls()) != 1 {
			t.Errorf("%s: calls %+v, want one account read", value, h.fake.Calls())
		}
	}
}

// A loss that reaches the limit halts trading with the figures as evidence,
// and a loss that grows over later checks keeps the one halt and push.
func TestALossPastTheLimitHaltsTradingOnce(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	l, err := h.lossCheck(t, "10050.00") // exactly the limit reaches it
	if err != nil {
		t.Fatal(err)
	}
	if !l.Reached || !l.Loss.Equal(money.MustParseMoney("50.00")) {
		t.Fatalf("measured %+v, want the limit reached", l)
	}
	st := h.halted(t)
	if len(st.Active) != 1 || st.Active[0].Cause != halt.DailyLoss {
		t.Fatalf("halts %+v, want one daily_loss halt", st.Active)
	}
	want := "The account is worth $10050.00 at the broker, down $50.00 from $10100.00 at the close of 2026-03-02, which reaches the $50.00 daily loss limit."
	if st.Active[0].Evidence != want {
		t.Errorf("evidence:\n got %q\nwant %q", st.Active[0].Evidence, want)
	}
	for _, value := range []string{"10000.00", "9900.00"} {
		h.clock.Advance(15 * time.Minute)
		if l, err := h.lossCheck(t, value); err != nil || !l.Reached {
			t.Fatalf("%s: %+v, %v", value, l, err)
		}
	}
	if n := len(h.halted(t).Active); n != 1 || h.pushes() != 1 {
		t.Errorf("%d halts and %d pushes, want one each", n, h.pushes())
	}
	if h.fake.Count(brokertest.Place) != 0 || h.fake.Count(brokertest.Cancel) != 0 {
		t.Errorf("the check placed or cancelled: %+v", h.fake.Calls())
	}
}

// The limit is the one in force: a limit tightened from the phone app is
// the one the check compares against.
func TestTheLossCheckUsesTheTightenedLimit(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	if _, err := limits.Tighten(context.Background(), h.store, h.strategy, prevShut, limits.DailyLoss, "20.00"); err != nil {
		t.Fatal(err)
	}
	l, err := h.lossCheck(t, "10075.00")
	if err != nil {
		t.Fatal(err)
	}
	if !l.Reached || !l.Limit.Equal(money.MustParseMoney("20.00")) || !h.halted(t).Has(halt.DailyLoss) {
		t.Fatalf("measured %+v, halts %+v; want the tightened 20.00 limit reached", l, h.halted(t).Active)
	}
}

func TestALossThatCannotBeMeasuredFailsWithoutAHalt(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"CCC": "10"}) // CCC has no close
	if _, err := h.lossCheck(t, "1.00"); !errors.Is(err, ErrLossUnknown) {
		t.Fatalf("err %v, want ErrLossUnknown", err)
	}
	if h.halted(t).On() {
		t.Errorf("halts %+v, want none", h.halted(t).Active)
	}
}

// An expired session fails every check until you log in again: it halts
// trading as a broker error, saying a login is needed, once, with one push,
// across both checks and many slots.
func TestAnExpiredSessionHaltsOnce(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	for range 3 {
		h.fake.Fail(brokertest.Account, sessionExpired)
		h.fake.Fail(brokertest.OpenOrders, sessionExpired)
	}
	for i := range 3 {
		h.clock.Set(checkTime.Add(time.Duration(i) * 15 * time.Minute))
		if _, err := h.ex.CheckDailyLoss(context.Background(), session); !errors.Is(err, ErrHalted) || !broker.LoginNeeded(err) {
			t.Fatalf("slot %d: loss check = %v, want ErrHalted wrapping login_needed", i, err)
		}
		if _, err := h.ex.CheckOpenOrders(context.Background()); !errors.Is(err, ErrHalted) {
			t.Fatalf("slot %d: open orders check = %v, want ErrHalted", i, err)
		}
	}
	st := h.halted(t)
	if len(st.Active) != 1 || st.Active[0].Cause != halt.BrokerError || !strings.HasPrefix(st.Active[0].Evidence, SessionExpired) {
		t.Fatalf("halts %+v, want one broker_error halt saying the session expired", st.Active)
	}
	if h.pushes() != 1 {
		t.Errorf("%d pushes, want 1", h.pushes())
	}
	// Once you have logged in and reset, the next expiry halts again.
	if _, err := h.sw.Reset(context.Background(), halt.ResetRequest{Through: st.Newest(), Reason: "logged in again",
		Device: "100.64.0.10"}); err != nil {
		t.Fatal(err)
	}
	h.fake.Fail(brokertest.Account, sessionExpired)
	if _, err := h.ex.CheckDailyLoss(context.Background(), session); !errors.Is(err, ErrHalted) {
		t.Fatal(err)
	}
	if n := len(h.halted(t).Active); n != 1 || h.pushes() != 2 {
		t.Errorf("after a reset: %d active halts, %d pushes; want 1 and 2", n, h.pushes())
	}
}

// Any other failed call halts as a broker error without the login words.
func TestAFailedAccountReadHaltsAsABrokerError(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	h.fake.Fail(brokertest.Account, errors.New("made-up time-out"))
	if _, err := h.lossCheck(t, "10100.00"); !errors.Is(err, ErrHalted) {
		t.Fatalf("err %v, want ErrHalted", err)
	}
	st := h.halted(t)
	if !st.Has(halt.BrokerError) || strings.Contains(st.Active[0].Evidence, SessionExpired) ||
		!strings.Contains(st.Active[0].Evidence, "made-up time-out") {
		t.Errorf("halts %+v", st.Active)
	}
}

func (h *harness) openCheck(t *testing.T) *Reconciled {
	t.Helper()
	h.clock.Set(checkTime)
	res, err := h.ex.CheckOpenOrders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestOpenOrdersThatAgreeDoNotHalt(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	stop := h.resting(t, "AAA", "10", "9.00")
	// Holdings and cash differ, but the 15-minute check reads only the
	// open orders; the full reconciliation compares the rest.
	h.agree(map[string]string{"AAA": "3"}, stop)
	res := h.openCheck(t)
	if len(res.Differences) != 0 || h.halted(t).On() {
		t.Fatalf("differences %q, halts %+v; want none", res.Differences, h.halted(t).Active)
	}
	if calls := h.fake.Calls(); len(calls) != 1 || calls[0].Method != brokertest.OpenOrders {
		t.Errorf("calls %+v, want one open-orders listing", calls)
	}
}

// An order that filled since the last check is no longer listed as open: it
// is looked up once and its fill recorded, and nothing halts.
func TestAnOrderThatFilledSinceTheLastCheckIsSettled(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	stop := h.resting(t, "AAA", "10", "9.00")
	buy := h.order(t, signals.KindBuy, "BBB", "2", p("20.20"), session,
		orders.AwaitingApproval, orders.Approved, orders.Submitted, orders.Accepted)
	h.agree(nil, stop)
	h.fake.Answer(brokertest.Lookup, broker.Report{Ref: buy.Key, Status: broker.StatusFilled, Filled: q("2"),
		FillPrice: p("20.10")}, nil)
	res := h.openCheck(t)
	if len(res.Differences) != 0 || h.halted(t).On() {
		t.Fatalf("differences %q, halts %+v; want none", res.Differences, h.halted(t).Active)
	}
	if o := h.get(t, buy.ID); o.State != orders.Filled || h.lastActor(t, o.ID) != orders.Reconciliation {
		t.Errorf("the buy is %s by %s, want filled by reconciliation", o.State, h.lastActor(t, o.ID))
	}
	if h.fake.Count(brokertest.Lookup) != 1 || h.fake.Count(brokertest.Place) != 0 {
		t.Errorf("calls %+v, want one lookup and no send", h.fake.Calls())
	}
	// A lookup that still finds the order open (it was listed a moment
	// late) is no difference either.
	h2 := newHarness(t, passAll{})
	h2.hold(t, map[string]string{"AAA": "10"})
	stop2 := h2.resting(t, "AAA", "10", "9.00")
	h2.agree(nil)
	if res := h2.openCheck(t); len(res.Differences) != 0 || h2.halted(t).On() || h2.get(t, stop2.ID).State != orders.Accepted {
		t.Fatalf("differences %q, halts %+v", res.Differences, h2.halted(t).Active)
	}
}

func TestAnOpenOrderDifferenceHaltsTrading(t *testing.T) {
	cases := map[string]struct {
		setup func(t *testing.T, h *harness)
		want  string
	}{
		"an open order only the broker has": {
			setup: func(_ *testing.T, h *harness) {
				h.fake.Open = []broker.Report{{Ref: "made-up-reference", Status: broker.StatusAccepted}}
			},
			want: "The broker has an open order (reference made-up-reference) that sherwood does not have open.",
		},
		"an open order's fills": {
			setup: func(t *testing.T, h *harness) {
				stop := h.resting(t, "AAA", "10", "9.00")
				h.fake.Open = []broker.Report{{Ref: stop.Key, Status: broker.StatusPartiallyFilled, Filled: q("4")}}
			},
			want: "the broker reports 4 shares filled, sherwood has recorded 0.",
		},
		"an order gone from the broker that its lookup cannot explain": {
			setup: func(t *testing.T, h *harness) {
				stop := h.resting(t, "AAA", "10", "9.00")
				h.fake.Answer(brokertest.Lookup, broker.Report{Ref: stop.Key, Status: broker.StatusFilled, Filled: q("10")}, nil)
			},
			want: "without a fill price sherwood can record.",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, passAll{})
			h.hold(t, map[string]string{"AAA": "10"})
			h.agree(map[string]string{"AAA": "10"})
			c.setup(t, h)
			res := h.openCheck(t)
			if !strings.Contains(strings.Join(res.Differences, " "), c.want) {
				t.Fatalf("differences %q, want one containing %q", res.Differences, c.want)
			}
			st := h.halted(t)
			if len(st.Active) != 1 || st.Active[0].Cause != halt.Reconciliation || !strings.Contains(st.Active[0].Evidence, c.want) {
				t.Fatalf("halts %+v, want one reconciliation halt naming %q", st.Active, c.want)
			}
			if h.fake.Count(brokertest.Place) != 0 || h.fake.Count(brokertest.Cancel) != 0 {
				t.Errorf("the check placed or cancelled: %+v", h.fake.Calls())
			}
		})
	}
}

func TestAFailedOpenOrderListingHaltsAsABrokerError(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, nil)
	h.fake.Fail(brokertest.OpenOrders, errors.New("made-up connection reset"))
	h.clock.Set(checkTime)
	if _, err := h.ex.CheckOpenOrders(context.Background()); !errors.Is(err, ErrHalted) {
		t.Fatalf("err %v, want ErrHalted", err)
	}
	if !h.halted(t).Has(halt.BrokerError) || h.fake.Count(brokertest.Lookup) != 0 {
		t.Errorf("halts %+v, calls %+v", h.halted(t).Active, h.fake.Calls())
	}
}
