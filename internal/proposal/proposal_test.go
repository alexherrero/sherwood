package proposal

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/prices/pricestest"
	"github.com/alexherrero/sherwood/internal/risk"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every symbol, price, holding and amount below is made up, and nothing here
// is financial advice. The signals are for Friday 9 January 2026, so the
// orders are for the next session, Monday 12 January.

const (
	signalDate  = "2026-01-09"
	sessionDate = "2026-01-12"
)

var history = []string{"2026-01-05", "2026-01-06", "2026-01-07", "2026-01-08", signalDate}

// testStrategy is the example file's limits with a made-up watchlist, sectors
// and one strategy: a two-day breakout, a 10% fixed stop and a fixed-amount
// sizing rule.
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
sectors:
  AAA: sector-a
  BBB: sector-b
  DDD: sector-a
  EEE: sector-c
strategies:
  - name: test-breakout
    portfolio_share: 0.50
    stocks: [AAA, BBB, DDD, EEE]
    rules:
      - type: breakout
        settings:
          days: 2
          exit_days: 2
      - type: fixed_stop
        settings:
          distance: 0.10
      - type: size_fixed_amount
        settings:
          amount: "100.00"
`))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func date(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation(clock.DateLayout, s, clock.Eastern)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// at is hh:mm US Eastern on the date s.
func at(t *testing.T, s string, hour, minute int) time.Time {
	return date(t, s).Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

type harness struct {
	store    *store.Store
	clock    *clock.Fake
	alerts   *notifytest.Fake
	halts    *halt.Switch
	logs     *loggingtest.Output
	strategy *config.Strategy
	p        *Proposer
	day      Day
}

// newHarness records the signal date's prices and signals:
//
//   - AAA, on the watchlist and not held, closes at 11.00 above its two-day
//     high: a buy.
//   - BBB, 3 held at 100.00, closes at 89.00 below its 90.00 stop: a
//     protective sell.
//   - EEE, 4 held at 10.00, closes at 9.50 below its two-day low but above
//     its 9.00 stop: an ordinary sell.
//   - DDD, 5 held at 10.00, does nothing.
//
// The cash is 1000.00, so the portfolio is worth 1355.00 at the close.
func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	now := at(t, signalDate, 16, 30)
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	log, out := loggingtest.New(t)
	h := &harness{store: s, clock: clock.NewFake(now), alerts: &notifytest.Fake{}, logs: out, strategy: testStrategy(t)}
	h.halts, err = halt.New(halt.Config{Store: s, Clock: h.clock, Notifier: h.alerts, Logger: log})
	if err != nil {
		t.Fatal(err)
	}

	primary, check := pricestest.New("primary"), pricestest.New("cross")
	closes := map[string][]string{
		"AAA": {"10.00", "10.00", "10.00", "10.00", "11.00"},
		"BBB": {"100.00", "100.00", "100.00", "100.00", "89.00"},
		"DDD": {"10.00", "10.00", "10.00", "10.00", "10.00"},
		"EEE": {"10.00", "10.00", "10.00", "10.00", "9.50"},
	}
	for sym, cs := range closes {
		for i, c := range cs {
			b := pricestest.Bar(sym, history[i], c, c, c, c, 1000)
			primary.Add(b)
			check.Add(b)
		}
	}
	f := &prices.Fetcher{Primary: primary, Check: check, Clock: h.clock, Calendar: clock.NYSE(), Store: s,
		Log: log, HistoryDays: 10}
	if _, err := f.FetchDay(ctx, date(t, signalDate), prices.Stocks{Held: []string{"BBB", "DDD", "EEE"},
		Watched: []string{"AAA", "BBB", "DDD", "EEE"}}, config.MustDecimal("0.02")); err != nil {
		t.Fatal(err)
	}

	held := []holdings.Holding{
		{Symbol: "BBB", Quantity: money.MustParseQuantity("3"), AverageCost: money.MustParseMoney("100.00"), Opened: date(t, "2026-01-05")},
		{Symbol: "DDD", Quantity: money.MustParseQuantity("5"), AverageCost: money.MustParseMoney("10.00"), Opened: date(t, "2026-01-05")},
		{Symbol: "EEE", Quantity: money.MustParseQuantity("4"), AverageCost: money.MustParseMoney("10.00"), Opened: date(t, "2026-01-05")},
	}
	set, cash, err := holdings.Record(ctx, s, holdings.Snapshot{Source: holdings.SourceImport,
		AsOf: at(t, signalDate, 10, 0), Holdings: held, Cash: money.MustParseMoney("1000.00")})
	if err != nil {
		t.Fatal(err)
	}
	runner := &signals.Runner{Store: s, Clock: h.clock, Log: log}
	if _, err := runner.Run(ctx, date(t, signalDate), h.strategy, set.Holdings); err != nil {
		t.Fatal(err)
	}
	h.day = Day{Date: date(t, signalDate), Holdings: set, Cash: &cash}

	h.p, err = New(Config{Store: s, Clock: h.clock, Calendar: clock.NYSE(), Strategy: h.strategy,
		Checker: risk.Standard{}, Switch: h.halts, Notifier: h.alerts,
		BrokerHealthy: func(context.Context) bool { return true }, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) propose(t *testing.T, d Day) Result {
	t.Helper()
	res, err := h.p.Propose(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// orders lists every order, oldest first.
func (h *harness) orders(t *testing.T) []orders.Order {
	t.Helper()
	list, err := orders.InState(context.Background(), h.store.Reader(), orders.States...)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// orderText is "date symbol kind type state quantity price" per order.
func orderText(list []orders.Order) []string {
	var out []string
	for _, o := range list {
		p := "-"
		if o.Price != nil {
			p = o.Price.String()
		}
		out = append(out, strings.Join([]string{o.Date.Format(clock.DateLayout), o.Symbol, string(o.Kind),
			string(o.Type), string(o.State), o.Quantity.String(), p}, " "))
	}
	return out
}

func (h *harness) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := h.store.Reader().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRunningTwiceMakesOneOrderPerKey(t *testing.T) {
	h := newHarness(t)
	res := h.propose(t, h.day)

	// Protective sells first, then exits, then buys.
	want := []string{
		sessionDate + " BBB stop_sell stop approved 3 90.00",
		sessionDate + " EEE sell sell_at_open awaiting_approval 4 -",
		sessionDate + " AAA buy day_limit_buy awaiting_approval 9 11.11",
	}
	list := h.orders(t)
	if got := orderText(list); !reflect.DeepEqual(got, want) {
		t.Fatalf("orders:\n got %q\nwant %q", got, want)
	}
	if res.Waiting != 2 || res.BuysWaiting != 1 || res.Protective != 1 || res.Blocked != 0 || res.Existing != 0 {
		t.Errorf("result %+v", res)
	}
	if got := res.Session.Format(clock.DateLayout); got != sessionDate {
		t.Errorf("session %s, want %s", got, sessionDate)
	}
	for _, o := range list {
		if want := signals.Key("test-breakout", o.Symbol, o.Side, o.Kind, date(t, sessionDate)); o.Key != want {
			t.Errorf("%s key %s, want %s", o.Symbol, o.Key, want)
		}
		if !strings.Contains(o.Verdict, "Passed: ") || strings.Contains(o.Verdict, "Refused: ") {
			t.Errorf("%s verdict %q", o.Symbol, o.Verdict)
		}
		hist, err := orders.History(context.Background(), h.store.Reader(), o.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(hist) != 2 || hist[0].Actor != orders.Proposer || hist[1].Actor != orders.Risk || hist[1].Verdict != o.Verdict {
			t.Errorf("%s history %+v", o.Symbol, hist)
		}
	}
	// The reasons carry the rule, the sizing, the limit and the stops.
	for sym, parts := range map[string][]string{
		"AAA": {"breakout: ", "100.00 at 11.11 a share buys 9 shares", "limit 11.11 is the close of 11.00 plus 1", "initial stop "},
		"BBB": {"fixed_stop: ", "stop 90.00 for all 3 shares"},
		"EEE": {"breakout: ", "sells all 4 shares"},
	} {
		for _, o := range list {
			if o.Symbol != sym {
				continue
			}
			for _, p := range parts {
				if !strings.Contains(o.Reason, p) {
					t.Errorf("%s reason %q lacks %q", sym, o.Reason, p)
				}
			}
		}
	}

	hist := h.count(t, "order_history")
	again := h.propose(t, h.day)
	if got := orderText(h.orders(t)); !reflect.DeepEqual(got, want) {
		t.Errorf("after a second run:\n got %q\nwant %q", got, want)
	}
	if again.Existing != 3 || again.Waiting+again.Protective+again.Blocked != 0 {
		t.Errorf("second result %+v", again)
	}
	if n := h.count(t, "order_history"); n != hist {
		t.Errorf("%d history rows after the second run, want %d", n, hist)
	}
	if got := h.alerts.Sent(); !reflect.DeepEqual(got, []notify.Alert{notify.BuysWaiting(1)}) {
		t.Errorf("alerts %v, want one buys-waiting alert for 1", got)
	}
}

func TestTheNotificationCarriesOnlyTheCount(t *testing.T) {
	h := newHarness(t)
	h.propose(t, h.day)
	sent := h.alerts.Sent()
	if len(sent) != 1 {
		t.Fatalf("%d alerts, want 1", len(sent))
	}
	b, err := sent[0].Payload()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"text":"1 buy waiting","path":"/approvals"}`; got != want {
		t.Errorf("payload %s, want %s", got, want)
	}
	for _, leak := range []string{"AAA", "11.11", "100.00", "breakout"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("payload %s names %q", b, leak)
		}
	}
}

func TestAFailedNotificationDoesNotFailTheProposal(t *testing.T) {
	h := newHarness(t)
	h.alerts.Err = notify.ErrNoRecipients
	h.propose(t, h.day)
	if n := len(h.orders(t)); n != 3 {
		t.Errorf("%d orders, want 3", n)
	}
	if !strings.Contains(h.logs.String(), "waiting buys not notified") {
		t.Errorf("log:\n%s", h.logs.String())
	}
}

func TestACatchUpDateProposesNoBuy(t *testing.T) {
	t.Run("catch-up run", func(t *testing.T) {
		h := newHarness(t)
		h.clock.Set(at(t, "2026-01-10", 3, 0)) // Saturday night: the session has not opened
		d := h.day
		d.CatchUp = true
		res := h.propose(t, d)
		want := []string{
			sessionDate + " BBB stop_sell stop approved 3 90.00",
			sessionDate + " EEE sell sell_at_open awaiting_approval 4 -",
		}
		if got := orderText(h.orders(t)); !reflect.DeepEqual(got, want) {
			t.Errorf("orders:\n got %q\nwant %q", got, want)
		}
		if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "AAA buy: this run is catching up") {
			t.Errorf("skipped %q", res.Skipped)
		}
		if len(h.alerts.Sent()) != 0 {
			t.Errorf("alerts %v, want none", h.alerts.Sent())
		}
	})
	t.Run("session already open", func(t *testing.T) {
		h := newHarness(t)
		h.clock.Set(at(t, sessionDate, 9, 30))
		res := h.propose(t, h.day)
		want := []string{sessionDate + " BBB stop_sell stop approved 3 90.00"}
		if got := orderText(h.orders(t)); !reflect.DeepEqual(got, want) {
			t.Errorf("orders:\n got %q\nwant %q", got, want)
		}
		if len(res.Skipped) != 2 {
			t.Errorf("skipped %q", res.Skipped)
		}
		for _, s := range res.Skipped {
			if !strings.Contains(s, "the session of "+sessionDate+" has already opened") {
				t.Errorf("skipped %q", s)
			}
		}
	})
}

func TestABlockedOrderStoresItsReason(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// Rules and proposals still run while the switch is on; the risk check
	// refuses every buy and ordinary sell, and protective sells still go.
	if _, _, err := h.halts.Throw(ctx, halt.Operator, "Made-up evidence: the stop switch was pressed."); err != nil {
		t.Fatal(err)
	}
	res := h.propose(t, h.day)
	want := []string{
		sessionDate + " BBB stop_sell stop approved 3 90.00",
		sessionDate + " EEE sell sell_at_open blocked 4 -",
		sessionDate + " AAA buy day_limit_buy blocked 9 11.11",
	}
	list := h.orders(t)
	if got := orderText(list); !reflect.DeepEqual(got, want) {
		t.Fatalf("orders:\n got %q\nwant %q", got, want)
	}
	if res.Blocked != 2 || res.Waiting != 0 || res.BuysWaiting != 0 {
		t.Errorf("result %+v", res)
	}
	for _, o := range list[1:] {
		hist, err := orders.History(ctx, h.store.Reader(), o.ID)
		if err != nil {
			t.Fatal(err)
		}
		last := hist[len(hist)-1]
		if last.To != orders.Blocked || last.Actor != orders.Risk || !strings.Contains(last.Reason, risk.CheckSwitch) {
			t.Errorf("%s blocked with %+v", o.Symbol, last)
		}
		if !strings.Contains(o.Verdict, "Refused: "+risk.CheckSwitch) || last.Verdict != o.Verdict {
			t.Errorf("%s verdict %q", o.Symbol, o.Verdict)
		}
	}
	if sent := h.alerts.Sent(); len(sent) != 1 || sent[0] != notify.TradingHalted() {
		t.Errorf("alerts %v, want only the halt's", sent)
	}
}

func TestExpiryDeclinesUnapprovedOrders(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.propose(t, h.day)

	// The signal date's own expiry leaves the next session's orders alone.
	if n, err := h.p.Expire(ctx, date(t, signalDate)); err != nil || n != 0 {
		t.Fatalf("expire through %s: %d, %v", signalDate, n, err)
	}
	h.clock.Set(at(t, sessionDate, 16, 30))
	n, err := h.p.Expire(ctx, date(t, sessionDate))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("declined %d, want 2", n)
	}
	want := []string{
		sessionDate + " BBB stop_sell stop approved 3 90.00",
		sessionDate + " EEE sell sell_at_open declined 4 -",
		sessionDate + " AAA buy day_limit_buy declined 9 11.11",
	}
	list := h.orders(t)
	if got := orderText(list); !reflect.DeepEqual(got, want) {
		t.Errorf("orders:\n got %q\nwant %q", got, want)
	}
	hist, err := orders.History(ctx, h.store.Reader(), list[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	last := hist[len(hist)-1]
	if last.From != orders.AwaitingApproval || last.Actor != orders.Expiry ||
		last.Reason != "It was not approved by the end of its trading day, "+sessionDate+"." {
		t.Errorf("expiry %+v", last)
	}
	if n, err := h.p.Expire(ctx, date(t, sessionDate)); err != nil || n != 0 {
		t.Errorf("second expiry: %d, %v", n, err)
	}
}

func TestNewRefusesAMissingPart(t *testing.T) {
	h := newHarness(t)
	full := h.p.cfg
	for name, mod := range map[string]func(*Config){
		"store":    func(c *Config) { c.Store = nil },
		"clock":    func(c *Config) { c.Clock = nil },
		"calendar": func(c *Config) { c.Calendar = nil },
		"strategy": func(c *Config) { c.Strategy = nil },
		"checker":  func(c *Config) { c.Checker = nil },
		"switch":   func(c *Config) { c.Switch = nil },
		"notifier": func(c *Config) { c.Notifier = nil },
		"broker":   func(c *Config) { c.BrokerHealthy = nil },
		"logger":   func(c *Config) { c.Logger = nil },
	} {
		c := full
		mod(&c)
		if _, err := New(c); err == nil {
			t.Errorf("New without its %s was allowed", name)
		}
	}
}

func TestADateWithoutSignalsProposesNothing(t *testing.T) {
	h := newHarness(t)
	d := h.day
	d.Date = date(t, "2026-01-08")
	res := h.propose(t, d)
	if len(h.orders(t)) != 0 || res.Waiting+res.Protective+res.Blocked+res.Existing != 0 {
		t.Errorf("orders %q, result %+v", orderText(h.orders(t)), res)
	}
}

func TestDayOrdersCountOnlyWhatMayTrade(t *testing.T) {
	p := money.MustParsePrice("10.00")
	q := money.MustParseQuantity("2")
	mk := func(id int64, sym string, k signals.Kind, s orders.State) orders.Order {
		o := orders.Order{ID: id, Symbol: sym, Kind: k, State: s, Quantity: q}
		if orders.TypeFor(k) != orders.SellAtOpen {
			o.Price = &p
		}
		return o
	}
	list := []orders.Order{
		mk(1, "AAA", signals.KindBuy, orders.AwaitingApproval), // not yet approved
		mk(2, "BBB", signals.KindBuy, orders.Approved),
		mk(3, "CCC", signals.KindBuy, orders.Filled),
		mk(4, "DDD", signals.KindStopSell, orders.Approved), // a resting stop
		mk(5, "EEE", signals.KindStopSell, orders.Filled),
		mk(6, "FFF", signals.KindSell, orders.Accepted),
		mk(7, "GGG", signals.KindBuy, orders.Blocked),
		mk(8, "HHH", signals.KindBuy, orders.Approved), // the order being checked
	}
	got := dayOrders(list, 8, map[string]money.Price{"FFF": money.MustParsePrice("5.00")})
	want := []risk.DayOrder{
		{Symbol: "BBB", Kind: risk.Buy, Value: money.MustParseMoney("20.00"), Open: true},
		{Symbol: "CCC", Kind: risk.Buy, Value: money.MustParseMoney("20.00")},
		{Symbol: "EEE", Kind: risk.ProtectiveSell, Value: money.MustParseMoney("20.00")},
		{Symbol: "FFF", Kind: risk.Sell, Value: money.MustParseMoney("10.00")},
	}
	if len(got) != len(want) {
		t.Fatalf("day orders %+v, want %+v", got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Symbol != w.Symbol || g.Kind != w.Kind || !g.Value.Equal(w.Value) || g.Open != w.Open {
			t.Errorf("day order %d: %+v, want %+v", i, g, w)
		}
	}
}

func TestExpiryLeavesApprovedOrdersAlone(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.propose(t, h.day)
	// You approve the buy; only the sell is still waiting at the close.
	list := h.orders(t)
	if err := orders.Update(ctx, h.store, func(tx *sql.Tx) error {
		_, err := orders.Move(ctx, tx, list[2].ID, orders.Change{To: orders.Approved, Actor: orders.Operator,
			Reason: "Approved in a test.", At: h.clock.Now()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h.clock.Set(at(t, sessionDate, 16, 30))
	if n, err := h.p.Expire(ctx, date(t, sessionDate)); err != nil || n != 1 {
		t.Fatalf("expire: %d, %v", n, err)
	}
	if got := orderText(h.orders(t))[2]; got != sessionDate+" AAA buy day_limit_buy approved 9 11.11" {
		t.Errorf("approved buy after expiry: %s", got)
	}
	st, err := halt.Active(ctx, h.store.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if st.On() {
		t.Errorf("halts %+v", st.Active)
	}
}

func TestATighteningFromThePhoneAppliesAtProposal(t *testing.T) {
	h := newHarness(t)
	if _, err := limits.Tighten(context.Background(), h.store, h.strategy, h.clock.Now(), limits.OrderValue, "50.00"); err != nil {
		t.Fatal(err)
	}
	h.propose(t, h.day)
	want := []string{
		sessionDate + " BBB stop_sell stop approved 3 90.00",
		sessionDate + " EEE sell sell_at_open awaiting_approval 4 -",
		sessionDate + " AAA buy day_limit_buy blocked 9 11.11", // 99.99, within the file's 100.00
	}
	list := h.orders(t)
	if got := orderText(list); !reflect.DeepEqual(got, want) {
		t.Fatalf("orders:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(list[2].Verdict, "Refused: "+risk.CheckOrderValue) {
		t.Errorf("the buy's verdict is %q, want refused on order value", list[2].Verdict)
	}
}
