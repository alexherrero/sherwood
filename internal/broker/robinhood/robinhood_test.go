package robinhood

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/broker/brokertest"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
)

// Every symbol, id, price and amount here is made up.

// health is a settable broker.Health standing in for the supervisor.
type health struct{ down atomic.Bool }

func (h *health) Healthy() bool { return !h.down.Load() }

type rig struct {
	fh     *fakeHelper
	a      *Adapter
	helper *health
	alerts *notifytest.Fake
	log    *loggingtest.Output
	// extraKeys are keys sherwood holds beyond those the fake was sent.
	extraKeys []string
	keysErr   error
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{fh: newFakeHelper(t), helper: &health{}, alerts: &notifytest.Fake{}}
	log, out := loggingtest.New(t)
	r.log = out
	a, err := New(Config{
		Client: broker.NewClient(r.fh.path, time.Second),
		Helper: r.helper,
		Login:  &broker.LoginAlert{Notifier: r.alerts},
		Keys: func(context.Context) ([]string, error) {
			return append(r.fh.keys(), r.extraKeys...), r.keysErr
		},
		Logger: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.a = a
	return r
}

var session = time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)

func order(key string, typ broker.Type, symbol string) broker.Order {
	o := broker.Order{Ref: key, Symbol: symbol, Side: broker.Sell, Type: typ, Quantity: money.MustParseQuantity("2"),
		Session: session}
	if typ == broker.DayLimitBuy {
		o.Side = broker.Buy
	}
	if typ != broker.SellAtOpen {
		p := money.MustParsePrice("12.34")
		o.Price = &p
	}
	return o
}

// harness runs the conformance suite against the adapter over the fake helper.
type harness struct {
	*rig
	n int
}

func (h *harness) Adapter() broker.Adapter { return h.a }

func (h *harness) Order(_ *testing.T, typ broker.Type) broker.Order {
	h.n++
	return order(strings.Repeat(fmt.Sprint(h.n%10), 64)+fmt.Sprint(h.n), typ, "ZZA")
}

func (h *harness) Refused(t *testing.T) broker.Order {
	h.fh.mu.Lock()
	h.fh.refused["ZZX"] = "the broker says this stock cannot be traded"
	h.fh.mu.Unlock()
	o := h.Order(t, broker.DayLimitBuy)
	o.Symbol = "ZZX"
	return o
}

func (h *harness) Placed(*testing.T, broker.Order, broker.Report) {}
func (h *harness) Cancelled(*testing.T, string)                   {}
func (h *harness) Unknown(*testing.T) string                      { return strings.Repeat("f", 64) }

func (h *harness) Hold(_ *testing.T, cash money.Money, ps []broker.Position) broker.Account {
	h.fh.mu.Lock()
	defer h.fh.mu.Unlock()
	h.fh.holdings = []map[string]string{}
	for _, p := range ps {
		h.fh.holdings = append(h.fh.holdings, map[string]string{"symbol": p.Symbol, "quantity": p.Quantity.String(),
			"average_cost": p.AverageCost.String(), "opened_on": "2026-02-02"})
	}
	h.fh.cash = cash.String()
	value := cash.Add(money.MustParseMoney("250.00"))
	h.fh.value = value.String()
	return broker.Account{Cash: cash, Value: value}
}

func (h *harness) Break(_ *testing.T, m brokertest.Method) bool {
	h.fh.failNext(string(m), "broker_error")
	return true
}

func (h *harness) SetHealthy(_ *testing.T, ok bool) { h.helper.down.Store(!ok) }

func TestAdapterConforms(t *testing.T) {
	brokertest.Run(t, func(t *testing.T) brokertest.Harness { return &harness{rig: newRig(t)} })
}

func TestPlaceSendsTheHelpersArgumentsOnce(t *testing.T) {
	ctx := context.Background()
	key := strings.Repeat("a", 64)
	cases := []struct {
		typ  broker.Type
		want string
	}{
		{broker.DayLimitBuy, `{"price":"12.34","quantity":"2","ref":"` + key + `","side":"buy","symbol":"ZZA","type":"day_limit_buy"}`},
		{broker.Stop, `{"price":"12.34","quantity":"2","ref":"` + key + `","side":"sell","symbol":"ZZA","type":"stop"}`},
		{broker.SellAtOpen, `{"quantity":"2","ref":"` + key + `","side":"sell","symbol":"ZZA","type":"sell_at_open"}`},
	}
	for _, c := range cases {
		t.Run(string(c.typ), func(t *testing.T) {
			r := newRig(t)
			o := order(key, c.typ, "ZZA")
			o.Quantity = money.MustParseQuantity("2.000000")
			rep, err := r.a.Place(ctx, o)
			if err != nil || rep.Status != broker.StatusAccepted || rep.Ref != key {
				t.Fatalf("Place = %+v, %v", rep, err)
			}
			if got := mustJSON(r.fh.last()["args"]); got != c.want {
				t.Errorf("args = %s\nwant   %s", got, c.want)
			}
			if n := r.fh.count(opPlace); n != 1 {
				t.Errorf("place was sent %d times, want once", n)
			}
		})
	}
}

func TestAMalformedOrderNeverReachesTheHelper(t *testing.T) {
	r := newRig(t)
	o := order("k", broker.SellAtOpen, "ZZA")
	o.Side = broker.Buy
	rep, err := r.a.Place(context.Background(), o)
	if err != nil || rep.Status != broker.StatusRejected || !strings.Contains(rep.Detail, "must be a sell") {
		t.Fatalf("Place = %+v, %v; want a refusal", rep, err)
	}
	if r.fh.count(opPlace) != 0 {
		t.Error("a malformed order was sent to the helper")
	}
}

// Every helper error code is an error the caller halts on, and no call is
// ever made twice.
func TestEveryHelperErrorIsAnErrorAndIsNotRetried(t *testing.T) {
	ctx := context.Background()
	codes := []string{"login_needed", "broker_settings", "broker_error", "bad_broker_reply", "order_not_found",
		"bad_request", "internal", "unknown_op"}
	calls := map[string]func(a *Adapter) error{
		opPlace:      func(a *Adapter) error { _, err := a.Place(ctx, order("k", broker.Stop, "ZZA")); return err },
		opCancel:     func(a *Adapter) error { _, err := a.Cancel(ctx, "k"); return err },
		opLookup:     func(a *Adapter) error { _, err := a.Lookup(ctx, "k"); return err },
		opOpenOrders: func(a *Adapter) error { _, err := a.OpenOrders(ctx); return err },
		opHoldings:   func(a *Adapter) error { _, err := a.Holdings(ctx); return err },
		opCash:       func(a *Adapter) error { _, err := a.Cash(ctx); return err },
		opAccount:    func(a *Adapter) error { _, err := a.Account(ctx); return err },
	}
	for op, call := range calls {
		for _, code := range codes {
			t.Run(op+"/"+code, func(t *testing.T) {
				r := newRig(t)
				r.fh.failNext(op, code)
				err := call(r.a)
				var he *broker.HelperError
				if !errors.As(err, &he) || he.Code != code || he.Op != op {
					t.Fatalf("err = %v, want the helper's %s for %s", err, code, op)
				}
				if n := r.fh.count(op); n != 1 {
					t.Errorf("%s was sent %d times, want once", op, n)
				}
			})
		}
	}
}

func TestAHelperThatCannotBeReachedOrStaysSilentIsAnError(t *testing.T) {
	r := newRig(t)
	gone, err := New(Config{Client: broker.NewClient(r.fh.path+".gone", time.Second), Helper: r.helper,
		Login: &broker.LoginAlert{Notifier: r.alerts}, Keys: r.a.keys, Logger: r.a.log})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gone.Place(context.Background(), order("k", broker.Stop, "ZZA")); !errors.Is(err, broker.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}

	r.fh.mu.Lock()
	r.fh.silent[opPlace] = true
	r.fh.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.a.Place(ctx, order("k", broker.Stop, "ZZA")); !errors.Is(err, broker.ErrNoReply) {
		t.Fatalf("err = %v, want ErrNoReply", err)
	}
	if n := r.fh.count(opPlace); n != 1 {
		t.Errorf("place was sent %d times, want once", n)
	}
}

func TestAnswersThatBreakTheContractAreErrors(t *testing.T) {
	ctx := context.Background()
	key := strings.Repeat("a", 64)
	ref := ClientRef(key)
	report := func(over map[string]any) string {
		r := map[string]any{"ref": key, "client_ref": ref, "broker_id": "00000000-0000-0000-0000-000000000001",
			"status": "accepted", "filled": "0", "fill_price": nil, "detail": ""}
		for k, v := range over {
			if v == "<drop>" {
				delete(r, k)
			} else {
				r[k] = v
			}
		}
		return mustJSON(r)
	}
	open := func(reports ...string) string {
		return `{"as_of":"2026-03-03T15:00:00Z","orders":[` + strings.Join(reports, ",") + `]}`
	}
	opened := func(over map[string]any) string {
		over["ref"] = nil
		return report(over)
	}
	cases := []struct {
		name, op, result string
	}{
		{"about another order", opLookup, report(map[string]any{"ref": "other"})},
		{"with no key", opLookup, report(map[string]any{"ref": nil})},
		{"another client reference", opCancel, report(map[string]any{"client_ref": ClientRef("other")})},
		{"no client reference", opPlace, report(map[string]any{"client_ref": nil})},
		{"an unknown status", opLookup, report(map[string]any{"status": "pending"})},
		{"no status", opLookup, report(map[string]any{"status": "<drop>"})},
		{"no detail", opLookup, report(map[string]any{"detail": nil})},
		{"filled not a decimal", opLookup, report(map[string]any{"filled": "lots"})},
		{"filled as a number", opLookup, report(map[string]any{"filled": 4})},
		{"a fill price with nothing filled", opLookup, report(map[string]any{"fill_price": "9.48"})},
		{"a fill price that is not a decimal", opLookup, report(map[string]any{"filled": "4", "fill_price": "cheap"})},
		{"a fill price of zero", opLookup, report(map[string]any{"filled": "4", "fill_price": "0.001"})},
		{"an accepted order with no broker id", opPlace, report(map[string]any{"broker_id": nil})},
		{"an unknown field", opLookup, report(map[string]any{"extra": 1})},
		{"not an object", opLookup, `[]`},
		{"null", opLookup, `null`},
		{"an open order with a key", opOpenOrders, open(report(nil))},
		{"an open order that is filled", opOpenOrders, open(opened(map[string]any{"status": "filled"}))},
		{"no order list", opOpenOrders, `{"as_of":"x","orders":null}`},
		{"no holdings list", opHoldings, `{"as_of":"x"}`},
		{"a holding with no symbol", opHoldings, `{"as_of":"x","holdings":[{"symbol":"","quantity":"1","average_cost":"1","opened_on":"2026-01-02"}]}`},
		{"two holdings of one stock", opHoldings, `{"as_of":"x","holdings":[` +
			`{"symbol":"ZZA","quantity":"1","average_cost":"1","opened_on":"2026-01-02"},` +
			`{"symbol":"ZZA","quantity":"2","average_cost":"1","opened_on":"2026-01-02"}]}`},
		{"a holding of no shares", opHoldings, `{"as_of":"x","holdings":[{"symbol":"ZZA","quantity":"0","average_cost":"1","opened_on":"2026-01-02"}]}`},
		{"a negative average cost", opHoldings, `{"as_of":"x","holdings":[{"symbol":"ZZA","quantity":"1","average_cost":"-1","opened_on":"2026-01-02"}]}`},
		{"cash that is not a decimal", opCash, `{"as_of":"x","cash":"some","buying_power":"1"}`},
		{"no cash", opCash, `{"as_of":"x","buying_power":"1"}`},
		{"a value that is not a decimal", opAccount, `{"as_of":"x","cash":"1","buying_power":"1","value":"1e3"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.extraKeys = []string{key}
			r.fh.resultNext(c.op, c.result)
			var err error
			switch c.op {
			case opPlace:
				_, err = r.a.Place(ctx, order(key, broker.Stop, "ZZA"))
			case opCancel:
				_, err = r.a.Cancel(ctx, key)
			case opLookup:
				_, err = r.a.Lookup(ctx, key)
			case opOpenOrders:
				_, err = r.a.OpenOrders(ctx)
			case opHoldings:
				_, err = r.a.Holdings(ctx)
			case opCash:
				_, err = r.a.Cash(ctx)
			case opAccount:
				_, err = r.a.Account(ctx)
			}
			if !errors.Is(err, ErrBadReply) && !errors.Is(err, broker.ErrBadReply) {
				t.Fatalf("err = %v, want a bad reply", err)
			}
		})
	}
}

func TestReportsCarryFillsAndTheBrokersReason(t *testing.T) {
	ctx := context.Background()
	key := strings.Repeat("a", 64)
	r := newRig(t)
	r.fh.resultNext(opLookup,
		mustJSON(map[string]any{"ref": key, "client_ref": ClientRef(key), "broker_id": "00000000-0000-0000-0000-000000000002",
			"status": "partially_filled", "filled": "4", "fill_price": "9.48", "detail": ""}),
		// An average over several fills can run finer than a cent.
		mustJSON(map[string]any{"ref": key, "client_ref": ClientRef(key), "broker_id": "00000000-0000-0000-0000-000000000002",
			"status": "filled", "filled": "10", "fill_price": "12.3450", "detail": ""}),
	)
	r.fh.resultNext(opPlace, mustJSON(map[string]any{"ref": key, "client_ref": ClientRef(key), "broker_id": nil,
		"status": "rejected", "filled": "0", "fill_price": nil, "detail": "the broker lists no stock with this symbol"}))

	rep, err := r.a.Lookup(ctx, key)
	if err != nil || rep.Status != broker.StatusPartiallyFilled || rep.Filled.String() != "4" ||
		rep.FillPrice == nil || rep.FillPrice.String() != "9.48" {
		t.Fatalf("Lookup = %+v, %v", rep, err)
	}
	rep, err = r.a.Lookup(ctx, key)
	if err != nil || rep.Status != broker.StatusFilled || rep.FillPrice == nil || rep.FillPrice.String() != "12.35" {
		t.Fatalf("Lookup = %+v, %v; want the fill price rounded to 12.35", rep, err)
	}
	rep, err = r.a.Place(ctx, order(key, broker.DayLimitBuy, "ZZQ"))
	if err != nil || rep.Status != broker.StatusRejected || rep.Detail != "the broker lists no stock with this symbol" {
		t.Fatalf("Place = %+v, %v; want the broker's refusal as a report", rep, err)
	}
}

func TestOpenOrdersPutBackSherwoodsKeysAndNameStrangers(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	mine := order(strings.Repeat("1", 64), broker.Stop, "ZZA")
	if _, err := r.a.Place(ctx, mine); err != nil {
		t.Fatal(err)
	}
	// An order sherwood called off but the broker still lists, by the key
	// sherwood holds for it.
	called := strings.Repeat("2", 64)
	r.extraKeys = []string{called}
	r.fh.mu.Lock()
	r.fh.foreign = []map[string]any{
		{"ref": nil, "client_ref": "99999999-9999-4999-8999-999999999999", "broker_id": "00000000-0000-0000-0000-000000000077",
			"status": "accepted", "filled": "0", "fill_price": nil, "detail": ""},
		{"ref": nil, "client_ref": nil, "broker_id": "00000000-0000-0000-0000-000000000078",
			"status": "partially_filled", "filled": "1", "fill_price": "3.21", "detail": ""},
		{"ref": nil, "client_ref": ClientRef(called), "broker_id": "00000000-0000-0000-0000-000000000079",
			"status": "accepted", "filled": "0", "fill_price": nil, "detail": ""},
	}
	r.fh.mu.Unlock()

	open, err := r.a.OpenOrders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, o := range open {
		refs = append(refs, o.Ref)
	}
	want := []string{
		mine.Ref,
		called,
		"foreign-order:00000000-0000-0000-0000-000000000077",
		"foreign-order:00000000-0000-0000-0000-000000000078",
	}
	if strings.Join(refs, " ") != strings.Join(want, " ") {
		t.Fatalf("OpenOrders refs = %v\nwant %v", refs, want)
	}
	if open[3].FillPrice == nil || open[3].FillPrice.String() != "3.21" || open[3].Status != broker.StatusPartiallyFilled {
		t.Errorf("the foreign partial fill = %+v", open[3])
	}

	// Two open orders with one key's reference: a double send.
	r.fh.mu.Lock()
	r.fh.foreign = append(r.fh.foreign, map[string]any{"ref": nil, "client_ref": ClientRef(called),
		"broker_id": "00000000-0000-0000-0000-000000000080", "status": "accepted", "filled": "0", "fill_price": nil, "detail": ""})
	r.fh.mu.Unlock()
	if _, err := r.a.OpenOrders(ctx); !errors.Is(err, ErrBadReply) || !strings.Contains(err.Error(), "sent twice") {
		t.Fatalf("err = %v, want a double send reported", err)
	}

	// Sherwood's keys cannot be read: the listing cannot be trusted.
	r.keysErr = errors.New("made-up database failure")
	if _, err := r.a.OpenOrders(ctx); err == nil || !strings.Contains(err.Error(), "made-up database failure") {
		t.Fatalf("err = %v, want the keys' failure", err)
	}
}

func TestALoginNeededStreakSendsOneAlertAndMakesTheAdapterUnwell(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if !r.a.Healthy() {
		t.Fatal("a new adapter is not healthy")
	}
	r.fh.failNext(opHoldings, "login_needed", "login_needed")
	r.fh.failNext(opAccount, "login_needed")
	r.fh.failNext(opCash, "broker_error")
	for range 2 {
		if _, err := r.a.Holdings(ctx); !broker.LoginNeeded(err) {
			t.Fatalf("err = %v, want login_needed", err)
		}
	}
	if _, err := r.a.Account(ctx); !broker.LoginNeeded(err) {
		t.Fatalf("err = %v, want login_needed", err)
	}
	if n := len(r.alerts.Sent()); n != 1 {
		t.Fatalf("a streak of three sent %d alerts, want one", n)
	}
	if r.a.Healthy() {
		t.Error("the adapter is healthy while the broker session has lapsed")
	}
	// Another kind of error neither ends the streak nor alerts.
	if _, err := r.a.Cash(ctx); err == nil || broker.LoginNeeded(err) {
		t.Fatalf("err = %v, want a broker_error", err)
	}
	if r.a.Healthy() || len(r.alerts.Sent()) != 1 {
		t.Error("a broker_error ended the login streak")
	}
	// A call that works ends the streak; the next lapse alerts again.
	if _, err := r.a.Cash(ctx); err != nil {
		t.Fatal(err)
	}
	if !r.a.Healthy() {
		t.Error("the adapter is still unwell after a call worked")
	}
	r.fh.failNext(opOpenOrders, "login_needed")
	if _, err := r.a.OpenOrders(ctx); !broker.LoginNeeded(err) {
		t.Fatalf("err = %v, want login_needed", err)
	}
	if got := r.alerts.Sent(); len(got) != 2 || got[1].Name() != "broker_login_needed" {
		t.Fatalf("alerts = %v, want a second broker_login_needed", got)
	}
}

func TestAnAlertThatCannotBeSentIsLogged(t *testing.T) {
	r := newRig(t)
	r.alerts.Err = errors.New("made-up push failure")
	r.fh.failNext(opLookup, "login_needed")
	if _, err := r.a.Lookup(context.Background(), "k"); !broker.LoginNeeded(err) {
		t.Fatalf("err = %v, want login_needed", err)
	}
	if !strings.Contains(r.log.String(), "the broker login alert could not be sent") ||
		!strings.Contains(r.log.String(), "made-up push failure") {
		t.Errorf("log = %s", r.log.String())
	}
}

func TestTheAdapterIsUnwellWhileTheHelperIsDown(t *testing.T) {
	r := newRig(t)
	r.helper.down.Store(true)
	if r.a.Healthy() {
		t.Error("healthy while the helper is down")
	}
}

// recorder is a Caller that records each call's time limit.
type recorder struct{ limits map[string]time.Duration }

func (c *recorder) CallFor(_ context.Context, op string, timeout time.Duration, _, _ any) error {
	c.limits[op] = timeout
	return errors.New("made-up failure")
}

func TestEachCallGetsItsTimeLimit(t *testing.T) {
	ctx := context.Background()
	c := &recorder{limits: map[string]time.Duration{}}
	log, _ := loggingtest.New(t)
	a, err := New(Config{Client: c, Helper: &health{}, Login: &broker.LoginAlert{Notifier: &notifytest.Fake{}},
		Keys: func(context.Context) ([]string, error) { return nil, nil }, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = a.Place(ctx, order("k", broker.Stop, "ZZA"))
	_, _ = a.Cancel(ctx, "k")
	_, _ = a.Lookup(ctx, "k")
	_, _ = a.OpenOrders(ctx)
	_, _ = a.Holdings(ctx)
	_, _ = a.Cash(ctx)
	_, _ = a.Account(ctx)
	want := map[string]time.Duration{opPlace: time.Minute, opCancel: time.Minute, opLookup: time.Minute,
		opOpenOrders: time.Minute, opHoldings: 2 * time.Minute, opCash: 30 * time.Second, opAccount: 30 * time.Second}
	for op, d := range want {
		if c.limits[op] != d {
			t.Errorf("%s: time limit %s, want %s", op, c.limits[op], d)
		}
	}
}

func TestNewRefusesAMissingPart(t *testing.T) {
	log, _ := loggingtest.New(t)
	ok := Config{Client: &recorder{}, Helper: &health{}, Login: &broker.LoginAlert{},
		Keys: func(context.Context) ([]string, error) { return nil, nil }, Logger: log}
	if _, err := New(ok); err != nil {
		t.Fatalf("a full config was refused: %v", err)
	}
	for name, spoil := range map[string]func(c *Config){
		"client": func(c *Config) { c.Client = nil },
		"helper": func(c *Config) { c.Helper = nil },
		"login":  func(c *Config) { c.Login = nil },
		"keys":   func(c *Config) { c.Keys = nil },
		"logger": func(c *Config) { c.Logger = nil },
	} {
		c := ok
		spoil(&c)
		if _, err := New(c); err == nil {
			t.Errorf("a config with no %s was accepted", name)
		}
	}
}
