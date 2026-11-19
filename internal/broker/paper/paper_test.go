package paper

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/prices/pricestest"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every symbol, price, amount and strategy here is made up.

var (
	// session is Tuesday 3 March 2026, a full trading day.
	session = time.Date(2026, 3, 3, 0, 0, 0, 0, clock.Eastern)
	before  = time.Date(2026, 3, 2, 16, 0, 0, 0, clock.Eastern) // the previous close
	evening = time.Date(2026, 3, 3, 16, 30, 0, 0, clock.Eastern)
	hash    = strings.Repeat("cd", 32)
)

type harness struct {
	store *store.Store
	clock *clock.Fake
	pb    *Broker
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sherwood.db"), before)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	h := &harness{store: s, clock: clock.NewFake(evening)}
	h.pb, err = New(Config{Store: s, Clock: h.clock, Calendar: clock.NYSE(), Policy: money.WholeShares})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func q(s string) money.Quantity { return money.MustParseQuantity(s) }

func p(s string) *money.Price {
	v := money.MustParsePrice(s)
	return &v
}

// hold records holdings (symbol → shares at an average cost of 10.00) and cash
// at the previous close.
func (h *harness) hold(t *testing.T, cash string, held map[string]string) {
	t.Helper()
	var hs []holdings.Holding
	for sym, n := range held {
		hs = append(hs, holdings.Holding{Symbol: sym, Quantity: q(n), AverageCost: money.MustParseMoney("10.00"),
			Opened: time.Date(2026, 2, 2, 0, 0, 0, 0, clock.Eastern)})
	}
	if _, _, err := holdings.Record(context.Background(), h.store, holdings.Snapshot{
		Source: holdings.SourceImport, AsOf: before, Holdings: hs, Cash: money.MustParseMoney(cash)}); err != nil {
		t.Fatal(err)
	}
}

// accepted makes an order of kind k for date and moves it to accepted, the
// way the execution layer leaves a sent order.
func (h *harness) accepted(t *testing.T, k signals.Kind, sym, shares string, price *money.Price, date time.Time) orders.Order {
	t.Helper()
	var o orders.Order
	err := orders.Update(context.Background(), h.store, func(tx *sql.Tx) error {
		var err error
		o, _, err = orders.Create(context.Background(), tx, orders.New{Strategy: "example-test", Symbol: sym,
			Date: date, Kind: k, Quantity: q(shares), Price: price, Reason: "test: made-up reason", StrategyHash: hash},
			orders.Proposer, before)
		if err != nil {
			return err
		}
		path := []orders.State{orders.AwaitingApproval, orders.Approved, orders.Submitted, orders.Accepted}
		if k == signals.KindStopSell {
			path = path[1:]
		}
		for _, to := range path {
			if o, err = orders.Move(context.Background(), tx, o.ID, orders.Change{To: to, Actor: orders.Execution,
				Reason: "test", At: before}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func (h *harness) order(t *testing.T, id int64) orders.Order {
	t.Helper()
	o, err := orders.Get(context.Background(), h.store.Reader(), id)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func bar(sym, open, high, low, closing string) prices.Bar {
	return pricestest.Bar(sym, "2026-03-03", open, high, low, closing, 1000)
}

func (h *harness) settle(t *testing.T, bars ...prices.Bar) *Settlement {
	t.Helper()
	res, err := h.pb.Settle(context.Background(), session, bars)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestFills(t *testing.T) {
	cases := []struct {
		name      string
		kind      signals.Kind
		price     *money.Price
		bar       prices.Bar
		fills     bool
		at        string
		wantState orders.State
	}{
		{"a gap down through a stop fills at the open", signals.KindStopSell, p("9.00"),
			bar("AAA", "8.50", "8.80", "8.20", "8.60"), true, "8.50", orders.Filled},
		{"a stop the low reaches fills at the stop", signals.KindStopSell, p("9.00"),
			bar("AAA", "9.50", "9.60", "9.00", "9.40"), true, "9.00", orders.Filled},
		{"a stop the low stays above keeps resting", signals.KindStopSell, p("9.00"),
			bar("AAA", "9.50", "9.60", "9.01", "9.40"), false, "", orders.Accepted},
		{"a buy limit not reached does not fill and expires", signals.KindAdd, p("10.00"),
			bar("AAA", "10.50", "10.80", "10.20", "10.60"), false, "", orders.Expired},
		{"a low that only touches the limit does not fill", signals.KindAdd, p("10.00"),
			bar("AAA", "10.30", "10.50", "10.00", "10.40"), false, "", orders.Expired},
		{"an open below the limit fills at the open", signals.KindAdd, p("10.00"),
			bar("AAA", "9.80", "10.20", "9.70", "10.10"), true, "9.80", orders.Filled},
		{"a low through the limit fills at the limit", signals.KindAdd, p("10.00"),
			bar("AAA", "10.30", "10.50", "9.95", "10.40"), true, "10.00", orders.Filled},
		{"a sell at the open fills at the open", signals.KindTrim, nil,
			bar("AAA", "10.30", "10.50", "9.95", "10.40"), true, "10.30", orders.Filled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.hold(t, "1000.00", map[string]string{"AAA": "10"})
			o := h.accepted(t, c.kind, "AAA", "4", c.price, session)
			res := h.settle(t, c.bar)
			if got := h.order(t, o.ID).State; got != c.wantState {
				t.Errorf("state %s, want %s", got, c.wantState)
			}
			if !c.fills {
				if len(res.Fills) != 0 || len(res.Unfilled) != 1 || res.Holdings != nil {
					t.Fatalf("settlement %+v, want one unfilled order and no snapshot", res)
				}
				return
			}
			if len(res.Fills) != 1 || res.Fills[0].Fill.Price.String() != c.at || !res.Fills[0].Fill.Quantity.Equal(q("4")) {
				t.Fatalf("fills %+v, want 4 at %s", res.Fills, c.at)
			}
			fills, err := orders.Fills(context.Background(), h.store.Reader(), o.ID)
			if err != nil || len(fills) != 1 || fills[0].Price.String() != c.at {
				t.Fatalf("stored fills %+v, %v", fills, err)
			}
			// Holdings and cash move by exactly the fill.
			value := money.MustParsePrice(c.at).Mul(q("4"))
			wantShares, wantCash := "6", money.MustParseMoney("1000.00").Add(value)
			if c.kind == signals.KindAdd {
				wantShares, wantCash = "14", money.MustParseMoney("1000.00").Sub(value)
			}
			if res.Holdings == nil || res.Holdings.Source != holdings.SourceFills {
				t.Fatalf("snapshot %+v, want one from fills", res.Holdings)
			}
			got, _ := res.Holdings.Find("AAA")
			if got.Quantity.String() != wantShares || !res.Cash.Amount.Equal(wantCash) {
				t.Errorf("after the fill: %s shares and %s cash, want %s and %s", got.Quantity, res.Cash.Amount, wantShares, wantCash)
			}
		})
	}
}

func TestSettlementWritesOneSnapshotAtTheCloseAndIsIdempotent(t *testing.T) {
	h := newHarness(t)
	h.hold(t, "1000.00", map[string]string{"AAA": "10"})
	trim := h.accepted(t, signals.KindTrim, "AAA", "4", nil, session)
	stop := h.accepted(t, signals.KindStopSell, "AAA", "6", p("9.00"), session)
	buy := h.accepted(t, signals.KindBuy, "BBB", "5", p("20.00"), session)
	later := h.accepted(t, signals.KindBuy, "CCC", "1", p("5.00"), session.AddDate(0, 0, 1))

	res := h.settle(t, bar("AAA", "10.00", "10.00", "8.50", "8.80"), bar("BBB", "19.50", "20.00", "19.00", "19.80"),
		bar("CCC", "4.00", "4.00", "4.00", "4.00"))
	// The trim goes first at the open, then the stop on what is left, then the buy.
	var order []int64
	for _, f := range res.Fills {
		order = append(order, f.Order.ID)
	}
	if len(order) != 3 || order[0] != trim.ID || order[1] != stop.ID || order[2] != buy.ID {
		t.Fatalf("filled orders %v, want trim %d, stop %d, buy %d", order, trim.ID, stop.ID, buy.ID)
	}
	if h.order(t, later.ID).State != orders.Accepted {
		t.Error("an order for a later session was worked")
	}
	set, err := holdings.Current(context.Background(), h.store.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set.Find("AAA"); ok || len(set.Holdings) != 1 {
		t.Fatalf("holdings %+v, want only BBB", set.Holdings)
	}
	bbb, _ := set.Find("BBB")
	if !bbb.Opened.Equal(session) || bbb.AverageCost.String() != "19.50" {
		t.Errorf("BBB opened %s at %s, want %s at 19.50", bbb.Opened, bbb.AverageCost, session)
	}
	if want := time.Date(2026, 3, 3, 16, 0, 0, 0, clock.Eastern); !set.AsOf.Equal(want) || set.Source != holdings.SourceFills {
		t.Errorf("snapshot as of %s from %s, want the close %s from fills", set.AsOf, set.Source, want)
	}
	// 1000 + 4 × 10.00 + 6 × 9.00 − 5 × 19.50 = 996.50
	cash, err := holdings.LatestCash(context.Background(), h.store.Reader())
	if err != nil || cash.Amount.String() != "996.50" {
		t.Errorf("cash %s, %v, want 996.50", cash.Amount, err)
	}

	again := h.settle(t, bar("AAA", "10.00", "10.00", "8.50", "8.80"), bar("BBB", "19.50", "20.00", "19.00", "19.80"))
	if len(again.Fills) != 0 || again.Holdings != nil {
		t.Errorf("a second settlement filled %d orders", len(again.Fills))
	}
	n := 0
	if err := h.store.Reader().QueryRow(`SELECT count(*) FROM holding_snapshots`).Scan(&n); err != nil || n != 2 {
		t.Errorf("%d snapshots, %v, want the import and one from fills", n, err)
	}
}

func TestAnAddAveragesTheCost(t *testing.T) {
	h := newHarness(t)
	h.hold(t, "1000.00", map[string]string{"AAA": "10"}) // 10 at 10.00
	h.accepted(t, signals.KindAdd, "AAA", "5", p("13.00"), session)
	res := h.settle(t, bar("AAA", "13.00", "13.00", "13.00", "13.00"))
	got, _ := res.Holdings.Find("AAA")
	// (100.00 + 65.00) / 15 = 11.00
	if got.Quantity.String() != "15" || !got.AverageCost.Equal(money.MustParseMoney("11")) {
		t.Errorf("%s shares at %s, want 15 at 11.00", got.Quantity, got.AverageCost)
	}
	if !got.Opened.Equal(time.Date(2026, 2, 2, 0, 0, 0, 0, clock.Eastern)) {
		t.Errorf("opened %s; an add keeps the holding's opening date", got.Opened)
	}
}

func TestSettleRefusesASellOfSharesNotHeldAndWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.hold(t, "1000.00", map[string]string{"AAA": "3"})
	trim := h.accepted(t, signals.KindTrim, "AAA", "2", nil, session)
	h.accepted(t, signals.KindStopSell, "AAA", "3", p("9.00"), session)
	_, err := h.pb.Settle(context.Background(), session, []prices.Bar{bar("AAA", "8.00", "8.00", "8.00", "8.00")})
	if !errors.Is(err, ErrSettle) {
		t.Fatalf("Settle = %v, want ErrSettle", err)
	}
	if h.order(t, trim.ID).State != orders.Accepted {
		t.Error("a refused settlement still filled an order")
	}
}

func TestSettleSkipsStocksWithoutABarAndExpiresStaleDayOrders(t *testing.T) {
	h := newHarness(t)
	h.hold(t, "1000.00", map[string]string{"AAA": "10"})
	stale := h.accepted(t, signals.KindBuy, "BBB", "1", p("20.00"), session.AddDate(0, 0, -1))
	noBar := h.accepted(t, signals.KindStopSell, "AAA", "10", p("9.00"), session)
	sellNoBar := h.accepted(t, signals.KindTrim, "CCC", "1", nil, session)
	res := h.settle(t, bar("BBB", "1.00", "1.00", "1.00", "1.00"))
	if len(res.Fills) != 0 || len(res.Skipped) != 3 || len(res.Expired) != 2 {
		t.Fatalf("settlement %+v, want three skipped and two expired", res)
	}
	want := map[int64]orders.State{stale.ID: orders.Expired, sellNoBar.ID: orders.Expired, noBar.ID: orders.Accepted}
	for id, st := range want {
		if got := h.order(t, id).State; got != st {
			t.Errorf("order %d is %s, want %s", id, got, st)
		}
	}
}

func TestSettleRefusesAClosedDayAndAWrongBar(t *testing.T) {
	h := newHarness(t)
	if _, err := h.pb.Settle(context.Background(), time.Date(2026, 3, 7, 0, 0, 0, 0, clock.Eastern), nil); !errors.Is(err, ErrSettle) {
		t.Errorf("a Saturday: %v", err)
	}
	b := pricestest.Bar("AAA", "2026-03-02", "1.00", "1.00", "1.00", "1.00", 1)
	if _, err := h.pb.Settle(context.Background(), session, []prices.Bar{b}); !errors.Is(err, ErrSettle) {
		t.Errorf("another date's bar: %v", err)
	}
}

func TestTheAdapterOperations(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.hold(t, "500.00", map[string]string{"AAA": "10"})
	o := h.accepted(t, signals.KindStopSell, "AAA", "10", p("9.00"), session)

	ok := broker.Order{Ref: "ref-1", Symbol: "AAA", Side: broker.Sell, Type: broker.Stop, Quantity: q("10"),
		Price: p("9.00"), Session: session}
	if r, err := h.pb.Place(ctx, ok); err != nil || r.Status != broker.StatusAccepted || r.Ref != "ref-1" {
		t.Errorf("Place = %+v, %v", r, err)
	}
	for name, bad := range map[string]broker.Order{
		"no price":  {Ref: "r", Symbol: "AAA", Side: broker.Sell, Type: broker.Stop, Quantity: q("1"), Session: session},
		"wrong way": {Ref: "r", Symbol: "AAA", Side: broker.Buy, Type: broker.SellAtOpen, Quantity: q("1"), Session: session},
		"a fraction": {Ref: "r", Symbol: "AAA", Side: broker.Sell, Type: broker.SellAtOpen, Quantity: q("0.5"),
			Session: session},
		"a Saturday": {Ref: "r", Symbol: "AAA", Side: broker.Sell, Type: broker.SellAtOpen, Quantity: q("1"),
			Session: time.Date(2026, 3, 7, 0, 0, 0, 0, clock.Eastern)},
	} {
		if r, err := h.pb.Place(ctx, bad); err != nil || r.Status != broker.StatusRejected || r.Detail == "" {
			t.Errorf("%s: Place = %+v, %v, want a rejection with a reason", name, r, err)
		}
	}

	if r, err := h.pb.Lookup(ctx, o.Key); err != nil || r.Status != broker.StatusAccepted {
		t.Errorf("Lookup = %+v, %v", r, err)
	}
	open, err := h.pb.OpenOrders(ctx)
	if err != nil || len(open) != 1 || open[0].Ref != o.Key {
		t.Errorf("OpenOrders = %+v, %v", open, err)
	}
	if r, err := h.pb.Cancel(ctx, o.Key); err != nil || r.Status != broker.StatusCancelled {
		t.Errorf("Cancel = %+v, %v", r, err)
	}
	if _, err := h.pb.Lookup(ctx, "no-such-ref"); err == nil {
		t.Error("Lookup of an unknown reference succeeded")
	}
	pos, err := h.pb.Holdings(ctx)
	if err != nil || len(pos) != 1 || pos[0].Symbol != "AAA" || pos[0].Quantity.String() != "10" {
		t.Errorf("Holdings = %+v, %v", pos, err)
	}

	// The account is valued at the latest checked close.
	if _, err := h.pb.Account(ctx); err == nil {
		t.Error("Account valued a holding with no checked close")
	}
	storeDay(t, h, "2026-03-02", "12.00")
	a, err := h.pb.Account(ctx)
	if err != nil || a.Cash.String() != "500.00" || a.Value.String() != "620.00" {
		t.Errorf("Account = %+v, %v, want 500.00 cash and 620.00 value", a, err)
	}
}

// storeDay checks and stores a flat AAA bar for date through the fetcher.
func storeDay(t *testing.T, h *harness, date, c string) {
	t.Helper()
	primary, check := pricestest.New("primary"), pricestest.New("cross")
	b := pricestest.Bar("AAA", date, c, c, c, c, 1000)
	primary.Add(b)
	check.Add(b)
	log, _ := loggingtest.New(t)
	f := &prices.Fetcher{Primary: primary, Check: check, Clock: h.clock, Calendar: clock.NYSE(), Store: h.store,
		Log: log, HistoryDays: 5}
	tol, err := config.ParseDecimal("0.02")
	if err != nil {
		t.Fatal(err)
	}
	d, err := time.ParseInLocation(clock.DateLayout, date, clock.Eastern)
	if err != nil {
		t.Fatal(err)
	}
	day, err := f.FetchDay(context.Background(), d, prices.Stocks{Held: []string{"AAA"}}, tol)
	if err != nil || !day.Usable {
		t.Fatalf("FetchDay = %+v, %v", day, err)
	}
}

func TestCashIsReadEvenWhenAHoldingCannotBeValued(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if c, err := h.pb.Cash(ctx); err != nil || !c.IsZero() {
		t.Fatalf("Cash with nothing recorded = %s, %v; want zero", c, err)
	}
	h.hold(t, "1234.50", map[string]string{"ZZZ": "3"}) // ZZZ has no checked close
	if _, err := h.pb.Account(ctx); err == nil {
		t.Fatal("Account valued a holding with no close")
	}
	if c, err := h.pb.Cash(ctx); err != nil || c.String() != "1234.50" {
		t.Fatalf("Cash = %s, %v; want 1234.50", c, err)
	}
}
