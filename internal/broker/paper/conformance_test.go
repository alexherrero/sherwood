package paper

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/broker/brokertest"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/prices/pricestest"
	"github.com/alexherrero/sherwood/internal/signals"
)

// conformance runs the shared adapter suite against the paper broker. Its
// book is the orders table, so the harness records each answer there as the
// execution layer would. Every symbol, price and amount is made up.
type conformance struct {
	*harness
	n int
}

func (c *conformance) Adapter() broker.Adapter { return c.pb }

var kinds = map[broker.Type]signals.Kind{
	broker.DayLimitBuy: signals.KindBuy,
	broker.Stop:        signals.KindStopSell,
	broker.SellAtOpen:  signals.KindSell,
}

// Order makes an approved order in the orders table, for a symbol of its own,
// and returns it as the broker would be asked to take it.
func (c *conformance) Order(t *testing.T, typ broker.Type) broker.Order {
	t.Helper()
	c.n++
	k := kinds[typ]
	var price *money.Price
	if typ != broker.SellAtOpen {
		price = p("12.34")
	}
	var o orders.Order
	err := orders.Update(context.Background(), c.store, func(tx *sql.Tx) error {
		var err error
		o, _, err = orders.Create(context.Background(), tx, orders.New{Strategy: "example-test",
			Symbol: fmt.Sprintf("ZZ%c", 'A'+c.n), Date: session, Kind: k, Quantity: q("2"), Price: price,
			Reason: "test: made-up reason", StrategyHash: hash}, orders.Proposer, before)
		if err != nil {
			return err
		}
		path := []orders.State{orders.AwaitingApproval, orders.Approved}
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
	return broker.Order{Ref: o.Key, Symbol: o.Symbol, Side: broker.Side(o.Side), Type: broker.Type(o.Type),
		Quantity: o.Quantity, Price: o.Price, Session: o.Date}
}

// Refused is an order for a Saturday, when the paper broker's market is shut.
func (c *conformance) Refused(*testing.T) broker.Order {
	return broker.Order{Ref: "made-up-refused-key", Symbol: "ZZR", Side: broker.Buy, Type: broker.DayLimitBuy,
		Quantity: q("2"), Price: p("12.34"), Session: time.Date(2026, 3, 7, 0, 0, 0, 0, clock.Eastern)}
}

func (c *conformance) move(t *testing.T, ref string, to ...orders.State) {
	t.Helper()
	err := orders.Update(context.Background(), c.store, func(tx *sql.Tx) error {
		o, err := orders.ByKey(context.Background(), tx, ref)
		if err != nil {
			return err
		}
		for _, s := range to {
			if o, err = orders.Move(context.Background(), tx, o.ID, orders.Change{To: s, Actor: orders.Execution,
				Reason: "test", At: before}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (c *conformance) Placed(t *testing.T, o broker.Order, r broker.Report) {
	t.Helper()
	if r.Status == broker.StatusAccepted {
		c.move(t, o.Ref, orders.Submitted, orders.Accepted)
	}
}

func (c *conformance) Cancelled(t *testing.T, ref string) { c.move(t, ref, orders.Cancelled) }

func (c *conformance) Unknown(*testing.T) string { return "no-such-ref" }

// closeAt is every held stock's made-up close on the previous trading day.
const closeAt = "20.00"

func (c *conformance) Hold(t *testing.T, cash money.Money, ps []broker.Position) broker.Account {
	t.Helper()
	hs := make([]holdings.Holding, len(ps))
	value := cash
	var syms []string
	for i, pos := range ps {
		hs[i] = holdings.Holding{Symbol: pos.Symbol, Quantity: pos.Quantity, AverageCost: pos.AverageCost,
			Opened: time.Date(2026, 2, 2, 0, 0, 0, 0, clock.Eastern)}
		value = value.Add(money.MustParsePrice(closeAt).Mul(pos.Quantity))
		syms = append(syms, pos.Symbol)
	}
	if _, _, err := holdings.Record(context.Background(), c.store, holdings.Snapshot{
		Source: holdings.SourceImport, AsOf: before, Holdings: hs, Cash: cash}); err != nil {
		t.Fatal(err)
	}
	if len(syms) > 0 {
		storeCloses(t, c.harness, "2026-03-02", closeAt, syms...)
	}
	return broker.Account{Cash: cash, Value: value}
}

// Break closes the store, so every later call that reads it fails. Place
// reads nothing, so it cannot fail.
func (c *conformance) Break(t *testing.T, m brokertest.Method) bool {
	if m == brokertest.Place {
		return false
	}
	if err := c.store.Close(); err != nil {
		t.Fatal(err)
	}
	return true
}

// storeCloses checks and stores a flat bar at closing for each symbol on date.
func storeCloses(t *testing.T, h *harness, date, closing string, syms ...string) {
	t.Helper()
	primary, check := pricestest.New("primary"), pricestest.New("cross")
	for _, s := range syms {
		b := pricestest.Bar(s, date, closing, closing, closing, closing, 1000)
		primary.Add(b)
		check.Add(b)
	}
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
	day, err := f.FetchDay(context.Background(), d, prices.Stocks{Held: syms}, tol)
	if err != nil || !day.Usable {
		t.Fatalf("FetchDay = %+v, %v", day, err)
	}
}

func TestPaperBrokerConforms(t *testing.T) {
	brokertest.Run(t, func(t *testing.T) brokertest.Harness { return &conformance{harness: newHarness(t)} })
}
