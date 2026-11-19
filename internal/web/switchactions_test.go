package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexherrero/sherwood/internal/broker/brokertest"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/execution"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/signals"
)

// Every symbol, price, amount and address here is made up.

// newTradingFixture is a fixture whose stop switch cancels through a real
// execution layer over the fake broker, with the example strategy file's
// limits.
func newTradingFixture(t *testing.T) (*fixture, *brokertest.Fake) {
	t.Helper()
	fake := &brokertest.Fake{}
	f := newFixtureWith(t, config.ModePaper, func(f *fixture, cfg *Config) {
		cfg.Strategy = exampleStrategy(t)
		ex, err := execution.New(execution.Config{Store: f.store, Clock: f.clock, Calendar: clock.NYSE(), Adapter: fake,
			Checker: f.checker, Switch: f.stop, Strategy: cfg.Strategy, Logger: cfg.Logger})
		if err != nil {
			t.Fatal(err)
		}
		cfg.Orders = ex
	})
	return f, fake
}

func exampleStrategy(t *testing.T) *config.Strategy {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "strategy.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := config.ParseStrategy(b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTheSwitchDeclinesAndCancelsBuysAndKeepsStopsUntilTheSecondConfirmation(t *testing.T) {
	f, fake := newTradingFixture(t)
	waiting := f.propose(signals.KindBuy, "AAA", "10", "12.00")
	open := f.propose(signals.KindBuy, "BBB", "2", "20.00", orders.Approved, orders.Submitted, orders.Accepted)
	stop := f.propose(signals.KindStopSell, "CCC", "5", "9.00", orders.Submitted, orders.Accepted)
	c, token := f.session()

	wantToday(t, f.postSwitch(switchOnPath, token, c, fromAddr(phoneAddr)), "on")
	if o, e := f.order(waiting.ID), f.lastEvent(waiting.ID); o.State != orders.Declined || e.Actor != orders.StopSwitch {
		t.Errorf("the waiting buy is %s by %s, want declined by the stop switch", o.State, e.Actor)
	}
	if o, e := f.order(open.ID), f.lastEvent(open.ID); o.State != orders.Cancelled || e.Actor != orders.StopSwitch {
		t.Errorf("the open buy is %s by %s, want cancelled by the stop switch", o.State, e.Actor)
	}
	if o := f.order(stop.ID); o.State != orders.Accepted {
		t.Errorf("the protective stop is %s, want still resting", o.State)
	}
	if calls := fake.Calls(); len(calls) != 1 || calls[0].Method != brokertest.Cancel || calls[0].Ref != open.Key {
		t.Fatalf("broker calls = %+v, want one cancel of the open buy", calls)
	}
	wantIn(t, f.page("/?switch=on"), "open buys are cancelled at the broker. Protective stops stay in place.",
		"1 protective stop is resting at the broker.", "Cancel protective stops too")

	// The second confirmation needs the box ticked and an approval device.
	wantToday(t, f.postSwitch(switchStopsPath, token, c, fromAddr(phoneAddr)), "stops_confirm")
	if w := f.postSwitch(switchStopsPath, token, c, fromAddr(laptopAddr), confirmField, confirmYes); w.Code != http.StatusForbidden {
		t.Errorf("from another device = %d, want 403", w.Code)
	}
	if o := f.order(stop.ID); o.State != orders.Accepted || fake.Count(brokertest.Cancel) != 1 {
		t.Fatalf("the stop is %s after %d cancels, want resting after one", o.State, fake.Count(brokertest.Cancel))
	}

	wantToday(t, f.postSwitch(switchStopsPath, token, c, fromAddr(phoneAddr), confirmField, confirmYes), "stops")
	if o, e := f.order(stop.ID), f.lastEvent(stop.ID); o.State != orders.Cancelled || e.Actor != orders.StopSwitch {
		t.Errorf("the stop is %s by %s, want cancelled by the stop switch", o.State, e.Actor)
	}
	if fake.Count(brokertest.Cancel) != 2 {
		t.Errorf("%d cancels, want 2", fake.Count(brokertest.Cancel))
	}
	if st := f.switchState(); !st.Has(halt.StopsCancelled) || !st.Has(halt.Operator) {
		t.Errorf("halts = %+v, want the press and the stops confirmation", st.Active)
	}
	wantIn(t, f.page("/"), "You cancelled the protective stops", "Your holdings have no stop orders.")
}

func TestTheStopsCannotBeCancelledWhileTheSwitchIsOff(t *testing.T) {
	f, fake := newTradingFixture(t)
	stop := f.propose(signals.KindStopSell, "CCC", "5", "9.00", orders.Submitted, orders.Accepted)
	c, token := f.session()
	wantToday(t, f.postSwitch(switchStopsPath, token, c, fromAddr(phoneAddr), confirmField, confirmYes), "stops_off")
	if o := f.order(stop.ID); o.State != orders.Accepted || len(fake.Calls()) != 0 || f.switchState().On() {
		t.Errorf("the stop is %s after calls %+v; want nothing changed", o.State, fake.Calls())
	}
	wantNotIn(t, f.page("/"), "Cancel protective stops too")
}

func TestWithoutABrokerTheSwitchStillDeclinesWaitingBuys(t *testing.T) {
	f := newFixtureMode(t, config.ModeLive)
	waiting := f.propose(signals.KindBuy, "AAA", "10", "12.00")
	open := f.propose(signals.KindBuy, "BBB", "2", "20.00", orders.Approved, orders.Submitted, orders.Accepted)
	c, token := f.session()
	wantToday(t, f.postSwitch(switchOnPath, token, c, fromAddr(phoneAddr)), "on_nobroker")
	if o := f.order(waiting.ID); o.State != orders.Declined {
		t.Errorf("the waiting buy is %s, want declined", o.State)
	}
	if o := f.order(open.ID); o.State != orders.Accepted {
		t.Errorf("the open buy is %s, want left for you to cancel at the broker", o.State)
	}
	wantIn(t, f.page("/?switch=on_nobroker"), "cancel them in the broker&#39;s own app")
}

func (f *fixture) tighten(name limits.Name, value string, from string) *httptest.ResponseRecorder {
	f.t.Helper()
	c, token := f.session()
	return f.postSwitch(tightenPath, token, c, fromAddr(from), limitField, string(name), valueField, value)
}

func (f *fixture) phoneChanges() int {
	f.t.Helper()
	var n int
	if err := f.store.Reader().QueryRow(`SELECT count(*) FROM settings_changes WHERE source = 'phone'`).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestALooseningFromTheAppIsRefusedAndATighteningIsRecordedAndEnforced(t *testing.T) {
	f, _ := newTradingFixture(t)
	wantLimit := func(w *httptest.ResponseRecorder, result string) {
		t.Helper()
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings?limit="+result+"#limits" {
			t.Fatalf("answer = %d to %q, want 303 to the %s result", w.Code, w.Header().Get("Location"), result)
		}
	}
	wantLimit(f.tighten(limits.OrdersPerDay, "5", phoneAddr), "looser")
	wantLimit(f.tighten(limits.OrdersPerDay, "3", phoneAddr), "same")
	wantLimit(f.tighten(limits.OrdersPerDay, "lots", phoneAddr), "invalid")
	wantLimit(f.tighten("limits.min_paper_weeks", "8", phoneAddr), "unknown")
	if w := f.tighten(limits.OrdersPerDay, "2", laptopAddr); w.Code != http.StatusForbidden {
		t.Errorf("from another device = %d, want 403", w.Code)
	}
	if n := f.phoneChanges(); n != 0 {
		t.Fatalf("%d tightenings recorded, want none", n)
	}
	wantIn(t, f.page("/settings?limit=looser"), "A loosening is made by editing the strategy file on the NAS")

	wantLimit(f.tighten(limits.OrdersPerDay, "2", phoneAddr), "tightened")
	if n := f.phoneChanges(); n != 1 {
		t.Fatalf("%d tightenings recorded, want 1", n)
	}
	body := f.page("/settings?limit=tightened")
	wantIn(t, body, "The limit is tightened.", "Most orders sent in one trading day", "In force: 2, tightened",
		"Strategy file: 3", "Most orders sent in one trading day: 3 to 2")

	// The approval's re-check reads the tightened limit at once.
	o := f.propose(signals.KindBuy, "AAA", "10", "12.00")
	wantApprovals(t, f.act(approvePath, o.ID, fromAddr(phoneAddr)), "approved")
	if _, snap := f.checker.last(t); snap.Limits.MaxOrdersPerDay != 2 || snap.Limits.MaxOrderValue.String() != "100.00" {
		t.Errorf("the approval checked against orders per day %d and order value %s, want 2 and the file's 100.00",
			snap.Limits.MaxOrdersPerDay, snap.Limits.MaxOrderValue)
	}
}

func TestSettingsShowsTheLimitsTheModeAndThePaperRecord(t *testing.T) {
	f, _ := newTradingFixture(t)
	body := f.page("/settings")
	wantIn(t, body, "Paper trading: no real money moves.", "Largest share of the portfolio in one stock",
		"In force: 10% (0.10)", "Largest value of one order", "In force: $100.00", "Set only in the strategy file",
		"Fractional shares", "No paper record yet.")
	wantNotIn(t, body, "Not built yet.")

	ctx := context.Background()
	if _, _, err := holdings.Record(ctx, f.store, holdings.Snapshot{Source: holdings.SourceImport,
		AsOf: f.clock.Now(), Cash: money.MustParseMoney("1000.00")}); err != nil {
		t.Fatal(err)
	}
	f.propose(signals.KindBuy, "AAA", "10", "12.00")
	wantIn(t, f.page("/settings"), "Began", "$1,000.00", "$0.00, no change", "Orders waiting for approval")
}
