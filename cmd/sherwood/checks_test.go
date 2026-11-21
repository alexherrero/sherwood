package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/broker/brokertest"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/execution"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
	"github.com/alexherrero/sherwood/internal/risk"
	"github.com/alexherrero/sherwood/internal/scheduler"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every amount here is made up. The example file's daily loss limit is
// 50.00, and the records hold 1000.00 cash and no stocks.

type checksHarness struct {
	store  *store.Store
	clock  *clock.Fake
	fake   *brokertest.Fake
	alerts *notifytest.Fake
	sched  *scheduler.Scheduler
}

// newChecks registers the 15-minute checks, through an executor over the
// fake broker, on a scheduler whose clock starts at start.
func newChecks(t *testing.T, start time.Time) *checksHarness {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sherwood.db"), start)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, _, err := holdings.Record(ctx, s, holdings.Snapshot{Source: holdings.SourceImport,
		AsOf: at(t, "2026-11-23", 16, 0), Cash: money.MustParseMoney("1000.00")}); err != nil {
		t.Fatal(err)
	}
	log, _ := loggingtest.New(t)
	h := &checksHarness{store: s, clock: clock.NewFake(start), alerts: &notifytest.Fake{},
		fake: &brokertest.Fake{Acct: broker.Account{Cash: money.MustParseMoney("1000.00"), Value: money.MustParseMoney("1000.00")}}}
	sw, err := halt.New(halt.Config{Store: s, Clock: h.clock, Notifier: h.alerts, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	ex, err := execution.New(execution.Config{Store: s, Clock: h.clock, Calendar: clock.NYSE(), Adapter: h.fake,
		Checker: risk.Standard{}, Switch: sw, Strategy: acStrategy(t), Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	h.sched, err = scheduler.New(scheduler.Config{Store: s, Clock: h.clock, Calendar: clock.NYSE(), Logger: log,
		Alerts: h.alerts, CatchUpDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := registerChecks(h.sched, ex); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *checksHarness) runAt(t *testing.T, when time.Time) {
	t.Helper()
	h.clock.Set(when)
	if err := h.sched.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (h *checksHarness) halts(t *testing.T) halt.State {
	t.Helper()
	st, err := halt.Active(context.Background(), h.store.Reader())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (h *checksHarness) sent(a notify.Alert) int {
	n := 0
	for _, s := range h.alerts.Sent() {
		if s == a {
			n++
		}
	}
	return n
}

func TestTheChecksRunEvery15MinutesInMarketHoursOnly(t *testing.T) {
	h := newChecks(t, at(t, "2026-11-24", 8, 0))
	for _, when := range []time.Time{
		at(t, "2026-11-24", 9, 0),   // before the open
		at(t, "2026-11-24", 9, 30),  // slot
		at(t, "2026-11-24", 9, 44),  // same slot
		at(t, "2026-11-24", 9, 45),  // slot
		at(t, "2026-11-24", 15, 50), // the last slot, 15:45, still current
		at(t, "2026-11-24", 16, 0),  // the close
		at(t, "2026-11-24", 20, 0),  // evening
		at(t, "2026-11-26", 10, 0),  // Thanksgiving
	} {
		h.runAt(t, when)
	}
	for _, m := range []brokertest.Method{brokertest.Account, brokertest.OpenOrders} {
		if n := h.fake.Count(m); n != 3 {
			t.Errorf("%d %s calls, want 3 (09:30, 09:45 and 15:45)", n, m)
		}
	}
	if h.fake.Count(brokertest.Place) != 0 || h.fake.Count(brokertest.Cancel) != 0 {
		t.Errorf("a check placed or cancelled: %+v", h.fake.Calls())
	}
	h2 := &acHarness{store: h.store}
	got := h2.strings(t, `SELECT job_name || ' ' || trading_date || ' ' || outcome FROM job_runs ORDER BY job_name`)
	want := []string{"daily-loss-check 2026-11-24 succeeded", "open-orders-check 2026-11-24 succeeded"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("job runs:\n got %q\nwant %q", got, want)
	}
	if h.halts(t).On() || len(h.alerts.Sent()) != 0 {
		t.Errorf("halts %+v, alerts %v; want none", h.halts(t).Active, h.alerts.Sent())
	}
}

func TestALossPastTheLimitHaltsTradingFromTheCheckJob(t *testing.T) {
	h := newChecks(t, at(t, "2026-11-24", 9, 0))
	h.fake.Acct.Value = money.MustParseMoney("960.00") // down 40.00
	h.runAt(t, at(t, "2026-11-24", 10, 0))
	if h.halts(t).On() {
		t.Fatalf("halted under the limit: %+v", h.halts(t).Active)
	}
	h.fake.Acct.Value = money.MustParseMoney("940.00") // down 60.00
	h.runAt(t, at(t, "2026-11-24", 10, 15))
	h.fake.Acct.Value = money.MustParseMoney("900.00")
	h.runAt(t, at(t, "2026-11-24", 10, 30))
	st := h.halts(t)
	if len(st.Active) != 1 || st.Active[0].Cause != halt.DailyLoss ||
		!strings.Contains(st.Active[0].Evidence, "down $60.00 from $1000.00 at the close of 2026-11-23") {
		t.Fatalf("halts %+v, want one daily_loss halt with the figures", st.Active)
	}
	if n := h.sent(notify.TradingHalted()); n != 1 {
		t.Errorf("%d halt pushes, want 1", n)
	}
}

// An expired broker session fails every check until you log in again. It
// halts trading as a broker error once, with one halt push and one job
// failure push for the day, however many slots fail.
func TestAnExpiredSessionHaltsOnceThroughTheCheckJobs(t *testing.T) {
	h := newChecks(t, at(t, "2026-11-24", 9, 0))
	expired := &broker.HelperError{Op: "account", Code: broker.CodeLoginNeeded, Message: "made-up: no usable session"}
	for range 4 {
		h.fake.Fail(brokertest.Account, expired)
		h.fake.Fail(brokertest.OpenOrders, expired)
	}
	for i := range 4 {
		h.runAt(t, at(t, "2026-11-24", 10, 15*i))
	}
	st := h.halts(t)
	if len(st.Active) != 1 || st.Active[0].Cause != halt.BrokerError ||
		!strings.HasPrefix(st.Active[0].Evidence, execution.SessionExpired) {
		t.Fatalf("halts %+v, want one broker_error halt saying the session expired", st.Active)
	}
	if n := h.sent(notify.TradingHalted()); n != 1 {
		t.Errorf("%d halt pushes, want 1", n)
	}
	if n := h.sent(notify.JobFailed()); n != 1 {
		t.Errorf("%d job failure pushes, want 1", n)
	}
	if n := h.fake.Count(brokertest.Account) + h.fake.Count(brokertest.OpenOrders); n != 8 {
		t.Errorf("%d broker calls, want 8: one per check per slot, none retried", n)
	}
}
