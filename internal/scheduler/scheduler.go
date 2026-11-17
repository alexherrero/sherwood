package scheduler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/store"
)

// DefaultCatchUpDays is how many trading dates, counting today, the scheduler
// looks back over for runs it missed, unless Config says otherwise. Five is a
// trading week: enough to cover a long weekend or a few days of the NAS being
// off, and short enough that a fresh database does not replay history.
const DefaultCatchUpDays = 5

// maxWait caps how long the loop sleeps before it looks at the clock again,
// so a stepped or suspended system clock costs at most this much lateness.
const maxWait = time.Hour

// maxDetail caps the text kept in job_runs.detail.
const maxDetail = 500

// InputsUnavailable is the input hash recorded when a job's Inputs function
// fails, so the run can still be recorded as failed.
const InputsUnavailable = "inputs-unavailable"

// ErrJobPanicked means a job panicked. The run is recorded as failed and the
// scheduler stops, because a panic is a bug and sherwood fails closed.
var ErrJobPanicked = errors.New("scheduler: job panicked")

// Job is a daily job. It runs once per trading date, at Hour:Minute US
// Eastern time on that date.
type Job struct {
	// Name keys the job's runs in job_runs. It must be unique and must not
	// change once the job has run, or its history is lost.
	Name string
	// Hour and Minute are the time of day in US Eastern time.
	Hour, Minute int
	// Inputs, when set, returns everything the run will read, in a stable
	// encoding. The scheduler hashes it together with the trading date and
	// records the hash; nil means the run reads nothing but its date.
	Inputs func(ctx context.Context, r Run) ([]byte, error)
	// Run does the work. A nil error records the run as succeeded, an error
	// made with Skip records it as skipped, and any other error records it as
	// failed. Succeeded and skipped runs are final; a failed run is tried
	// again the next time the scheduler starts.
	Run func(ctx context.Context, r Run) error
}

// Run is one run of a job, for one trading date. A catch-up run carries the
// date it was due on, not today's, so a job can refuse to act on a session
// other than the one its work was approved for.
type Run struct {
	Job         string
	TradingDate time.Time     // midnight US Eastern at the start of the date
	Session     clock.Session // that date's session
	Scheduled   time.Time     // when the run was due
	// CatchUp is true when the run's trading date is before today's date in
	// US Eastern time, so its own day has already gone.
	CatchUp   bool
	InputHash string // set before Run is called
}

// Date returns the run's trading date as YYYY-MM-DD.
func (r Run) Date() string { return store.FormatDate(r.TradingDate) }

// SkipError is the error a job returns, through Skip, to be recorded as
// skipped rather than failed.
type SkipError struct{ Reason string }

func (e *SkipError) Error() string { return "skipped: " + e.Reason }

// Skip returns an error that records the run as skipped with reason, for
// example when a catch-up run's session has closed. A skipped run is final.
func Skip(reason string) error { return &SkipError{Reason: reason} }

// Config is what a Scheduler needs. Every field but CatchUpDays is required.
type Config struct {
	Store    *store.Store
	Clock    clock.Clock
	Calendar *clock.Calendar
	Logger   *slog.Logger
	// CatchUpDays is how many trading dates, counting today when it is one,
	// the scheduler looks back over. Runs due on older dates are never made
	// up. Zero means DefaultCatchUpDays.
	CatchUpDays int
}

// Scheduler runs registered jobs on trading days and records every run in
// job_runs. It runs jobs one at a time, in the order they fell due.
type Scheduler struct {
	db      *sql.DB
	clk     clock.Clock
	cal     *clock.Calendar
	log     *slog.Logger
	catchUp int

	mu      sync.Mutex
	jobs    []Job
	started bool

	// tried holds failed runs already tried by this process, so a failure is
	// not retried in a tight loop; the next start tries it again.
	tried map[runKey]bool

	// onWait, when set by a test, is called once the loop has asked the
	// clock to wake it, with the time it will wake at.
	onWait func(next time.Time)
}

type runKey struct {
	job  string
	date string
}

// New returns a scheduler with no jobs.
func New(cfg Config) (*Scheduler, error) {
	switch {
	case cfg.Store == nil:
		return nil, errors.New("scheduler: no store")
	case cfg.Clock == nil:
		return nil, errors.New("scheduler: no clock")
	case cfg.Calendar == nil:
		return nil, errors.New("scheduler: no calendar")
	case cfg.Logger == nil:
		return nil, errors.New("scheduler: no logger")
	case cfg.CatchUpDays < 0:
		return nil, fmt.Errorf("scheduler: catch-up days %d is negative", cfg.CatchUpDays)
	}
	days := cfg.CatchUpDays
	if days == 0 {
		days = DefaultCatchUpDays
	}
	return &Scheduler{
		db:      cfg.Store.Writer(),
		clk:     cfg.Clock,
		cal:     cfg.Calendar,
		log:     cfg.Logger,
		catchUp: days,
		tried:   map[runKey]bool{},
	}, nil
}

// Register adds a job. It must be called before Run or RunDue.
func (s *Scheduler) Register(j Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.started:
		return fmt.Errorf("scheduler: register %q after start", j.Name)
	case j.Name == "":
		return errors.New("scheduler: job has no name")
	case j.Run == nil:
		return fmt.Errorf("scheduler: job %q has no run function", j.Name)
	case j.Hour < 0 || j.Hour > 23 || j.Minute < 0 || j.Minute > 59:
		return fmt.Errorf("scheduler: job %q time %d:%02d is not a time of day", j.Name, j.Hour, j.Minute)
	}
	for _, have := range s.jobs {
		if have.Name == j.Name {
			return fmt.Errorf("scheduler: job %q registered twice", j.Name)
		}
	}
	s.jobs = append(s.jobs, j)
	return nil
}

func (s *Scheduler) start() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = true
	return s.jobs
}

// Run catches up every missed run in the catch-up window, then runs each job
// as it falls due, until ctx is done. It returns ctx's error when ctx ends,
// and otherwise only on an error that must stop trading: the calendar not
// covering a date (clock.ErrNotCovered), the database refusing a record, or
// a job panicking. A job's own failure is recorded and logged, and does not
// stop the scheduler.
func (s *Scheduler) Run(ctx context.Context) error {
	jobs := s.start()
	for {
		if err := s.RunDue(ctx); err != nil {
			return err
		}
		var wake <-chan time.Time
		if len(jobs) > 0 {
			now := s.clk.Now()
			next, err := s.next(jobs, now)
			if err != nil {
				return err
			}
			wake = s.clk.After(min(next.Sub(now), maxWait))
			if s.onWait != nil {
				s.onWait(next)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		}
	}
}

// RunDue runs, once each and in the order they fell due, every run in the
// catch-up window whose time has come and that has not yet succeeded or been
// skipped. It returns the same errors as Run.
func (s *Scheduler) RunDue(ctx context.Context) error {
	jobs := s.start()
	if len(jobs) == 0 {
		return nil
	}
	now := s.clk.Now()
	sessions, err := s.window(now)
	if err != nil {
		return err
	}
	today := store.FormatDate(clock.DateOf(now))

	type due struct {
		job     int
		session clock.Session
		at      time.Time
	}
	var queue []due
	missed := make([]int, len(jobs))
	inWindow := map[runKey]bool{}
	for _, ses := range sessions {
		date := store.FormatDate(ses.Date)
		for i, j := range jobs {
			key := runKey{j.Name, date}
			inWindow[key] = true
			at := atTime(ses.Date, j)
			if at.After(now) || s.tried[key] {
				continue
			}
			done, err := s.finished(ctx, key)
			if err != nil {
				return err
			}
			if done {
				continue
			}
			queue = append(queue, due{i, ses, at})
			if date != today {
				missed[i]++
			}
		}
	}
	for key := range s.tried {
		if !inWindow[key] {
			delete(s.tried, key)
		}
	}
	sort.SliceStable(queue, func(a, b int) bool {
		if !queue[a].at.Equal(queue[b].at) {
			return queue[a].at.Before(queue[b].at)
		}
		return queue[a].job < queue[b].job
	})
	for i, n := range missed {
		if n > 0 {
			s.log.Info("catching up missed runs", "job", jobs[i].Name, "missed_dates", n)
		}
	}
	for _, d := range queue {
		if err := ctx.Err(); err != nil {
			return err
		}
		date := store.FormatDate(d.session.Date)
		r := Run{
			Job:         jobs[d.job].Name,
			TradingDate: d.session.Date,
			Session:     d.session,
			Scheduled:   d.at,
			CatchUp:     date != today,
		}
		if err := s.runOne(ctx, jobs[d.job], r); err != nil {
			return err
		}
	}
	return nil
}

// window returns the sessions of the last catchUp trading dates up to and
// including now's date, oldest first.
func (s *Scheduler) window(now time.Time) ([]clock.Session, error) {
	today := clock.DateOf(now)
	var dates []time.Time
	open, err := s.cal.IsTradingDay(today)
	if err != nil {
		return nil, fmt.Errorf("scheduler: %w", err)
	}
	if open {
		dates = append(dates, today)
	}
	for d := today; len(dates) < s.catchUp; {
		if d, err = s.cal.PreviousTradingDay(d); err != nil {
			return nil, fmt.Errorf("scheduler: catch-up window: %w", err)
		}
		dates = append(dates, d)
	}
	sessions := make([]clock.Session, len(dates))
	for i, d := range dates {
		ses, err := s.cal.Session(d)
		if err != nil {
			return nil, fmt.Errorf("scheduler: %w", err)
		}
		sessions[len(dates)-1-i] = ses
	}
	return sessions, nil
}

// next returns the earliest time after now that a job falls due.
func (s *Scheduler) next(jobs []Job, now time.Time) (time.Time, error) {
	today := clock.DateOf(now)
	open, err := s.cal.IsTradingDay(today)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: %w", err)
	}
	following, err := s.cal.NextTradingDay(today)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: next trading day: %w", err)
	}
	var best time.Time
	for _, j := range jobs {
		at := atTime(following, j)
		if t := atTime(today, j); open && t.After(now) {
			at = t
		}
		if best.IsZero() || at.Before(best) {
			best = at
		}
	}
	return best, nil
}

func atTime(date time.Time, j Job) time.Time {
	y, m, d := date.Date()
	return time.Date(y, m, d, j.Hour, j.Minute, 0, 0, clock.Eastern)
}

// finished reports whether key's run has succeeded or been skipped.
func (s *Scheduler) finished(ctx context.Context, key runKey) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM job_runs WHERE job_name = ? AND trading_date = ? AND outcome IN ('succeeded', 'skipped')`,
		key.job, key.date).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("scheduler: read job_runs: %w", err)
	}
	return n > 0, nil
}

// runOne records the start of r, runs it, and records how it ended.
func (s *Scheduler) runOne(ctx context.Context, j Job, r Run) (err error) {
	key := runKey{r.Job, r.Date()}
	s.tried[key] = true

	var inputs []byte
	var inputsErr error
	if j.Inputs != nil {
		inputs, inputsErr = j.Inputs(ctx, r)
	}
	r.InputHash = InputsUnavailable
	if inputsErr == nil {
		r.InputHash = HashInputs(r.Date(), inputs)
	}

	started := s.clk.Now()
	claimed, err := s.claim(ctx, r, started)
	if err != nil || !claimed {
		return err
	}
	log := s.log.With("job", r.Job, "trading_date", r.Date(), "input_hash", r.InputHash)
	if inputsErr != nil {
		return s.finish(ctx, log, r, started, fmt.Errorf("inputs: %w", inputsErr))
	}
	log.Info("job run started")

	defer func() {
		if p := recover(); p != nil {
			if ferr := s.finish(ctx, log, r, started, errors.New("the job panicked")); ferr != nil {
				err = errors.Join(ErrJobPanicked, ferr)
				return
			}
			err = fmt.Errorf("%w: %s on %s", ErrJobPanicked, r.Job, r.Date())
		}
	}()
	return s.finish(ctx, log, r, started, j.Run(ctx, r))
}

// claim writes the run's row as running: a new row, or the existing row of a
// failed or interrupted run. It reports false, without error, when the run
// has already succeeded or been skipped, so a run is never repeated.
func (s *Scheduler) claim(ctx context.Context, r Run, started time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
INSERT INTO job_runs (job_name, trading_date, input_hash, started_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (job_name, trading_date) DO UPDATE SET
    input_hash = excluded.input_hash,
    started_at = excluded.started_at,
    finished_at = NULL,
    outcome = 'running',
    detail = ''
WHERE job_runs.outcome IN ('running', 'failed')`,
		r.Job, r.Date(), r.InputHash, store.FormatTime(started))
	if err != nil {
		return false, fmt.Errorf("scheduler: record start of %s on %s: %w", r.Job, r.Date(), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("scheduler: record start of %s on %s: %w", r.Job, r.Date(), err)
	}
	return n == 1, nil
}

// finish records how a run ended, from the error its job returned.
func (s *Scheduler) finish(ctx context.Context, log *slog.Logger, r Run, started time.Time, runErr error) error {
	outcome, detail := "succeeded", ""
	var skip *SkipError
	switch {
	case errors.As(runErr, &skip):
		outcome, detail = "skipped", skip.Reason
	case runErr != nil:
		outcome, detail = "failed", runErr.Error()
	}
	if len(detail) > maxDetail {
		detail = detail[:maxDetail]
	}
	// A clock stepped backwards must not make the row finish before it
	// started.
	finished := s.clk.Now()
	if finished.Before(started) {
		finished = started
	}

	// The record is written even when ctx has ended, so a run cut short is
	// left failed rather than running.
	res, err := s.db.ExecContext(context.WithoutCancel(ctx),
		`UPDATE job_runs SET outcome = ?, finished_at = ?, detail = ?
WHERE job_name = ? AND trading_date = ? AND outcome = 'running'`,
		outcome, store.FormatTime(finished), detail, r.Job, r.Date())
	if err == nil {
		var n int64
		if n, err = res.RowsAffected(); err == nil && n != 1 {
			err = fmt.Errorf("%d rows updated, want 1", n)
		}
	}
	if err != nil {
		return fmt.Errorf("scheduler: record end of %s on %s: %w", r.Job, r.Date(), err)
	}

	args := []any{"outcome", outcome, "duration", finished.Sub(started)}
	switch outcome {
	case "failed":
		log.Error("job run failed", append(args, "error", runErr)...)
	case "skipped":
		log.Info("job run skipped", append(args, "reason", detail)...)
	default:
		log.Info("job run finished", args...)
	}
	return nil
}

// HashInputs is the input hash recorded for a run on date (YYYY-MM-DD) that
// read inputs: hex SHA-256 over the date, a newline, and the inputs.
func HashInputs(date string, inputs []byte) string {
	h := sha256.New()
	h.Write([]byte(date))
	h.Write([]byte{'\n'})
	h.Write(inputs)
	return hex.EncodeToString(h.Sum(nil))
}
