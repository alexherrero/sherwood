package scheduler

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
)

func (rec *recorder) every(name string, d time.Duration) Job {
	j := rec.job(name, 0, 0)
	j.Every = d
	return j
}

// slots lists the runs so far as "job date HH:MM", the slot each was for.
func (rec *recorder) slots() []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var s []string
	for _, r := range rec.runs {
		s = append(s, r.Job+" "+r.Date()+" "+r.Scheduled.In(clock.Eastern).Format("15:04"))
	}
	return s
}

func wantSlots(t *testing.T, rec *recorder, want ...string) {
	t.Helper()
	if got := rec.slots(); !slices.Equal(got, want) {
		t.Fatalf("slots:\n got %q\nwant %q", got, want)
	}
}

// The interval job runs every slot from the open until the close and never
// outside market hours: not before the open, not after the close, not on a
// holiday or a weekend, and only to the early close on a half day.
func TestIntervalJobRunsOnlyInMarketHours(t *testing.T) {
	st := openStore(t)
	// The Wednesday before Thanksgiving, mid-afternoon: the 14:45 slot is
	// current, so it runs at once.
	clk := clock.NewFake(et(2026, 11, 25, 14, 50))
	rec := &recorder{}
	r := newRig(t, st, clk, Config{CatchUpDays: 1}, rec.every("checks", 15*time.Minute))
	waits, stop := loop(t, r.s)

	var want []string
	for _, at := range []string{"14:45", "15:00", "15:15", "15:30", "15:45"} {
		want = append(want, "checks 2026-11-25 "+at)
	}
	// Thursday is Thanksgiving; Friday closes at 13:00.
	for h := 9; h <= 12; h++ {
		for m := 0; m < 60; m += 15 {
			if h == 9 && m < 30 {
				continue
			}
			want = append(want, "checks 2026-11-27 "+time.Date(0, 1, 1, h, m, 0, 0, time.UTC).Format("15:04"))
		}
	}
	// The weekend is skipped; Monday opens at 09:30.
	want = append(want, "checks 2026-11-30 09:30")

	next := <-waits
	if want := et(2026, 11, 25, 15, 0); !next.Equal(want) {
		t.Fatalf("first wake %v, want %v", next, want)
	}
	for len(rec.slots()) < len(want) {
		clk.Set(next)
		next = <-waits
	}
	wantSlots(t, rec, want...)
	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}

	cal := clock.NYSE()
	for _, run := range rec.runs {
		ses, err := cal.Session(run.TradingDate)
		if err != nil {
			t.Fatal(err)
		}
		if run.Scheduled.Before(ses.Open) || !run.Scheduled.Before(ses.Close) {
			t.Errorf("slot %v is outside %v to %v", run.Scheduled, ses.Open, ses.Close)
		}
		if run.CatchUp {
			t.Errorf("slot %v is marked as a catch-up", run.Scheduled)
		}
	}
	// One row per date, holding that date's latest slot.
	got := rows(t, st)
	if len(got) != 3 {
		t.Fatalf("job_runs has %d rows, want one per date: %+v", len(got), got)
	}
	if got[0].date != "2026-11-25" || got[0].outcome != "succeeded" || got[0].started < "2026-11-25T20:45" {
		t.Errorf("Wednesday's row %+v, want the 15:45 slot, succeeded", got[0])
	}
}

// A slot that passed while the scheduler could not run it is never made up:
// the current slot runs, late, and the earlier ones are dropped. A restart
// within a slot that has run does not run it again.
func TestIntervalJobMakesUpNoMissedSlot(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	// Started at 10:07: the 10:00 slot is current, so it runs, late.
	clk := clock.NewFake(et(2026, 11, 24, 10, 7))
	rec := &recorder{}
	r := newRig(t, st, clk, Config{CatchUpDays: 1}, rec.every("checks", 15*time.Minute))
	if err := r.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	wantSlots(t, rec, "checks 2026-11-24 10:00")
	if !rec.last().Scheduled.Equal(et(2026, 11, 24, 10, 0)) {
		t.Errorf("scheduled %v", rec.last().Scheduled)
	}
	// Again within the same slot: nothing.
	clk.Set(et(2026, 11, 24, 10, 14))
	if err := r.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	wantSlots(t, rec, "checks 2026-11-24 10:00")

	// The container was down from 10:14 to 11:40: only 11:30 runs.
	clk.Set(et(2026, 11, 24, 11, 40))
	again := newRig(t, st, clk, Config{CatchUpDays: 1}, rec.every("checks", 15*time.Minute))
	if err := again.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	wantSlots(t, rec, "checks 2026-11-24 10:00", "checks 2026-11-24 11:30")

	// A restart within the slot that has run does not run it again.
	clk.Set(et(2026, 11, 24, 11, 41))
	third := newRig(t, st, clk, Config{CatchUpDays: 1}, rec.every("checks", 15*time.Minute))
	if err := third.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	// After the close, and before the next open, nothing runs, and the
	// day's missed slots are not made up the next morning either.
	for _, at := range []time.Time{et(2026, 11, 24, 16, 0), et(2026, 11, 25, 9, 29)} {
		clk.Set(at)
		if err := third.s.RunDue(ctx); err != nil {
			t.Fatal(err)
		}
	}
	wantSlots(t, rec, "checks 2026-11-24 10:00", "checks 2026-11-24 11:30")
	clk.Set(et(2026, 11, 25, 9, 30))
	if err := third.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	wantSlots(t, rec, "checks 2026-11-24 10:00", "checks 2026-11-24 11:30", "checks 2026-11-25 09:30")
}

// A failed slot is not tried again within its slot (no tight loop), and the
// next slot runs as usual.
func TestIntervalJobFailedSlotIsNotRetried(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	clk := clock.NewFake(et(2026, 11, 24, 10, 0))
	rec := &recorder{err: func(Run) error { return errors.New("the broker did not answer") }}
	r := newRig(t, st, clk, Config{CatchUpDays: 1}, rec.every("checks", 15*time.Minute))
	for range 3 {
		if err := r.s.RunDue(ctx); err != nil {
			t.Fatal(err)
		}
	}
	wantSlots(t, rec, "checks 2026-11-24 10:00")
	if got := rows(t, st); len(got) != 1 || got[0].outcome != "failed" || got[0].detail != "the broker did not answer" {
		t.Fatalf("rows %+v", got)
	}
	rec.err = nil
	clk.Set(et(2026, 11, 24, 10, 15))
	if err := r.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	wantSlots(t, rec, "checks 2026-11-24 10:00", "checks 2026-11-24 10:15")
	if got := rows(t, st); len(got) != 1 || got[0].outcome != "succeeded" || got[0].finished == "" {
		t.Fatalf("rows %+v, want the one row rewritten as succeeded", got)
	}
}

// Failure alerts are grouped as for daily jobs: one per alert group and date,
// however many slots fail, a success in between included, and across a
// restart while the latest slot has failed. The next date alerts again.
func TestIntervalJobAlertsOncePerDate(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	alerts := &notifytest.Fake{}
	fail := true
	rec := &recorder{err: func(Run) error {
		if fail {
			return errors.New("the broker did not answer")
		}
		return nil
	}}
	jobs := []Job{rec.every("open-orders", 15*time.Minute), rec.every("daily-loss", 15*time.Minute)}
	for i := range jobs {
		jobs[i].AlertGroup = "checks"
	}
	clk := clock.NewFake(et(2026, 11, 24, 10, 0))
	r := newRig(t, st, clk, Config{CatchUpDays: 1, Alerts: alerts}, jobs...)
	step := func(s *Scheduler, at time.Time) {
		t.Helper()
		clk.Set(at)
		if err := s.RunDue(ctx); err != nil {
			t.Fatal(err)
		}
	}
	step(r.s, et(2026, 11, 24, 10, 0))
	step(r.s, et(2026, 11, 24, 10, 15))
	wantAlerts(t, alerts, "job_failed")

	// A slot that succeeds, then one that fails again: still one alert.
	fail = false
	step(r.s, et(2026, 11, 24, 10, 30))
	fail = true
	step(r.s, et(2026, 11, 24, 10, 45))
	wantAlerts(t, alerts, "job_failed")

	// A restart while the latest slot has failed: no second alert.
	again := newRig(t, st, clk, Config{CatchUpDays: 1, Alerts: alerts}, jobs...)
	step(again.s, et(2026, 11, 24, 11, 0))
	wantAlerts(t, alerts, "job_failed")
	if n := len(rec.slots()); n != 10 {
		t.Fatalf("%d runs, want 10 (two jobs, five slots)", n)
	}

	// The next date is a new failure.
	step(again.s, et(2026, 11, 25, 9, 30))
	wantAlerts(t, alerts, "job_failed", "job_failed")
}

func TestRegisterRefusesBadIntervals(t *testing.T) {
	run := func(context.Context, Run) error { return nil }
	r := newRig(t, openStore(t), clock.NewFake(et(2026, 11, 16, 8, 0)), Config{})
	for name, j := range map[string]Job{
		"negative":       {Name: "x", Every: -time.Minute, Run: run},
		"seconds":        {Name: "x", Every: 90 * time.Second, Run: run},
		"under a minute": {Name: "x", Every: time.Second, Run: run},
		"with a time":    {Name: "x", Every: 15 * time.Minute, Hour: 9, Run: run},
		"with a minute":  {Name: "x", Every: 15 * time.Minute, Minute: 30, Run: run},
		"every day":      {Name: "x", Every: 15 * time.Minute, EveryDay: true, Run: run},
	} {
		if err := r.s.Register(j); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := r.s.Register(Job{Name: "checks", Every: 15 * time.Minute, Run: run}); err != nil {
		t.Errorf("a 15-minute job was refused: %v", err)
	}
}

// Interval and daily jobs share the loop: a daily job's time and the next
// slot are both woken for, in order.
func TestIntervalAndDailyJobsShareTheLoop(t *testing.T) {
	st := openStore(t)
	clk := clock.NewFake(et(2026, 11, 24, 9, 20))
	rec := &recorder{}
	r := newRig(t, st, clk, Config{CatchUpDays: 1},
		rec.every("checks", 30*time.Minute), rec.job("send-orders", 9, 35))
	waits, stop := loop(t, r.s)
	var woke []time.Time
	next := <-waits
	for range 3 {
		woke = append(woke, next)
		clk.Set(next)
		next = <-waits
	}
	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	want := []time.Time{et(2026, 11, 24, 9, 30), et(2026, 11, 24, 9, 35), et(2026, 11, 24, 10, 0)}
	if !slices.EqualFunc(woke, want, time.Time.Equal) {
		t.Fatalf("woke at %v, want %v", woke, want)
	}
	wantSlots(t, rec, "checks 2026-11-24 09:30", "send-orders 2026-11-24 09:35", "checks 2026-11-24 10:00")
}
