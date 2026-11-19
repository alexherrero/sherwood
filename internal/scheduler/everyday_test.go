package scheduler

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
)

func everyDay(j Job) Job {
	j.EveryDay = true
	return j
}

// alertNames lists the alerts sent so far by kind.
func alertNames(f *notifytest.Fake) []string {
	var out []string
	for _, a := range f.Sent() {
		out = append(out, a.Name())
	}
	return out
}

func wantAlerts(t *testing.T, f *notifytest.Fake, want ...string) {
	t.Helper()
	if got := alertNames(f); !slices.Equal(got, want) {
		t.Fatalf("alerts:\n got %q\nwant %q", got, want)
	}
}

func TestEveryDayJobRunsOnWeekendsAndHolidays(t *testing.T) {
	st := openStore(t)
	// Saturday before Thanksgiving week, after the backup's time.
	clk := clock.NewFake(et(2026, 11, 21, 3, 0))
	rec := &recorder{}
	r := newRig(t, st, clk, Config{CatchUpDays: 1},
		everyDay(rec.job("backup", 2, 0)), rec.job("reconcile", 9, 0))
	waits, stop := loop(t, r.s)

	// On start it makes up Saturday's backup (only the latest, never
	// Friday's) and Friday's reconcile.
	next := <-waits
	wantRuns(t, rec, "reconcile 2026-11-20", "backup 2026-11-21")
	sat := rec.runs[1]
	if sat.CatchUp || !sat.Session.Date.Equal(et(2026, 11, 21, 0, 0)) || !sat.Session.Open.IsZero() {
		t.Errorf("Saturday's run = %+v, want an on-time run whose session holds only the date", sat)
	}

	steps := []struct {
		wake time.Time
		run  string
	}{
		{et(2026, 11, 22, 2, 0), "backup 2026-11-22"},    // Sunday
		{et(2026, 11, 23, 2, 0), "backup 2026-11-23"},    // Monday
		{et(2026, 11, 23, 9, 0), "reconcile 2026-11-23"}, // Monday's trading job
		{et(2026, 11, 24, 2, 0), "backup 2026-11-24"},    // Tuesday
		{et(2026, 11, 24, 9, 0), "reconcile 2026-11-24"}, //
		{et(2026, 11, 25, 2, 0), "backup 2026-11-25"},    // Wednesday
		{et(2026, 11, 25, 9, 0), "reconcile 2026-11-25"}, //
		{et(2026, 11, 26, 2, 0), "backup 2026-11-26"},    // Thanksgiving: no reconcile
		{et(2026, 11, 27, 2, 0), "backup 2026-11-27"},    // Friday
	}
	for _, step := range steps {
		if !next.Equal(step.wake) {
			t.Fatalf("scheduler waits for %v, want %v", next, step.wake)
		}
		clk.Set(next)
		next = <-waits
		if got := rec.got(); got[len(got)-1] != step.run {
			t.Fatalf("last run is %q, want %q", got[len(got)-1], step.run)
		}
	}
	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
}

func TestEveryDayJobMakesUpOnlyItsLatestRun(t *testing.T) {
	st := openStore(t)
	rec := &recorder{}
	job := everyDay(rec.job("backup", 2, 0))
	// Before 02:00 on Wednesday, the latest due run is Tuesday's.
	clk := clock.NewFake(et(2026, 11, 18, 1, 0))
	r := newRig(t, st, clk, Config{}, job)
	if err := r.s.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantRuns(t, rec, "backup 2026-11-17")
	if !rec.last().CatchUp {
		t.Error("yesterday's run is not marked a catch-up")
	}
}

func TestFailedJobAlertsOnceAcrossRetriesAndRestarts(t *testing.T) {
	st := openStore(t)
	alerts := &notifytest.Fake{}
	rec := &recorder{err: func(Run) error { return errors.New("the source timed out") }}
	jobs := []Job{rec.job("after-close", 16, 30), rec.job("after-close-retry-1", 18, 0)}
	for i := range jobs {
		jobs[i].AlertGroup = "after-close"
	}
	ctx := context.Background()

	clk := clock.NewFake(et(2026, 11, 16, 16, 30))
	r := newRig(t, st, clk, Config{CatchUpDays: 1, Alerts: alerts}, jobs...)
	if err := r.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	wantAlerts(t, alerts, "job_failed")

	// The retry in the same group fails too: no second alert.
	clk.Set(et(2026, 11, 16, 18, 0))
	if err := r.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	// A restart retries both failed runs: still no second alert.
	clk.Advance(time.Minute)
	again := newRig(t, st, clk, Config{CatchUpDays: 1, Alerts: alerts}, jobs...)
	if err := again.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	wantRuns(t, rec, "after-close 2026-11-16", "after-close-retry-1 2026-11-16",
		"after-close 2026-11-16", "after-close-retry-1 2026-11-16")
	wantAlerts(t, alerts, "job_failed")

	// The next date's failure is a new failure, and alerts once more.
	clk.Set(et(2026, 11, 17, 16, 30))
	if err := again.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	wantAlerts(t, alerts, "job_failed", "job_failed")
}

func TestJobsAlertSeparatelyAndCanNameTheirOwnAlert(t *testing.T) {
	st := openStore(t)
	alerts := &notifytest.Fake{Err: errors.New("push service down")}
	fail := func(Run) error { return errors.New("no") }
	backup := &recorder{err: fail}
	other := &recorder{err: fail}
	b := everyDay(backup.job("backup", 2, 0))
	b.Alert = notify.BackupFailed()
	clk := clock.NewFake(et(2026, 11, 17, 9, 30))
	r := newRig(t, st, clk, Config{CatchUpDays: 1, Alerts: alerts}, b, other.job("reconcile", 9, 0))
	if err := r.s.RunDue(context.Background()); err != nil {
		t.Fatalf("a failed alert stopped the scheduler: %v", err)
	}
	wantAlerts(t, alerts, "backup_failed", "job_failed")
	if !strings.Contains(r.out.String(), `"msg":"alert not sent"`) {
		t.Errorf("a failed alert was not logged:\n%s", r.out.String())
	}
}

func TestRegisterRefusesAnAlertThatIsNotValid(t *testing.T) {
	st := openStore(t)
	r := newRig(t, st, clock.NewFake(et(2026, 11, 17, 9, 0)), Config{})
	j := (&recorder{}).job("x", 1, 0)
	j.Alert = notify.BuysWaiting(0)
	if err := r.s.Register(j); err == nil {
		t.Fatal("a job with an invalid alert was registered")
	}
	j.Alert = notify.Alert{}
	if err := r.s.Register(j); err != nil {
		t.Fatalf("the zero alert (the default) was refused: %v", err)
	}
}

func TestMissedRunsAlertOncePerPassButNotOnFirstStart(t *testing.T) {
	st := openStore(t)
	alerts := &notifytest.Fake{}
	rec := &recorder{}
	jobs := []Job{rec.job("reconcile", 9, 0), rec.job("after-close", 16, 30)}
	ctx := context.Background()

	// A brand-new database replays the window without calling it missed.
	clk := clock.NewFake(et(2026, 11, 16, 17, 0))
	r := newRig(t, st, clk, Config{CatchUpDays: 2, Alerts: alerts}, jobs...)
	if err := r.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	wantAlerts(t, alerts)

	// Down from Monday evening to Wednesday noon: one alert for the pass.
	clk.Set(et(2026, 11, 18, 12, 0))
	again := newRig(t, st, clk, Config{CatchUpDays: 3, Alerts: alerts}, jobs...)
	if err := again.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	wantAlerts(t, alerts, "job_missed")
	if !strings.Contains(again.out.String(), `"missed_runs":3`) {
		t.Errorf("no missed-runs warning:\n%s", again.out.String())
	}

	// A run started within a few minutes of its time is on time.
	clk.Set(et(2026, 11, 18, 16, 40))
	if err := again.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	wantAlerts(t, alerts, "job_missed")
}
