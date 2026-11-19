package execution

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/broker/brokertest"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/prices/pricestest"
	"github.com/alexherrero/sherwood/internal/risk"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every symbol, price, amount and strategy here is made up, and nothing here
// is financial advice.

var (
	// The session is Tuesday 3 March 2026; the previous one Monday 2 March.
	session  = time.Date(2026, 3, 3, 0, 0, 0, 0, clock.Eastern)
	prev     = time.Date(2026, 3, 2, 0, 0, 0, 0, clock.Eastern)
	prevShut = time.Date(2026, 3, 2, 18, 0, 0, 0, clock.Eastern)
	sendTime = time.Date(2026, 3, 3, 9, 35, 0, 0, clock.Eastern)
)

// testStrategy is the example file's limits with two made-up stocks in one
// made-up strategy.
func testStrategy(t *testing.T) *config.Strategy {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "strategy.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	head, _, ok := strings.Cut(string(b), "\nwatchlist:\n")
	if !ok {
		t.Fatal("the example file has no watchlist block")
	}
	s, err := config.ParseStrategy([]byte(head + `
watchlist:
  - AAA
  - BBB
sectors:
  AAA: sector-a
  BBB: sector-b
strategies:
  - name: test-trend
    portfolio_share: 0.50
    stocks: [AAA, BBB]
    rules:
      - type: breakout
        settings:
          days: 2
          exit_days: 2
      - type: trailing_stop
        settings:
          distance: 0.10
      - type: size_fixed_amount
        settings:
          amount: "50.00"
`))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// passAll is a risk check that passes every order, for tests about sending.
type passAll struct{}

func (passAll) Check(risk.Order, risk.Snapshot) risk.Verdict {
	return risk.Verdict{Results: []risk.Result{{Check: "test", Passed: true, Detail: "made up"}}}
}

type harness struct {
	store    *store.Store
	clock    *clock.Fake
	fake     *brokertest.Fake
	alerts   *notifytest.Fake
	sw       *halt.Switch
	ex       *Executor
	strategy *config.Strategy
}

func newHarness(t *testing.T, checker risk.Checker) *harness {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sherwood.db"), prevShut)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	log, _ := loggingtest.New(t)
	h := &harness{store: s, clock: clock.NewFake(prevShut), fake: &brokertest.Fake{}, alerts: &notifytest.Fake{},
		strategy: testStrategy(t)}
	h.sw, err = halt.New(halt.Config{Store: s, Clock: h.clock, Notifier: h.alerts, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	h.ex, err = New(Config{Store: s, Clock: h.clock, Calendar: clock.NYSE(), Adapter: h.fake, Checker: checker,
		Switch: h.sw, Strategy: h.strategy, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	h.storeCloses(t, map[string]string{"AAA": "10.00", "BBB": "20.00"})
	h.fake.Acct = broker.Account{Cash: money.MustParseMoney("10000.00"), Value: money.MustParseMoney("10100.00")}
	return h
}

// storeCloses checks and stores flat bars for the previous session.
func (h *harness) storeCloses(t *testing.T, closes map[string]string) {
	t.Helper()
	primary, check := pricestest.New("primary"), pricestest.New("cross")
	var syms []string
	for sym, c := range closes {
		b := pricestest.Bar(sym, "2026-03-02", c, c, c, c, 1000)
		primary.Add(b)
		check.Add(b)
		syms = append(syms, sym)
	}
	log, _ := loggingtest.New(t)
	f := &prices.Fetcher{Primary: primary, Check: check, Clock: h.clock, Calendar: clock.NYSE(), Store: h.store,
		Log: log, HistoryDays: 5}
	day, err := f.FetchDay(context.Background(), prev, prices.Stocks{Watched: syms},
		h.strategy.Limits.PriceCrossCheckTolerance)
	if err != nil || !day.Usable {
		t.Fatalf("FetchDay = %+v, %v", day, err)
	}
}

func q(s string) money.Quantity { return money.MustParseQuantity(s) }

func p(s string) *money.Price {
	v := money.MustParsePrice(s)
	return &v
}

// hold records the holdings (each at an average cost of 10.00) and 10000.00
// cash at the previous close.
func (h *harness) hold(t *testing.T, held map[string]string) {
	t.Helper()
	var hs []holdings.Holding
	for sym, n := range held {
		hs = append(hs, holdings.Holding{Symbol: sym, Quantity: q(n), AverageCost: money.MustParseMoney("10.00"),
			Opened: time.Date(2026, 2, 2, 0, 0, 0, 0, clock.Eastern)})
	}
	if _, _, err := holdings.Record(context.Background(), h.store, holdings.Snapshot{Source: holdings.SourceImport,
		AsOf: prevShut, Holdings: hs, Cash: money.MustParseMoney("10000.00")}); err != nil {
		t.Fatal(err)
	}
}

// order makes an order of kind k for date and moves it along path.
func (h *harness) order(t *testing.T, k signals.Kind, sym, shares string, price *money.Price, date time.Time,
	path ...orders.State) orders.Order {
	t.Helper()
	var o orders.Order
	err := orders.Update(context.Background(), h.store, func(tx *sql.Tx) error {
		var err error
		o, _, err = orders.Create(context.Background(), tx, orders.New{Strategy: "test-trend", Symbol: sym,
			Date: date, Kind: k, Quantity: q(shares), Price: price, Reason: "test: made-up reason",
			StrategyHash: h.strategy.Hash}, orders.Proposer, h.clock.Now())
		for _, to := range path {
			if err != nil {
				return err
			}
			o, err = orders.Move(context.Background(), tx, o.ID, orders.Change{To: to, Actor: orders.Operator,
				Reason: "test", At: h.clock.Now()})
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// approved is an approved order for the session.
func (h *harness) approved(t *testing.T, k signals.Kind, sym, shares string, price *money.Price) orders.Order {
	t.Helper()
	if k == signals.KindStopSell {
		return h.order(t, k, sym, shares, price, session, orders.Approved)
	}
	return h.order(t, k, sym, shares, price, session, orders.AwaitingApproval, orders.Approved)
}

// resting is a stop sent at an earlier session and resting at the broker.
func (h *harness) resting(t *testing.T, sym, shares, stop string) orders.Order {
	t.Helper()
	return h.order(t, signals.KindStopSell, sym, shares, p(stop), prev,
		orders.Approved, orders.Submitted, orders.Accepted)
}

// suggest records the stop rules' stop for a holding at the previous close.
func (h *harness) suggest(t *testing.T, sym, stop string) {
	t.Helper()
	if _, err := h.store.Writer().Exec(`INSERT INTO holding_suggestions
		(strategy, symbol, trading_date, suggestion, reason, stop, strategy_hash, created_at)
		VALUES (?, ?, ?, 'hold', '', ?, ?, ?)`,
		"test-trend", sym, "2026-03-02", stop, h.strategy.Hash, store.FormatTime(prevShut)); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) send(t *testing.T) *Result {
	t.Helper()
	h.clock.Set(sendTime)
	res, err := h.ex.Send(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (h *harness) get(t *testing.T, id int64) orders.Order {
	t.Helper()
	o, err := orders.Get(context.Background(), h.store.Reader(), id)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func (h *harness) halted(t *testing.T) halt.State {
	t.Helper()
	st, err := halt.Active(context.Background(), h.store.Reader())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// openStops lists the stop orders resting after the run.
func (h *harness) openStops(t *testing.T) []orders.Order {
	t.Helper()
	os, err := orders.InState(context.Background(), h.store.Reader(), orders.Accepted)
	if err != nil {
		t.Fatal(err)
	}
	var out []orders.Order
	for _, o := range os {
		if o.Type == orders.Stop {
			out = append(out, o)
		}
	}
	return out
}

func TestNewRefusesANilChecker(t *testing.T) {
	_, err := New(Config{})
	if !errors.Is(err, ErrNoChecker) {
		t.Fatalf("New without a checker = %v, want ErrNoChecker", err)
	}
	var none risk.Checker
	if _, err := New(Config{Checker: none, Adapter: &brokertest.Fake{}}); !errors.Is(err, ErrNoChecker) {
		t.Fatalf("New with a nil checker = %v, want ErrNoChecker", err)
	}
	if _, err := New(Config{Checker: passAll{}}); err == nil || errors.Is(err, ErrNoChecker) {
		t.Fatalf("New with a checker but nothing else = %v, want another refusal", err)
	}
}

func TestAFailedSendMovesTheOrderToUnknownHaltsAndCallsOnce(t *testing.T) {
	for name, failure := range map[string]error{
		"an error":   errors.New("made-up connection reset"),
		"a time-out": context.DeadlineExceeded,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, passAll{})
			h.hold(t, nil)
			first := h.approved(t, signals.KindBuy, "AAA", "2", p("10.10"))
			second := h.approved(t, signals.KindBuy, "BBB", "1", p("20.20"))
			h.fake.Fail(brokertest.Place, failure)

			h.clock.Set(sendTime)
			_, err := h.ex.Send(context.Background(), session)
			if !errors.Is(err, ErrHalted) {
				t.Fatalf("Send = %v, want ErrHalted", err)
			}
			if got := h.get(t, first.ID).State; got != orders.Unknown {
				t.Errorf("the failed order is %s, want unknown", got)
			}
			if got := h.get(t, second.ID).State; got != orders.Approved {
				t.Errorf("the next order is %s; nothing more is sent after a failure", got)
			}
			st := h.halted(t)
			if !st.On() || st.Active[0].Cause != halt.UnknownOrder || !strings.Contains(st.Active[0].Evidence, "AAA") {
				t.Errorf("halts %+v, want one for the unknown order", st.Active)
			}
			if len(h.alerts.Sent()) != 1 {
				t.Errorf("%d alerts, want one", len(h.alerts.Sent()))
			}
			if n := h.fake.Count(brokertest.Place); n != 1 {
				t.Errorf("%d place calls, want exactly one", n)
			}

			// Running again sends nothing: the switch is on, and the unknown
			// order is never resent.
			if _, err := h.ex.Send(context.Background(), session); err != nil {
				t.Fatal(err)
			}
			if n := h.fake.Count(brokertest.Place); n != 1 {
				t.Errorf("%d place calls after a second run, want still one", n)
			}
			hist, err := orders.History(context.Background(), h.store.Reader(), first.ID)
			if err != nil {
				t.Fatal(err)
			}
			var trail []string
			for _, e := range hist {
				trail = append(trail, string(e.To))
			}
			if got := strings.Join(trail, " "); got != "proposed awaiting_approval approved submitted unknown" {
				t.Errorf("history %q", got)
			}
		})
	}
}

func TestARefusalOrAStrangeAnswerHalts(t *testing.T) {
	for name, c := range map[string]struct {
		report broker.Report
		state  orders.State
		cause  halt.Cause
	}{
		"a refusal":         {broker.Report{Status: broker.StatusRejected, Detail: "made-up refusal"}, orders.Rejected, halt.BrokerError},
		"a filled answer":   {broker.Report{Status: broker.StatusFilled}, orders.Unknown, halt.UnknownOrder},
		"another reference": {broker.Report{Ref: "someone-else", Status: broker.StatusAccepted}, orders.Unknown, halt.UnknownOrder},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, passAll{})
			h.hold(t, nil)
			o := h.approved(t, signals.KindBuy, "AAA", "2", p("10.10"))
			if c.report.Ref == "" {
				c.report.Ref = o.Key
			}
			h.fake.Answer(brokertest.Place, c.report, nil)
			h.clock.Set(sendTime)
			if _, err := h.ex.Send(context.Background(), session); !errors.Is(err, ErrHalted) {
				t.Fatalf("Send = %v, want ErrHalted", err)
			}
			if got := h.get(t, o.ID).State; got != c.state {
				t.Errorf("order is %s, want %s", got, c.state)
			}
			if st := h.halted(t); !st.On() || st.Active[0].Cause != c.cause {
				t.Errorf("halts %+v, want %s", st.Active, c.cause)
			}
		})
	}
}

func TestASellAndABuyOfOneStockOnOneDayIsRefused(t *testing.T) {
	h := newHarness(t, risk.Standard{})
	h.hold(t, map[string]string{"AAA": "5"})
	trim := h.approved(t, signals.KindTrim, "AAA", "2", nil)
	add := h.approved(t, signals.KindAdd, "AAA", "2", p("10.10"))
	buy := h.approved(t, signals.KindBuy, "BBB", "1", p("20.20"))

	res := h.send(t)
	if got := h.get(t, trim.ID).State; got != orders.Accepted {
		t.Errorf("trim is %s, want accepted", got)
	}
	if got := h.get(t, buy.ID).State; got != orders.Accepted {
		t.Errorf("the other stock's buy is %s, want accepted", got)
	}
	blocked := h.get(t, add.ID)
	if blocked.State != orders.Blocked || len(res.Blocked) != 1 {
		t.Fatalf("add is %s, want blocked", blocked.State)
	}
	if !strings.Contains(blocked.Verdict, "Refused: "+risk.CheckSameDay) {
		t.Errorf("verdict %q does not refuse on the same-day check", blocked.Verdict)
	}
	for _, c := range h.fake.Calls() {
		if c.Ref == add.Key {
			t.Error("the refused add reached the broker")
		}
	}
	if h.halted(t).On() {
		t.Error("a risk refusal halted trading")
	}
}

func TestEveryOrderPassesTheRiskCheckBeforeItIsSent(t *testing.T) {
	h := newHarness(t, risk.Standard{})
	h.hold(t, nil)
	big := h.approved(t, signals.KindBuy, "AAA", "50", p("10.10")) // 505.00, over the 100.00 order limit
	h.send(t)
	o := h.get(t, big.ID)
	if o.State != orders.Blocked || !strings.Contains(o.Verdict, "Refused: "+risk.CheckOrderValue) {
		t.Errorf("order is %s with verdict %q, want blocked on order value", o.State, o.Verdict)
	}
	if n := len(h.fake.Calls()); n != 1 { // the account read only
		t.Errorf("%d broker calls, want only the account read", n)
	}
}

// coverage walks the broker calls in order and fails if, at any point, the
// stops and sells resting at the broker for sym cover more shares than held.
func coverage(t *testing.T, h *harness, sym string, held money.Quantity, rest []orders.Order) {
	t.Helper()
	open := map[string]money.Quantity{}
	for _, o := range rest {
		open[o.Key] = o.Quantity
	}
	for i, c := range h.fake.Calls() {
		switch c.Method {
		case brokertest.Cancel:
			delete(open, c.Ref)
		case brokertest.Place:
			if c.Order.Symbol == sym && c.Order.Side == broker.Sell {
				open[c.Ref] = c.Order.Quantity
			}
		}
		var total money.Quantity
		for _, n := range open {
			total = total.Add(n)
		}
		if total.Cmp(held) > 0 {
			t.Fatalf("after call %d (%s), %s shares of %s are covered but %s are held", i, c.Method, total, sym, held)
		}
	}
}

func TestATrimResizesTheRestingStopSoNoMoreSharesAreCoveredThanHeld(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	old := h.resting(t, "AAA", "10", "9.00")
	trim := h.approved(t, signals.KindTrim, "AAA", "4", nil)

	h.send(t)
	coverage(t, h, "AAA", q("10"), []orders.Order{old})
	var methods []string
	for _, c := range h.fake.Calls()[1:] {
		methods = append(methods, string(c.Method)+" "+c.Order.Quantity.String())
	}
	if got := strings.Join(methods, ", "); got != "cancel 0, place 6, place 4" {
		t.Errorf("broker calls %q, want the stop cancelled, then 6 shares stopped, then 4 sold", got)
	}
	if got := h.get(t, old.ID).State; got != orders.Cancelled {
		t.Errorf("old stop is %s, want cancelled", got)
	}
	stops := h.openStops(t)
	if len(stops) != 1 || stops[0].Quantity.String() != "6" || stops[0].Price.String() != "9.00" ||
		!stops[0].Date.Equal(session) {
		t.Fatalf("resting stops %+v, want one for 6 shares at 9.00 for the session", stops)
	}
	if got := h.get(t, trim.ID).State; got != orders.Accepted {
		t.Errorf("trim is %s, want accepted", got)
	}
}

func TestASellOfTheWholeHoldingCallsTheStopOff(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	old := h.resting(t, "AAA", "10", "9.00")
	h.suggest(t, "AAA", "9.50")
	exit := h.approved(t, signals.KindSell, "AAA", "10", nil)

	res := h.send(t)
	coverage(t, h, "AAA", q("10"), []orders.Order{old})
	if got := h.get(t, old.ID).State; got != orders.Cancelled {
		t.Errorf("old stop is %s, want cancelled", got)
	}
	if len(h.openStops(t)) != 0 {
		t.Error("a stop still rests on a holding being sold whole")
	}
	if got := h.get(t, exit.ID).State; got != orders.Accepted || len(res.Sent) != 1 {
		t.Errorf("exit is %s with %d sent, want accepted and only it sent", got, len(res.Sent))
	}
}

func TestAFailedCancelHaltsAndTheTrimIsNotSent(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	old := h.resting(t, "AAA", "10", "9.00")
	trim := h.approved(t, signals.KindTrim, "AAA", "4", nil)
	h.fake.Fail(brokertest.Cancel, errors.New("made-up time-out"))

	h.clock.Set(sendTime)
	if _, err := h.ex.Send(context.Background(), session); !errors.Is(err, ErrHalted) {
		t.Fatalf("Send = %v, want ErrHalted", err)
	}
	if got := h.get(t, old.ID).State; got != orders.Unknown {
		t.Errorf("the stop is %s, want unknown", got)
	}
	if got := h.get(t, trim.ID).State; got != orders.Approved {
		t.Errorf("the trim is %s; it must not go while the stop's state is unknown", got)
	}
	if h.fake.Count(brokertest.Cancel) != 1 || h.fake.Count(brokertest.Place) != 0 {
		t.Errorf("calls %+v, want one cancel and no place", h.fake.Calls())
	}
	if !h.halted(t).On() {
		t.Error("trading did not halt")
	}
}

func TestStopsRiseAfterTheCloseAndNeverFall(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10", "BBB": "4"})
	aaa := h.resting(t, "AAA", "10", "9.00")
	bbb := h.resting(t, "BBB", "4", "18.00")
	h.suggest(t, "AAA", "9.40") // a trailing stop raised after the close
	h.suggest(t, "BBB", "17.00")

	h.send(t)
	if got := h.get(t, aaa.ID).State; got != orders.Cancelled {
		t.Errorf("AAA's old stop is %s, want cancelled", got)
	}
	if got := h.get(t, bbb.ID).State; got != orders.Accepted {
		t.Errorf("BBB's stop is %s; a lower suggestion must not move it", got)
	}
	stops := h.openStops(t)
	if len(stops) != 2 {
		t.Fatalf("%d resting stops, want two", len(stops))
	}
	for _, s := range stops {
		if s.Symbol == "AAA" && (s.Price.String() != "9.40" || s.Quantity.String() != "10") {
			t.Errorf("AAA's stop is %s shares at %s, want 10 at 9.40", s.Quantity, s.Price)
		}
	}
	if n := h.fake.Count(brokertest.Cancel); n != 1 {
		t.Errorf("%d cancels, want one", n)
	}
}

func TestANewHoldingGetsItsStopAndAnAddResizesIt(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10", "BBB": "6"})
	h.suggest(t, "AAA", "9.00")              // bought yesterday: no stop rests yet
	old := h.resting(t, "BBB", "4", "18.00") // 2 more shares were added yesterday

	h.send(t)
	got := map[string]string{}
	for _, s := range h.openStops(t) {
		got[s.Symbol] = s.Quantity.String() + " at " + s.Price.String()
	}
	if got["AAA"] != "10 at 9.00" || got["BBB"] != "6 at 18.00" {
		t.Errorf("resting stops %v, want AAA 10 at 9.00 and BBB 6 at 18.00", got)
	}
	if h.get(t, old.ID).State != orders.Cancelled {
		t.Error("BBB's old stop was not replaced")
	}
}

func TestAProposedProtectiveSellBecomesTheOneStop(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	old := h.resting(t, "AAA", "10", "9.00")
	proposed := h.approved(t, signals.KindStopSell, "AAA", "10", p("9.60"))

	h.send(t)
	if h.get(t, old.ID).State != orders.Cancelled {
		t.Error("the old stop was not called off")
	}
	if got := h.get(t, proposed.ID).State; got != orders.Accepted {
		t.Errorf("the proposed stop is %s, want accepted", got)
	}
	if n := len(h.openStops(t)); n != 1 {
		t.Errorf("%d stops rest, want one", n)
	}
}

func TestWhileHaltedOnlyProtectiveStopsAreSent(t *testing.T) {
	h := newHarness(t, passAll{})
	h.hold(t, map[string]string{"AAA": "10"})
	h.suggest(t, "AAA", "9.00")
	buy := h.approved(t, signals.KindBuy, "BBB", "1", p("20.20"))
	trim := h.approved(t, signals.KindTrim, "AAA", "4", nil)
	if _, _, err := h.sw.Throw(context.Background(), halt.Operator, "made-up evidence"); err != nil {
		t.Fatal(err)
	}

	res := h.send(t)
	for _, o := range []orders.Order{buy, trim} {
		if got := h.get(t, o.ID).State; got != orders.Approved {
			t.Errorf("%s is %s while halted, want approved", o.Kind, got)
		}
	}
	if len(res.Held) != 2 {
		t.Errorf("%d held back, want two", len(res.Held))
	}
	stops := h.openStops(t)
	if len(stops) != 1 || stops[0].Quantity.String() != "10" {
		t.Errorf("stops %+v, want one for all 10 shares", stops)
	}
}

func TestSendRefusesToRunWhileTheMarketIsShut(t *testing.T) {
	h := newHarness(t, passAll{})
	h.approved(t, signals.KindBuy, "AAA", "1", p("10.10"))
	for _, at := range []time.Time{
		time.Date(2026, 3, 3, 9, 0, 0, 0, clock.Eastern),
		time.Date(2026, 3, 3, 16, 0, 0, 0, clock.Eastern),
		time.Date(2026, 3, 4, 9, 35, 0, 0, clock.Eastern),
	} {
		h.clock.Set(at)
		if _, err := h.ex.Send(context.Background(), session); !errors.Is(err, ErrNotOpen) {
			t.Errorf("at %s: %v, want ErrNotOpen", at, err)
		}
	}
	if n := len(h.fake.Calls()); n != 0 {
		t.Errorf("%d broker calls while shut", n)
	}
}

func TestAFailedAccountReadHaltsAndSendsNothing(t *testing.T) {
	h := newHarness(t, passAll{})
	h.approved(t, signals.KindBuy, "AAA", "1", p("10.10"))
	h.fake.Fail(brokertest.Account, errors.New("made-up failure"))
	h.clock.Set(sendTime)
	if _, err := h.ex.Send(context.Background(), session); !errors.Is(err, ErrHalted) {
		t.Fatalf("Send = %v, want ErrHalted", err)
	}
	if st := h.halted(t); !st.On() || st.Active[0].Cause != halt.BrokerError {
		t.Errorf("halts %+v, want a broker error", st.Active)
	}
	if n := h.fake.Count(brokertest.Place); n != 0 {
		t.Errorf("%d orders placed", n)
	}
}

func TestApprovedOrdersNeverSentExpireWithTheirSession(t *testing.T) {
	h := newHarness(t, passAll{})
	buy := h.approved(t, signals.KindBuy, "AAA", "1", p("10.10"))
	stop := h.approved(t, signals.KindStopSell, "BBB", "1", p("18.00"))
	next := h.order(t, signals.KindBuy, "BBB", "1", p("20.20"), session.AddDate(0, 0, 1),
		orders.AwaitingApproval, orders.Approved)
	log, _ := loggingtest.New(t)
	h.clock.Set(time.Date(2026, 3, 3, 16, 30, 0, 0, clock.Eastern))
	for range 2 { // a second run expires nothing more
		if _, err := ExpireUnsent(context.Background(), h.store, h.sw, h.clock, log, session); err != nil {
			t.Fatal(err)
		}
	}
	want := map[int64]orders.State{buy.ID: orders.Expired, stop.ID: orders.Expired, next.ID: orders.Approved}
	for id, st := range want {
		if got := h.get(t, id).State; got != st {
			t.Errorf("order %d is %s, want %s", id, got, st)
		}
	}
	if len(h.fake.Calls()) != 0 {
		t.Error("expiry called the broker")
	}
}
