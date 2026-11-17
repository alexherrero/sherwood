package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/store"
)

// et builds a time in US Eastern.
func et(y int, m time.Month, d, hour, minute int) time.Time {
	return time.Date(y, m, d, hour, minute, 0, 0, clock.Eastern)
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sherwood.db"), et(2026, 11, 1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

type rig struct {
	s   *Scheduler
	out *loggingtest.Output
}

// newRig builds a scheduler on st, the fake clock and the NYSE calendar
// (unless cfg names another), with jobs registered in order.
func newRig(t *testing.T, st *store.Store, clk clock.Clock, cfg Config, jobs ...Job) rig {
	t.Helper()
	logger, out := loggingtest.New(t)
	cfg.Store, cfg.Clock, cfg.Logger = st, clk, logger
	if cfg.Calendar == nil {
		cfg.Calendar = clock.NYSE()
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if err := s.Register(j); err != nil {
			t.Fatal(err)
		}
	}
	return rig{s, out}
}

// recorder is a job's run function that remembers every run it is given.
type recorder struct {
	mu   sync.Mutex
	runs []Run
	err  func(Run) error
}

func (rec *recorder) job(name string, hour, minute int) Job {
	return Job{Name: name, Hour: hour, Minute: minute, Run: func(_ context.Context, r Run) error {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.runs = append(rec.runs, r)
		if rec.err != nil {
			return rec.err(r)
		}
		return nil
	}}
}

// got lists the runs so far as "job date".
func (rec *recorder) got() []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var s []string
	for _, r := range rec.runs {
		s = append(s, r.Job+" "+r.Date())
	}
	return s
}

func (rec *recorder) last() Run {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.runs[len(rec.runs)-1]
}

type row struct {
	job, date, hash, started, finished, outcome, detail string
}

func rows(t *testing.T, st *store.Store) []row {
	t.Helper()
	q, err := st.Reader().Query(`SELECT job_name, trading_date, input_hash, started_at, coalesce(finished_at, ''), outcome, detail FROM job_runs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.Close() }()
	var out []row
	for q.Next() {
		var r row
		if err := q.Scan(&r.job, &r.date, &r.hash, &r.started, &r.finished, &r.outcome, &r.detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := q.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func wantRuns(t *testing.T, rec *recorder, want ...string) {
	t.Helper()
	if got := rec.got(); !slices.Equal(got, want) {
		t.Fatalf("runs:\n got %q\nwant %q", got, want)
	}
}

// loop starts s.Run and returns a channel of the times it says it will wake
// at and a function that stops it and returns Run's error. The loop
// blocks after each wake-up until the test reads the next wake time, so a
// test moves the clock only once the scheduler is waiting on it.
func loop(t *testing.T, s *Scheduler) (waits <-chan time.Time, stop func() error) {
	t.Helper()
	w := make(chan time.Time)
	s.onWait = func(next time.Time) { w <- next }
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- s.Run(ctx) }()
	return w, func() error {
		cancel()
		return <-errc
	}
}

func TestRunsOncePerTradingDaySkippingHolidaysAndWeekends(t *testing.T) {
	st := openStore(t)
	// Tuesday before Thanksgiving, before the job's time.
	clk := clock.NewFake(et(2026, 11, 24, 8, 0))
	rec := &recorder{}
	r := newRig(t, st, clk, Config{CatchUpDays: 1}, rec.job("reconcile", 9, 0))
	waits, stop := loop(t, r.s)

	steps := []struct {
		wake time.Time
		runs []string
	}{
		{et(2026, 11, 24, 9, 0), []string{"reconcile 2026-11-24"}},
		{et(2026, 11, 25, 9, 0), []string{"reconcile 2026-11-24", "reconcile 2026-11-25"}},
		// Thursday is Thanksgiving: the next run is Friday, a half day.
		{et(2026, 11, 27, 9, 0), []string{"reconcile 2026-11-24", "reconcile 2026-11-25", "reconcile 2026-11-27"}},
		// Then the weekend is skipped.
		{et(2026, 11, 30, 9, 0), []string{"reconcile 2026-11-24", "reconcile 2026-11-25", "reconcile 2026-11-27", "reconcile 2026-11-30"}},
	}
	next := <-waits
	for _, step := range steps {
		if !next.Equal(step.wake) {
			t.Fatalf("scheduler waits for %v, want %v", next, step.wake)
		}
		if clk.Pending() != 1 {
			t.Fatalf("scheduler has %d timers pending, want 1", clk.Pending())
		}
		clk.Set(next)
		// The loop asks to be woken again only once the run has finished.
		next = <-waits
		wantRuns(t, rec, step.runs...)
		last := rec.last()
		if last.CatchUp {
			t.Errorf("on-time run %s on %s is marked as a catch-up", last.Job, last.Date())
		}
		if !last.Session.Date.Equal(clock.DateOf(step.wake)) {
			t.Errorf("run's session is %v, want %v", last.Session.Date, clock.DateOf(step.wake))
		}
	}
	if !rec.runs[2].Session.HalfDay {
		t.Error("the day after Thanksgiving is not marked a half day")
	}
	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
	if got := len(rows(t, st)); got != 4 {
		t.Fatalf("job_runs has %d rows, want 4", got)
	}
}

func TestCatchesUpMissedDaysInOrderAfterRestart(t *testing.T) {
	st := openStore(t)
	rec := &recorder{}
	jobs := []Job{rec.job("reconcile", 9, 0), rec.job("after-close", 16, 30)}

	// Monday evening: both of Monday's runs happen, then the container stops.
	clk := clock.NewFake(et(2026, 11, 16, 17, 0))
	first := newRig(t, st, clk, Config{CatchUpDays: 1}, jobs...)
	if err := first.s.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantRuns(t, rec, "reconcile 2026-11-16", "after-close 2026-11-16")

	// It comes back at noon on Friday, with the default window.
	clk.Set(et(2026, 11, 20, 12, 0))
	second := newRig(t, st, clk, Config{}, jobs...)
	waits, stop := loop(t, second.s)
	if next := <-waits; !next.Equal(et(2026, 11, 20, 16, 30)) {
		t.Fatalf("after catching up, the scheduler waits for %v, want Friday 16:30", next)
	}
	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}

	wantRuns(t, rec,
		"reconcile 2026-11-16", "after-close 2026-11-16",
		"reconcile 2026-11-17", "after-close 2026-11-17",
		"reconcile 2026-11-18", "after-close 2026-11-18",
		"reconcile 2026-11-19", "after-close 2026-11-19",
		"reconcile 2026-11-20",
	)
	for _, r := range rec.runs[2:] {
		wantCatchUp := r.Date() != "2026-11-20"
		if r.CatchUp != wantCatchUp {
			t.Errorf("%s on %s: CatchUp = %v, want %v", r.Job, r.Date(), r.CatchUp, wantCatchUp)
		}
		// Each run is handed its own date's session, not today's.
		if got := store.FormatDate(r.Session.Date); got != r.Date() {
			t.Errorf("%s on %s was given the session of %s", r.Job, r.Date(), got)
		}
		if want := atTime(r.TradingDate, Job{Hour: 9}); r.Job == "reconcile" && !r.Scheduled.Equal(want) {
			t.Errorf("%s on %s: Scheduled = %v, want %v", r.Job, r.Date(), r.Scheduled, want)
		}
	}

	lines, err := second.out.Lines()
	if err != nil {
		t.Fatal(err)
	}
	caught := map[string]float64{}
	for _, l := range lines {
		if l["msg"] == "catching up missed runs" {
			caught[l["job"].(string)] = l["missed_dates"].(float64)
		}
	}
	if caught["reconcile"] != 3 || caught["after-close"] != 3 {
		t.Errorf("catch-up log lines = %v, want 3 missed dates for each job", caught)
	}
	if got := len(rows(t, st)); got != 9 {
		t.Errorf("job_runs has %d rows, want 9", got)
	}
}

func TestCatchUpWindowIsBounded(t *testing.T) {
	st := openStore(t)
	rec := &recorder{}
	// A fresh database on a Monday: the window of three trading dates is the
	// Thursday and Friday before and today. The earlier days are never run.
	clk := clock.NewFake(et(2026, 11, 23, 12, 0))
	r := newRig(t, st, clk, Config{CatchUpDays: 3}, rec.job("reconcile", 9, 0))
	if err := r.s.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantRuns(t, rec, "reconcile 2026-11-19", "reconcile 2026-11-20", "reconcile 2026-11-23")
}

func TestNeverRunsTwiceForTheSameDate(t *testing.T) {
	st := openStore(t)
	rec := &recorder{}
	job := rec.job("after-close", 16, 30)
	clk := clock.NewFake(et(2026, 11, 16, 16, 30))
	r := newRig(t, st, clk, Config{CatchUpDays: 1}, job)
	ctx := context.Background()

	for range 3 {
		if err := r.s.RunDue(ctx); err != nil {
			t.Fatal(err)
		}
		clk.Advance(time.Hour)
	}
	// A restart later the same evening does not run it again either.
	again := newRig(t, st, clk, Config{CatchUpDays: 1}, job)
	if err := again.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	wantRuns(t, rec, "after-close 2026-11-16")

	// Nor does a scheduler that never saw the run: claiming a finished row
	// changes nothing.
	ok, err := again.s.claim(ctx, rec.runs[0], clk.Now())
	if err != nil || ok {
		t.Fatalf("claim of a succeeded run = %v, %v; want false, nil", ok, err)
	}
	if got := rows(t, st); len(got) != 1 || got[0].outcome != "succeeded" {
		t.Fatalf("job_runs = %+v, want one succeeded row", got)
	}
}

func TestRecordsHashTimesAndOutcome(t *testing.T) {
	st := openStore(t)
	clk := clock.NewFake(et(2026, 11, 16, 9, 0))
	var given Run
	job := Job{
		Name: "signals", Hour: 9, Minute: 0,
		Inputs: func(_ context.Context, r Run) ([]byte, error) {
			return []byte("prices for " + r.Date()), nil
		},
		Run: func(_ context.Context, r Run) error {
			given = r
			clk.Advance(2 * time.Minute)
			return nil
		},
	}
	r := newRig(t, st, clk, Config{CatchUpDays: 1}, job)
	if err := r.s.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantHash := HashInputs("2026-11-16", []byte("prices for 2026-11-16"))
	if given.InputHash != wantHash {
		t.Errorf("run was given hash %q, want %q", given.InputHash, wantHash)
	}
	want := row{
		job: "signals", date: "2026-11-16", hash: wantHash,
		started:  "2026-11-16T14:00:00.000000000Z",
		finished: "2026-11-16T14:02:00.000000000Z",
		outcome:  "succeeded",
	}
	if got := rows(t, st); len(got) != 1 || got[0] != want {
		t.Fatalf("job_runs = %+v, want %+v", got, want)
	}
	if HashInputs("2026-11-17", []byte("prices for 2026-11-16")) == wantHash {
		t.Error("the input hash does not depend on the trading date")
	}
	if !strings.Contains(r.out.String(), `"outcome":"succeeded"`) {
		t.Errorf("no finished log line:\n%s", r.out.String())
	}
}

func TestFailedRunIsRetriedOnRestartInTheSameRow(t *testing.T) {
	st := openStore(t)
	rec := &recorder{err: func(Run) error { return errors.New("prices missing") }}
	job := rec.job("after-close", 16, 30)
	clk := clock.NewFake(et(2026, 11, 16, 17, 0))
	r := newRig(t, st, clk, Config{CatchUpDays: 1}, job)
	ctx := context.Background()
	if err := r.s.RunDue(ctx); err != nil {
		t.Fatalf("a job's failure stopped the scheduler: %v", err)
	}
	got := rows(t, st)
	if len(got) != 1 || got[0].outcome != "failed" || got[0].detail != "prices missing" {
		t.Fatalf("job_runs = %+v, want one failed row", got)
	}
	if !strings.Contains(r.out.String(), `"error":"prices missing"`) {
		t.Errorf("no failure log line:\n%s", r.out.String())
	}

	// The same process does not retry it.
	clk.Advance(time.Minute)
	if err := r.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	wantRuns(t, rec, "after-close 2026-11-16")

	// The next start does, and updates the row rather than adding one.
	rec.err = nil
	clk.Advance(time.Minute)
	again := newRig(t, st, clk, Config{CatchUpDays: 1}, job)
	if err := again.s.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	wantRuns(t, rec, "after-close 2026-11-16", "after-close 2026-11-16")
	got = rows(t, st)
	if len(got) != 1 || got[0].outcome != "succeeded" || got[0].detail != "" ||
		got[0].started != store.FormatTime(et(2026, 11, 16, 17, 2)) {
		t.Fatalf("job_runs = %+v, want the one row, now succeeded and restarted at 17:02", got)
	}
}

func TestSkippedRunIsFinal(t *testing.T) {
	st := openStore(t)
	rec := &recorder{err: func(r Run) error {
		if r.CatchUp {
			return Skip("approved for " + r.Date() + ", whose session has closed")
		}
		return nil
	}}
	job := rec.job("send-buys", 9, 35)
	clk := clock.NewFake(et(2026, 11, 17, 12, 0))
	r := newRig(t, st, clk, Config{CatchUpDays: 2}, job)
	if err := r.s.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := rows(t, st)
	if len(got) != 2 || got[0].outcome != "skipped" || got[1].outcome != "succeeded" ||
		got[0].detail != "approved for 2026-11-16, whose session has closed" {
		t.Fatalf("job_runs = %+v, want Monday skipped and Tuesday succeeded", got)
	}
	again := newRig(t, st, clk, Config{CatchUpDays: 2}, job)
	if err := again.s.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantRuns(t, rec, "send-buys 2026-11-16", "send-buys 2026-11-17")
}

func TestInputsFailureIsRecordedAsFailed(t *testing.T) {
	st := openStore(t)
	ran := false
	job := Job{
		Name: "signals", Hour: 16, Minute: 30,
		Inputs: func(context.Context, Run) ([]byte, error) { return nil, errors.New("no prices") },
		Run:    func(context.Context, Run) error { ran = true; return nil },
	}
	clk := clock.NewFake(et(2026, 11, 16, 17, 0))
	r := newRig(t, st, clk, Config{CatchUpDays: 1}, job)
	if err := r.s.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Error("the job ran without its inputs")
	}
	got := rows(t, st)
	if len(got) != 1 || got[0].outcome != "failed" || got[0].hash != InputsUnavailable || got[0].detail != "inputs: no prices" {
		t.Fatalf("job_runs = %+v", got)
	}
}

func TestPanicIsRecordedAndStopsTheScheduler(t *testing.T) {
	st := openStore(t)
	rec := &recorder{}
	jobs := []Job{
		{Name: "broken", Hour: 9, Minute: 0, Run: func(context.Context, Run) error { panic("bug") }},
		rec.job("after-close", 16, 30),
	}
	clk := clock.NewFake(et(2026, 11, 16, 17, 0))
	r := newRig(t, st, clk, Config{CatchUpDays: 1}, jobs...)
	if err := r.s.RunDue(context.Background()); !errors.Is(err, ErrJobPanicked) {
		t.Fatalf("RunDue returned %v, want ErrJobPanicked", err)
	}
	wantRuns(t, rec) // nothing after the panic ran
	got := rows(t, st)
	if len(got) != 1 || got[0].outcome != "failed" || got[0].detail != "the job panicked" {
		t.Fatalf("job_runs = %+v", got)
	}
}

func TestCancelledRunIsLeftFailedNotRunning(t *testing.T) {
	st := openStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &recorder{err: func(Run) error { cancel(); return ctx.Err() }}
	jobs := []Job{rec.job("reconcile", 9, 0), rec.job("after-close", 16, 30)}
	clk := clock.NewFake(et(2026, 11, 16, 17, 0))
	r := newRig(t, st, clk, Config{CatchUpDays: 1}, jobs...)
	if err := r.s.RunDue(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunDue returned %v, want context.Canceled", err)
	}
	wantRuns(t, rec, "reconcile 2026-11-16")
	if got := rows(t, st); len(got) != 1 || got[0].outcome != "failed" {
		t.Fatalf("job_runs = %+v, want one failed row", got)
	}
}

func TestUncoveredDateStopsTheScheduler(t *testing.T) {
	cal, err := clock.NewCalendar(2026, 2026, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("next run in an uncovered year", func(t *testing.T) {
		st := openStore(t)
		rec := &recorder{}
		clk := clock.NewFake(et(2026, 12, 31, 17, 0))
		r := newRig(t, st, clk, Config{Calendar: cal, CatchUpDays: 1}, rec.job("reconcile", 9, 0))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.s.onWait = func(next time.Time) {
			t.Errorf("scheduler would wait for %v in an uncovered year", next)
			cancel()
		}
		if err := r.s.Run(ctx); !errors.Is(err, clock.ErrNotCovered) {
			t.Fatalf("Run returned %v, want ErrNotCovered", err)
		}
		wantRuns(t, rec, "reconcile 2026-12-31")
	})

	t.Run("today in an uncovered year", func(t *testing.T) {
		st := openStore(t)
		rec := &recorder{}
		clk := clock.NewFake(et(2027, 1, 4, 12, 0))
		r := newRig(t, st, clk, Config{Calendar: cal, CatchUpDays: 1}, rec.job("reconcile", 9, 0))
		if err := r.s.RunDue(context.Background()); !errors.Is(err, clock.ErrNotCovered) {
			t.Fatalf("RunDue returned %v, want ErrNotCovered", err)
		}
		wantRuns(t, rec)
		if got := rows(t, st); len(got) != 0 {
			t.Fatalf("job_runs = %+v, want none", got)
		}
	})

	t.Run("catch-up window reaching an uncovered year", func(t *testing.T) {
		st := openStore(t)
		clk := clock.NewFake(et(2026, 1, 5, 12, 0))
		rec := &recorder{}
		r := newRig(t, st, clk, Config{Calendar: cal, CatchUpDays: 5}, rec.job("reconcile", 9, 0))
		if err := r.s.RunDue(context.Background()); !errors.Is(err, clock.ErrNotCovered) {
			t.Fatalf("RunDue returned %v, want ErrNotCovered", err)
		}
		wantRuns(t, rec)
	})
}

func TestEmptySchedulerWaitsForCancel(t *testing.T) {
	clk := clock.NewFake(et(2026, 11, 16, 8, 0))
	r := newRig(t, openStore(t), clk, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.s.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
	if clk.Pending() != 0 {
		t.Errorf("an empty scheduler set %d timers", clk.Pending())
	}
}

func TestRegisterRefusesBadJobs(t *testing.T) {
	run := func(context.Context, Run) error { return nil }
	r := newRig(t, openStore(t), clock.NewFake(et(2026, 11, 16, 8, 0)), Config{},
		Job{Name: "reconcile", Hour: 9, Run: run})
	for name, j := range map[string]Job{
		"no name":   {Hour: 9, Run: run},
		"no run":    {Name: "x", Hour: 9},
		"hour 24":   {Name: "x", Hour: 24, Run: run},
		"minute 60": {Name: "x", Hour: 9, Minute: 60, Run: run},
		"negative":  {Name: "x", Hour: -1, Run: run},
		"same name": {Name: "reconcile", Hour: 10, Run: run},
	} {
		if err := r.s.Register(j); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := r.s.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.s.Register(Job{Name: "late", Hour: 9, Run: run}); err == nil {
		t.Error("a job registered after start was accepted")
	}
}

func TestNewRefusesMissingParts(t *testing.T) {
	logger, _ := loggingtest.New(t)
	full := Config{Store: openStore(t), Clock: clock.NewFake(et(2026, 11, 16, 8, 0)), Calendar: clock.NYSE(), Logger: logger}
	for name, edit := range map[string]func(*Config){
		"store":    func(c *Config) { c.Store = nil },
		"clock":    func(c *Config) { c.Clock = nil },
		"calendar": func(c *Config) { c.Calendar = nil },
		"logger":   func(c *Config) { c.Logger = nil },
		"days":     func(c *Config) { c.CatchUpDays = -1 },
	} {
		c := full
		edit(&c)
		if _, err := New(c); err == nil {
			t.Errorf("missing %s: accepted", name)
		}
	}
	s, err := New(full)
	if err != nil {
		t.Fatal(err)
	}
	if s.catchUp != DefaultCatchUpDays {
		t.Errorf("catch-up days = %d, want %d", s.catchUp, DefaultCatchUpDays)
	}
}
