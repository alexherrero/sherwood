package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/broker/brokertest"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
	"github.com/alexherrero/sherwood/internal/scheduler"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every amount here is made up, and no broker is reached: the live adapter
// is a fake, or the Robinhood adapter over a helper that is never started.
// The example strategy file asks for 4 weeks of paper trading.

// gateCase is one start-up: the mode the settings file names, whether live
// mode was confirmed in the app, and how many weeks of paper trading came
// before live mode was recorded.
type gateCase struct {
	name       string
	mode       config.Mode
	confirmed  bool
	paperWeeks int
}

type gateHarness struct {
	store *store.Store
	clock *clock.Fake
	sched *scheduler.Scheduler
	sw    *halt.Switch
	built int // how many times the live adapter was built
	fake  *brokertest.Fake
	out   *loggingtest.Output
	tr    trading
}

// startWithGate records the case's history, as earlier starts and taps would
// have, then registers trading as a start at 09:00 ET on 2026-11-24 would.
func startWithGate(t *testing.T, c gateCase) *gateHarness {
	t.Helper()
	ctx := context.Background()
	now := at(t, "2026-11-24", 9, 0)
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sherwood.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	strategy := acStrategy(t)
	paperFrom := now.Add(-time.Duration(c.paperWeeks) * 7 * 24 * time.Hour)
	if _, _, err := holdings.Record(ctx, s, holdings.Snapshot{Source: holdings.SourceImport, AsOf: paperFrom,
		Cash: money.MustParseMoney("1000.00")}); err != nil {
		t.Fatal(err)
	}
	if _, err := limits.Record(ctx, s, strategy, paperFrom); err != nil {
		t.Fatal(err)
	}
	if _, err := limits.RecordMode(ctx, s, config.ModePaper, paperFrom); err != nil {
		t.Fatal(err)
	}
	if _, err := limits.RecordMode(ctx, s, config.ModeLive, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if c.confirmed {
		if _, err := limits.ConfirmLive(ctx, s, strategy, config.ModeLive, now.Add(-time.Hour)); err != nil {
			// A short paper record refuses the confirmation; record it as an
			// older version that had no such check would have.
			if _, err := s.Writer().ExecContext(ctx, `INSERT INTO settings_changes
				(changed_at, setting, new_value, source) VALUES (?, ?, ?, 'phone')`,
				store.FormatTime(now.Add(-time.Hour)), limits.LiveSetting, strategy.Hash); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The mode this start names, recorded as main does.
	if _, err := limits.RecordMode(ctx, s, c.mode, now); err != nil {
		t.Fatal(err)
	}

	log, out := loggingtest.New(t)
	h := &gateHarness{store: s, clock: clock.NewFake(now), out: out,
		fake: &brokertest.Fake{Acct: broker.Account{Cash: money.MustParseMoney("1000.00"), Value: money.MustParseMoney("1000.00")}}}
	h.sw, err = halt.New(halt.Config{Store: s, Clock: h.clock, Notifier: &notifytest.Fake{}, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	h.sched, err = scheduler.New(scheduler.Config{Store: s, Clock: h.clock, Calendar: clock.NYSE(), Logger: log, CatchUpDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	newLive := func() (liveAdapter, error) {
		h.built++
		return h.fake, nil
	}
	h.tr, err = registerTrading(ctx, env{clock: h.clock}, h.sched, c.mode, strategy, s, h.sw, log, newLive)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// ranAt runs whatever is due at hh:mm ET on 2026-11-24 and returns the names
// of the jobs that ran that day.
func (h *gateHarness) ranAt(t *testing.T, hour, minute int) []string {
	t.Helper()
	h.clock.Set(at(t, "2026-11-24", hour, minute))
	if err := h.sched.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	ah := &acHarness{store: h.store}
	return ah.strings(t, `SELECT DISTINCT job_name FROM job_runs WHERE trading_date = '2026-11-24' ORDER BY job_name`)
}

func TestWithoutAllThreeNoLiveAdapterIsBuiltAndNoOrderJobRuns(t *testing.T) {
	for _, c := range []gateCase{
		{name: "not confirmed", mode: config.ModeLive, confirmed: false, paperWeeks: 5},
		{name: "paper record too short", mode: config.ModeLive, confirmed: true, paperWeeks: 3},
		{name: "paper record just begun", mode: config.ModeLive, confirmed: true, paperWeeks: 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := startWithGate(t, c)
			if h.built != 0 || h.tr.exec != nil || h.tr.paper != nil || h.tr.live {
				t.Fatalf("built the adapter %d times; trading = %+v", h.built, h.tr)
			}
			if !h.tr.healthy(context.Background()) {
				t.Error("with no adapter the health read by the risk checks should be the paper default")
			}
			if ran := h.ranAt(t, 9, 46); len(ran) != 0 {
				t.Errorf("jobs ran: %q, want none", ran)
			}
			if !strings.Contains(h.out.String(), "orders are not sent") || !strings.Contains(h.out.String(), "live trading is off until all three hold") {
				t.Errorf("the log does not say why:\n%s", h.out.String())
			}
			if h.fake.Count(brokertest.Place)+h.fake.Count(brokertest.Account)+h.fake.Count(brokertest.OpenOrders) != 0 {
				t.Errorf("the broker was called: %v", h.fake.Calls())
			}
		})
	}
	// Paper mode, even after live mode was confirmed: the paper broker, and
	// no live adapter or 15-minute check.
	h := startWithGate(t, gateCase{mode: config.ModePaper, confirmed: true, paperWeeks: 5})
	if h.built != 0 || h.tr.paper == nil || h.tr.live {
		t.Fatalf("paper mode built the live adapter %d times; trading = %+v", h.built, h.tr)
	}
	ran := h.ranAt(t, 9, 46)
	if slices.Contains(ran, openOrdersCheckJob) || slices.Contains(ran, dailyLossCheckJob) {
		t.Errorf("paper mode ran %q", ran)
	}
	if h.fake.Count(brokertest.Account) != 0 {
		t.Errorf("paper mode called the live adapter: %v", h.fake.Calls())
	}
}

func TestWithAllThreeTheLiveAdapterAndItsJobsAreRegistered(t *testing.T) {
	h := startWithGate(t, gateCase{mode: config.ModeLive, confirmed: true, paperWeeks: 5})
	if h.built != 1 || h.tr.exec == nil || h.tr.paper != nil || !h.tr.live {
		t.Fatalf("built the adapter %d times; trading = %+v", h.built, h.tr)
	}
	// The risk checks read the adapter's health.
	ctx := context.Background()
	if !h.tr.healthy(ctx) {
		t.Error("healthy adapter reported unhealthy")
	}
	h.fake.SetHealthy(false)
	if h.tr.healthy(ctx) {
		t.Error("unhealthy adapter reported healthy")
	}
	h.fake.SetHealthy(true)
	if !strings.Contains(h.out.String(), "live trading is on") {
		t.Errorf("the log does not say live trading is on:\n%s", h.out.String())
	}
	got := h.ranAt(t, 9, 46)
	want := []string{dailyLossCheckJob, openOrdersCheckJob, reconcileJob, sendJob}
	if !slices.Equal(got, want) {
		t.Errorf("jobs that ran: %q, want %q", got, want)
	}
	if h.fake.Count(brokertest.OpenOrders) == 0 || h.fake.Count(brokertest.Account) == 0 {
		t.Errorf("the jobs did not reach the live adapter: %v", h.fake.Calls())
	}
}

func TestTheLiveAdapterIsTheRobinhoodAdapterOverTheSupervisor(t *testing.T) {
	ctx := context.Background()
	now := at(t, "2026-11-24", 9, 0)
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sherwood.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	log, _ := loggingtest.New(t)
	sock := filepath.Join(t.TempDir(), "broker.sock")
	// The helper is never started: building the adapter calls nothing.
	sup, err := newSupervisor(env{clock: clock.NewFake(now), stderr: &strings.Builder{}, helperCommand: []string{"false"}},
		options{brokerSocket: sock}, log, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	a, err := robinhoodAdapter(sup, &broker.LoginAlert{Notifier: &notifytest.Fake{}}, s, log)()
	if err != nil {
		t.Fatal(err)
	}
	if a.Healthy() {
		t.Error("the adapter is healthy before its helper has started")
	}
	if _, ok := a.(broker.CashReader); !ok {
		t.Error("the live adapter does not read cash on its own")
	}
	if liveCallTimeout < 120*time.Second {
		t.Errorf("the executor's call limit %s would cut the adapter's 120s holdings call short", liveCallTimeout)
	}
}
