package web

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/execution"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/proposal"
	"github.com/alexherrero/sherwood/internal/risk"
	"github.com/alexherrero/sherwood/internal/signals"
)

// Every symbol, price, strategy and address here is made up. The fixture's
// clock starts at 10:00 ET on Mon 16 Nov 2026, so an order for Tue 17 Nov can
// still be approved until 09:35 ET that morning.

// fakeChecker is a risk check that passes, or refuses when refuse is set,
// and remembers what it was asked.
type fakeChecker struct {
	mu     sync.Mutex
	refuse bool
	orders []risk.Order
	snaps  []risk.Snapshot
}

func (c *fakeChecker) Check(o risk.Order, s risk.Snapshot) risk.Verdict {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.orders = append(c.orders, o)
	c.snaps = append(c.snaps, s)
	return risk.Verdict{Results: []risk.Result{
		{Check: risk.CheckOrderValue, Passed: true, Detail: "The order is worth less than the most one order may be."},
		{Check: risk.CheckCash, Passed: !c.refuse, Detail: "Cash covers it, made up for the test."},
	}}
}

func (c *fakeChecker) setRefuse(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refuse = v
}

func (c *fakeChecker) last(t *testing.T) (risk.Order, risk.Snapshot) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.snaps) == 0 {
		t.Fatal("the risk check was never run")
	}
	return c.orders[len(c.orders)-1], c.snaps[len(c.snaps)-1]
}

func (c *fakeChecker) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.snaps)
}

const session = "2026-11-17"

func date(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation(clock.DateLayout, s, clock.Eastern)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// propose makes an order for the session and moves it as the proposal does:
// to awaiting_approval for a buy or ordinary sell, or approved for a stop.
// Then it makes any further moves given.
func (f *fixture) propose(kind signals.Kind, symbol, qty, price string, then ...orders.State) orders.Order {
	f.t.Helper()
	ctx := context.Background()
	n := orders.New{Strategy: "steady", Symbol: symbol, Date: date(f.t, session), Kind: kind,
		Quantity: money.MustParseQuantity(qty), Reason: "Made-up rule: " + symbol + " crossed its made-up line",
		StrategyHash: strings.Repeat("a", 64)}
	if price != "" {
		p := money.MustParsePrice(price)
		n.Price = &p
	}
	now := f.clock.Now()
	var o orders.Order
	err := orders.Update(ctx, f.store, func(tx *sql.Tx) error {
		var err error
		if o, _, err = orders.Create(ctx, tx, n, orders.Proposer, now); err != nil {
			return err
		}
		to := orders.AwaitingApproval
		if kind == signals.KindStopSell {
			to = orders.Approved
		}
		if o, err = orders.Move(ctx, tx, o.ID, orders.Change{To: to, Actor: orders.Risk, At: now,
			Reason: "The risk check passed.", Verdict: "Passed: Order value. Stored at proposal.\nPassed: Cash only. Stored cash line."}); err != nil {
			return err
		}
		for _, st := range then {
			if o, err = orders.Move(ctx, tx, o.ID, orders.Change{To: st, Actor: orders.Execution, At: now,
				Reason: "Moved for the test."}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return o
}

func (f *fixture) order(id int64) orders.Order {
	f.t.Helper()
	o, err := orders.Get(context.Background(), f.store.Reader(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return o
}

func (f *fixture) lastEvent(id int64) orders.Event {
	f.t.Helper()
	h, err := orders.History(context.Background(), f.store.Reader(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return h[len(h)-1]
}

// act posts an approval action for an order.
func (f *fixture) act(path string, id int64, headers map[string][]string) *httptest.ResponseRecorder {
	f.t.Helper()
	c, token := f.session()
	return f.postSwitch(path, token, c, headers, orderField, strconv.FormatInt(id, 10))
}

// wantApprovals checks for a 303 back to Approvals with the given result.
func wantApprovals(t *testing.T, w *httptest.ResponseRecorder, result string) {
	t.Helper()
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/approvals?result="+result {
		t.Fatalf("status %d, Location %q; want 303 to /approvals?result=%s\n%s",
			w.Code, w.Header().Get("Location"), result, w.Body)
	}
}

func (f *fixture) setNow(t *testing.T, eastern string) {
	t.Helper()
	at, err := time.ParseInLocation("2006-01-02 15:04", eastern, clock.Eastern)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Set(at)
}

func TestApprovalsShowsEachWaitingOrderWithBothVerdicts(t *testing.T) {
	f := newFixture(t)
	buy := f.propose(signals.KindBuy, "AAA", "10", "123.45")
	f.propose(signals.KindTrim, "BBB", "4", "")
	f.propose(signals.KindStopSell, "CCC", "7", "50.00") // approved at once: no card

	body := f.page("/approvals")
	wantIn(t, body,
		"<h1>Approvals</h1>", "2 waiting",
		`id="order-`+strconv.FormatInt(buy.ID, 10)+`">AAA</h2>`, "BBB",
		"Day limit $123.45", "$1,234.50 at the limit", "At the open",
		"Made-up rule: AAA crossed its made-up line",
		"Tue 17 Nov 2026", "Approve by Tue 17 Nov 2026, 9:35 am ET.",
		"Approve before 9:35 am ET on the order's trading day.",
		"expires at that evening's after-close",
		// The stored verdict, and the check run now.
		"Order value. Stored at proposal.", "Cash only. Stored cash line.",
		"Cash only. Cash covers it, made up for the test.",
		`action="/approvals/approve"`, `action="/approvals/decline"`,
		`name="order" value="`+strconv.FormatInt(buy.ID, 10)+`"`, `name="csrf_token"`)
	wantNotIn(t, body, "CCC", "Not built yet.", "The risk check refuses it now.")

	o, snap := f.checker.last(t)
	if !snap.Preview || !o.Session.Equal(date(t, session)) || o.Kind != risk.Sell || o.Symbol != "BBB" {
		t.Errorf("the card's check was asked %+v with preview %v", o, snap.Preview)
	}
	if !snap.SwitchOff || !snap.BrokerHealthy {
		t.Errorf("snapshot switch off %v, broker healthy %v", snap.SwitchOff, snap.BrokerHealthy)
	}
}

func TestApprovalsWithNothingWaiting(t *testing.T) {
	f := newFixture(t)
	wantIn(t, f.page("/approvals"), "Nothing is waiting for your approval.")
	wantNotIn(t, f.page("/"), "waiting for your approval")
}

func TestTheCardSaysWhenTheCheckNowRefuses(t *testing.T) {
	f := newFixture(t)
	f.propose(signals.KindBuy, "AAA", "10", "12.00")
	f.checker.setRefuse(true)
	wantIn(t, f.page("/approvals"), "The risk check refuses it now.", "Approving it would block it")
}

func TestApproveRechecksAndApproves(t *testing.T) {
	f := newFixture(t)
	o := f.propose(signals.KindBuy, "AAA", "10", "12.00")
	before := f.checker.calls()
	wantApprovals(t, f.act(approvePath, o.ID, fromAddr(phoneAddr)), "approved")
	if f.checker.calls() != before+1 {
		t.Error("approving did not run the risk check again")
	}
	ro, snap := f.checker.last(t)
	if !snap.Preview || ro.Kind != risk.Buy || !ro.Price.Equal(money.MustParsePrice("12.00")) {
		t.Errorf("approval checked %+v, preview %v", ro, snap.Preview)
	}
	got := f.order(o.ID)
	e := f.lastEvent(o.ID)
	if got.State != orders.Approved || e.Actor != orders.Operator || e.From != orders.AwaitingApproval {
		t.Fatalf("after approve: state %s, last event %+v", got.State, e)
	}
	if !strings.Contains(e.Reason, phoneAddr) || !strings.Contains(e.Verdict, "Passed: Cash only.") {
		t.Errorf("the approval's history row = %+v", e)
	}
	wantIn(t, f.page("/approvals?result=approved"), "Approved.", "Nothing is waiting for your approval.")

	// Approving it again changes nothing and halts nothing.
	wantApprovals(t, f.act(approvePath, o.ID, fromAddr(phoneAddr)), "gone")
	if f.switchState().On() {
		t.Error("a second tap threw the stop switch")
	}
}

func TestApproveBlocksAnOrderTheCheckNowRefuses(t *testing.T) {
	f := newFixture(t)
	o := f.propose(signals.KindBuy, "AAA", "10", "12.00")
	f.checker.setRefuse(true)
	wantApprovals(t, f.act(approvePath, o.ID, fromAddr(phoneAddr)), "blocked")
	got, e := f.order(o.ID), f.lastEvent(o.ID)
	if got.State != orders.Blocked || e.Actor != orders.Risk {
		t.Fatalf("after a refused approval: state %s, last event %+v", got.State, e)
	}
	if !strings.Contains(e.Reason, "Cash only: Cash covers it") || !strings.Contains(got.Verdict, "Refused: Cash only.") {
		t.Errorf("the blocked order's reason %q, verdict %q", e.Reason, got.Verdict)
	}
}

func TestDeclineDeclines(t *testing.T) {
	f := newFixture(t)
	o := f.propose(signals.KindSell, "BBB", "4", "")
	wantApprovals(t, f.act(declinePath, o.ID, fromAddr(phoneIPv6)), "declined")
	got, e := f.order(o.ID), f.lastEvent(o.ID)
	if got.State != orders.Declined || e.Actor != orders.Operator || !strings.Contains(e.Reason, "You declined it") {
		t.Fatalf("after decline: state %s, last event %+v", got.State, e)
	}
	// A declined order cannot then be approved.
	wantApprovals(t, f.act(approvePath, o.ID, fromAddr(phoneAddr)), "gone")
	if f.order(o.ID).State != orders.Declined {
		t.Error("a declined order was approved")
	}
}

func TestApprovalActionsNeedAnApprovalDeviceAndAToken(t *testing.T) {
	f := newFixture(t)
	o := f.propose(signals.KindBuy, "AAA", "10", "12.00")
	for _, path := range []string{approvePath, declinePath} {
		if w := f.act(path, o.ID, fromAddr(laptopAddr)); w.Code != http.StatusForbidden {
			t.Errorf("%s from another device = %d, want 403", path, w.Code)
		}
		c, _ := f.session()
		if w := f.postSwitch(path, "", c, fromAddr(phoneAddr), orderField, strconv.FormatInt(o.ID, 10)); w.Code != http.StatusForbidden {
			t.Errorf("%s without a token = %d, want 403", path, w.Code)
		}
		if w := f.do(req{method: http.MethodGet, path: path}); w.Code == http.StatusOK || w.Code == http.StatusSeeOther {
			t.Errorf("GET %s = %d", path, w.Code)
		}
	}
	if got := f.order(o.ID); got.State != orders.AwaitingApproval {
		t.Errorf("a refused action moved the order to %s", got.State)
	}
}

func TestAnApprovalAfterExpiryIsRefused(t *testing.T) {
	f := newFixture(t)
	o := f.propose(signals.KindBuy, "AAA", "10", "12.00")

	// The session's after-close declines it, as the after-close job does.
	f.setNow(t, session+" 16:30")
	logger, _ := loggingtest.New(t)
	p, err := proposal.New(proposal.Config{Store: f.store, Clock: f.clock, Calendar: clock.NYSE(),
		Strategy: &config.Strategy{Limits: config.Limits{BuyLimitMargin: config.MustDecimal("0.005")}},
		Checker:  f.checker, Switch: f.stop, Notifier: notify.Nowhere, Logger: logger,
		BrokerHealthy: func(context.Context) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := p.Expire(context.Background(), date(t, session)); err != nil || n != 1 {
		t.Fatalf("Expire = %d, %v", n, err)
	}

	wantApprovals(t, f.act(approvePath, o.ID, fromAddr(phoneAddr)), "expired")
	got, e := f.order(o.ID), f.lastEvent(o.ID)
	if got.State != orders.Declined || e.Actor != orders.Expiry {
		t.Fatalf("an expired order was moved: state %s, last event %+v", got.State, e)
	}
	wantIn(t, f.page("/approvals?result=expired"), "That order has expired.", "Nothing is waiting")
	if f.switchState().On() {
		t.Error("approving an expired order threw the stop switch")
	}
	// Orders shows what happened.
	wantIn(t, f.page("/orders"), "Waiting for approval to Declined, by expiry",
		"It was not approved by the end of its trading day, 2026-11-17.")
}

func TestAnApprovalAfterTheSendIsRefused(t *testing.T) {
	f := newFixture(t)
	o := f.propose(signals.KindBuy, "AAA", "10", "12.00")
	f.setNow(t, session+" 09:35")

	body := f.page("/approvals")
	wantIn(t, body, "Too late to approve.", `action="/approvals/decline"`)
	wantNotIn(t, body, `action="/approvals/approve"`)

	wantApprovals(t, f.act(approvePath, o.ID, fromAddr(phoneAddr)), "late")
	if got := f.order(o.ID); got.State != orders.AwaitingApproval {
		t.Fatalf("a late approval moved the order to %s", got.State)
	}
	// It can still be declined.
	wantApprovals(t, f.act(declinePath, o.ID, fromAddr(phoneAddr)), "declined")
}

func TestAnApprovedOrderNeverSentExpires(t *testing.T) {
	f := newFixture(t)
	o := f.propose(signals.KindBuy, "AAA", "10", "12.00")
	wantApprovals(t, f.act(approvePath, o.ID, fromAddr(phoneAddr)), "approved")

	f.setNow(t, session+" 16:30")
	logger, _ := loggingtest.New(t)
	if _, err := execution.ExpireUnsent(context.Background(), f.store, f.stop, f.clock, logger, date(t, session)); err != nil {
		t.Fatal(err)
	}
	if got := f.order(o.ID); got.State != orders.Expired {
		t.Fatalf("state %s, want expired", got.State)
	}
	wantIn(t, f.page("/orders"), "Waiting for approval to Approved, by you", "Approved to Expired, by expiry")
}

func TestAnUnknownOrderIsNotChanged(t *testing.T) {
	f := newFixture(t)
	for _, id := range []string{"", "0", "-3", "abc", "999"} {
		c, token := f.session()
		wantApprovals(t, f.postSwitch(approvePath, token, c, fromAddr(phoneAddr), orderField, id), "gone")
	}
	if n := f.rows("order_history"); n != 0 {
		t.Errorf("%d history rows were written", n)
	}
}

func TestTheApprovalSnapshotReadsHoldingsCashAndTheSessionsOrders(t *testing.T) {
	f := newFixture(t)
	f.recordPrices("2026-11-16",
		priceCheck{symbol: "AAA", outcome: "agreed", close: "12.00"},
		priceCheck{symbol: "BBB", held: true, outcome: "agreed", close: "20.00"})
	f.recordHoldings("2026-11-16T14:00:00Z", "1000.00", holding(t, "BBB", "5", "18.00"))
	approved := f.propose(signals.KindAdd, "BBB", "2", "20.10")
	wantApprovals(t, f.act(approvePath, approved.ID, fromAddr(phoneAddr)), "approved")
	f.propose(signals.KindBuy, "DDD", "1", "5.00") // waiting: does not count
	o := f.propose(signals.KindBuy, "AAA", "10", "12.00")
	if _, _, err := f.stop.Throw(context.Background(), halt.Operator, "Pressed for the test."); err != nil {
		t.Fatal(err)
	}

	wantApprovals(t, f.act(approvePath, o.ID, fromAddr(phoneAddr)), "approved")
	_, snap := f.checker.last(t)
	if !snap.Held["BBB"].Equal(money.MustParseQuantity("5")) || !snap.Cash.Equal(money.MustParseMoney("1000.00")) {
		t.Errorf("held %v, cash %s", snap.Held, snap.Cash)
	}
	if want := money.MustParseMoney("1100.00"); !snap.Value.Equal(want) || !snap.PreviousCloseValue.Equal(want) {
		t.Errorf("value %s, previous %s; want %s", snap.Value, snap.PreviousCloseValue, want)
	}
	if !snap.LastCloses["AAA"].Equal(money.MustParsePrice("12.00")) {
		t.Errorf("last closes %v", snap.LastCloses)
	}
	if len(snap.DayOrders) != 1 || snap.DayOrders[0].Symbol != "BBB" || !snap.DayOrders[0].Open ||
		!snap.DayOrders[0].Value.Equal(money.MustParseMoney("40.20")) {
		t.Errorf("day orders %+v, want the approved BBB add only", snap.DayOrders)
	}
	if snap.SwitchOff {
		t.Error("the snapshot missed the stop switch")
	}
}

func TestAPortfolioWithAnUnpricedHoldingIsValuedAtZero(t *testing.T) {
	f := newFixture(t)
	f.recordHoldings("2026-11-16T14:00:00Z", "1000.00", holding(t, "BBB", "5", "18.00"))
	o := f.propose(signals.KindBuy, "AAA", "10", "12.00")
	wantApprovals(t, f.act(approvePath, o.ID, fromAddr(phoneAddr)), "approved")
	if _, snap := f.checker.last(t); !snap.Value.IsZero() || !snap.Cash.Equal(money.MustParseMoney("1000.00")) {
		t.Errorf("value %s, cash %s; want the value zero so the check fails closed", snap.Value, snap.Cash)
	}
}

func TestTodayLinksToWaitingApprovals(t *testing.T) {
	f := newFixture(t)
	f.propose(signals.KindBuy, "AAA", "10", "12.00")
	wantIn(t, f.page("/"), "1 order is waiting for your approval.", `<a href="/approvals">Open Approvals</a>`)
	f.propose(signals.KindTrim, "BBB", "4", "")
	wantIn(t, f.page("/"), "2 orders are waiting for your approval.")
}

func TestOrdersListsEachOrderWithItsHistoryAndFills(t *testing.T) {
	f := newFixture(t)
	wantIn(t, f.page("/orders"), "No orders yet.")

	o := f.propose(signals.KindBuy, "AAA", "1500", "12.35", orders.Approved, orders.Submitted, orders.Accepted)
	ctx := context.Background()
	err := orders.Update(ctx, f.store, func(tx *sql.Tx) error {
		_, _, err := orders.RecordFill(ctx, tx, o.ID, orders.NewFill{Date: date(t, session),
			Quantity: money.MustParseQuantity("1000"), Price: money.MustParsePrice("12.30"),
			Reason: "Made-up fill at the open.", Actor: orders.Broker, At: f.clock.Now()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	f.propose(signals.KindTrim, "BBB", "4", "")

	body := f.page("/orders")
	wantIn(t, body,
		"Tue 17 Nov 2026", "AAA", "BBB", "Partly filled", "Waiting for approval",
		"Day limit $12.35", "1500",
		"Made by the proposal: Proposed",
		"Proposed to Waiting for approval, by the risk check",
		"Accepted by the broker to Partly filled, by the broker",
		"1000 shares at $12.30", "$12,300.00", "Made-up fill at the open.",
		"Filled", "1000 of 1500 shares, $12,300.00",
		"Order value. Stored at proposal.")
	wantNotIn(t, body, "Not built yet.")
}
