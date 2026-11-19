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
	"strings"
	"sync"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/notify"
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

// lateAfter is how long after its time a run may start before it counts as
// missed: the container was down or busy when it fell due, and you are told.
const lateAfter = 15 * time.Minute

// maxDetail caps the text kept in job_runs.detail.
const maxDetail = 500

// InputsUnavailable is the input hash recorded when a job's Inputs function
// fails, so the run can still be recorded as failed.
const InputsUnavailable = "inputs-unavailable"

// ErrJobPanicked means a job panicked. The run is recorded as failed and the
// scheduler stops, because a panic is a bug and sherwood fails closed.
var ErrJobPanicked = errors.New("scheduler: job panicked")

// Job is a daily job. It runs once per trading date, at Hour:Minute US
// Eastern time on that date, or once per calendar date when EveryDay is set.
type Job struct {
	// Name keys the job's runs in job_runs. It must be unique and must not
	// change once the job has run, or its history is lost.
	Name string
	// Hour and Minute are the time of day in US Eastern time.
	Hour, Minute int
	// EveryDay makes the job run on every calendar date, weekends and
	// holidays included, instead of on trading days only. Its runs carry the
	// calendar date as their TradingDate. An every-day job only ever makes up
	// its most recent missed run, never the dates before it: the nightly
	// backup is the example, and a backup taken late is as good as the one
	// that was missed.
	EveryDay bool
	// Alert is what is sent to your phone when a run fails. The zero Alert
	// means notify.JobFailed().
	Alert notify.Alert
	// AlertGroup joins jobs that try the same work at different times, such
	// as the after-close job and its retries: once one run in the group has
	// failed for a date, later failures in the group on that date send no
	// alert. Empty means the job's own name, so a run retried after a restart
	// still alerts only once.
	AlertGroup string
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

// Run is one run of a job, for one trading date (or, for an every-day job,
// one calendar date). A catch-up run carries the
// date it was due on, not today's, so a job can refuse to act on a session
// other than the one its work was approved for.
type Run struct {
	Job         string
	TradingDate time.Time     // midnight US Eastern at the start of the date
	Session     clock.Session // that date's session; only Date is set when the exchange is closed
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

// Config is what a Scheduler needs. Every field but CatchUpDays and Alerts is
// required.
type Config struct {
	Store    *store.Store
	Clock    clock.Clock
	Calendar *clock.Calendar
	Logger   *slog.Logger
	// Alerts is told once when a run fails (see Job.AlertGroup) and once
	// when a pass of the scheduler finds runs that are late because the
	// container was down. Nil means notify.Nowhere.
	Alerts notify.Notifier
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
	alerts  notify.Notifier
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
	alerts := cfg.Alerts
	if alerts == nil {
		alerts = notify.Nowhere
	}
	return &Scheduler{
		db:      cfg.Store.Writer(),
		clk:     cfg.Clock,
		cal:     cfg.Calendar,
		log:     cfg.Logger,
		alerts:  alerts,
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
	case j.Alert != (notify.Alert{}) && !j.Alert.Valid():
		return fmt.Errorf("scheduler: job %q has an alert not made by package notify", j.Name)
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
	daily, err := s.everyDaySession(jobs, now)
	if err != nil {
		return err
	}
	firstStart, err := s.firstStart(ctx)
	if err != nil {
		return err
	}

	type due struct {
		job     int
		session clock.Session
		at      time.Time
	}
	var queue []due
	missed := make([]int, len(jobs))
	late := 0
	inWindow := map[runKey]bool{}
	consider := func(i int, ses clock.Session) error {
		j := jobs[i]
		date := store.FormatDate(ses.Date)
		key := runKey{j.Name, date}
		inWindow[key] = true
		at := atTime(ses.Date, j)
		if at.After(now) || s.tried[key] {
			return nil
		}
		done, recorded, err := s.finished(ctx, key)
		if err != nil || done {
			return err
		}
		queue = append(queue, due{i, ses, at})
		if date != today {
			missed[i]++
		}
		// A run that never started near its time was missed. A failed or
		// interrupted run being tried again was not: it has a row already.
		if !recorded && now.Sub(at) > lateAfter {
			late++
		}
		return nil
	}
	for i, j := range jobs {
		if !j.EveryDay {
			continue
		}
		if err := consider(i, daily[i]); err != nil {
			return err
		}
	}
	for _, ses := range sessions {
		for i, j := range jobs {
			if j.EveryDay {
				continue
			}
			if err := consider(i, ses); err != nil {
				return err
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
	// One alert for the whole pass, however many runs it makes up. The very
	// first start of a new database has nothing it could have missed.
	if late > 0 && !firstStart {
		s.log.Warn("scheduled runs were missed and are running late", "missed_runs", late)
		s.send(ctx, notify.JobMissed())
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
		if err := s.runOne(ctx, jobs, jobs[d.job], r); err != nil {
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

// everyDaySession returns, for each every-day job (indexed as jobs), the
// session of the most recent date whose run time has come: today once the
// time has passed, otherwise yesterday. Other jobs' entries are left zero.
func (s *Scheduler) everyDaySession(jobs []Job, now time.Time) ([]clock.Session, error) {
	out := make([]clock.Session, len(jobs))
	today := clock.DateOf(now)
	for i, j := range jobs {
		if !j.EveryDay {
			continue
		}
		date := today
		if atTime(today, j).After(now) {
			date = clock.DateOf(today.AddDate(0, 0, -1))
		}
		ses, err := s.sessionOn(date)
		if err != nil {
			return nil, err
		}
		out[i] = ses
	}
	return out, nil
}

// sessionOn returns date's session, or a Session holding only the date when
// the exchange is closed then.
func (s *Scheduler) sessionOn(date time.Time) (clock.Session, error) {
	open, err := s.cal.IsTradingDay(date)
	if err != nil {
		return clock.Session{}, fmt.Errorf("scheduler: %w", err)
	}
	if !open {
		return clock.Session{Date: date}, nil
	}
	ses, err := s.cal.Session(date)
	if err != nil {
		return clock.Session{}, fmt.Errorf("scheduler: %w", err)
	}
	return ses, nil
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
	tomorrow := clock.DateOf(today.AddDate(0, 0, 1))
	var best time.Time
	for _, j := range jobs {
		at := atTime(following, j)
		if t := atTime(today, j); open && t.After(now) {
			at = t
		}
		if j.EveryDay {
			at = atTime(today, j)
			if !at.After(now) {
				at = atTime(tomorrow, j)
			}
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

// finished reports whether key's run has succeeded or been skipped, and
// whether it has a row at all (a failed or interrupted run has one too).
func (s *Scheduler) finished(ctx context.Context, key runKey) (done, recorded bool, err error) {
	var outcome string
	err = s.db.QueryRowContext(ctx,
		`SELECT outcome FROM job_runs WHERE job_name = ? AND trading_date = ?`,
		key.job, key.date).Scan(&outcome)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, false, nil
	case err != nil:
		return false, false, fmt.Errorf("scheduler: read job_runs: %w", err)
	}
	return outcome == "succeeded" || outcome == "skipped", true, nil
}

// firstStart reports whether no job has ever run against this database.
func (s *Scheduler) firstStart(ctx context.Context) (bool, error) {
	var ran bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM job_runs)`).Scan(&ran); err != nil {
		return false, fmt.Errorf("scheduler: read job_runs: %w", err)
	}
	return !ran, nil
}

// alreadyFailed reports whether a run of j's alert group has already failed
// on date, so its failure was already sent.
func (s *Scheduler) alreadyFailed(ctx context.Context, jobs []Job, j Job, date string) (bool, error) {
	group := alertGroup(j)
	var names []any
	for _, other := range jobs {
		if alertGroup(other) == group {
			names = append(names, other.Name)
		}
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ")
	var failed bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM job_runs WHERE trading_date = ? AND outcome = 'failed' AND job_name IN (`+marks+`))`,
		append([]any{date}, names...)...).Scan(&failed)
	if err != nil {
		return false, fmt.Errorf("scheduler: read job_runs: %w", err)
	}
	return failed, nil
}

func alertGroup(j Job) string {
	if j.AlertGroup != "" {
		return j.AlertGroup
	}
	return j.Name
}

// send sends a; a failure is logged and never stops a job or the scheduler.
func (s *Scheduler) send(ctx context.Context, a notify.Alert) {
	err := s.alerts.Notify(context.WithoutCancel(ctx), a)
	switch {
	case errors.Is(err, notify.ErrNoRecipients):
	case err != nil:
		s.log.Warn("alert not sent", "push_alert", a.Name(), "error", err)
	}
}

// runOne records the start of r, runs it, and records how it ended.
func (s *Scheduler) runOne(ctx context.Context, jobs []Job, j Job, r Run) (err error) {
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

	// Read before the claim, which clears this run's own failed row.
	alerted, err := s.alreadyFailed(ctx, jobs, j, r.Date())
	if err != nil {
		return err
	}
	started := s.clk.Now()
	claimed, err := s.claim(ctx, r, started)
	if err != nil || !claimed {
		return err
	}
	log := s.log.With("job", r.Job, "trading_date", r.Date(), "input_hash", r.InputHash)
	end := func(runErr error) error {
		failed, err := s.finish(ctx, log, r, started, runErr)
		if failed && !alerted {
			s.send(ctx, failureAlert(j))
		}
		return err
	}
	if inputsErr != nil {
		return end(fmt.Errorf("inputs: %w", inputsErr))
	}
	log.Info("job run started")

	defer func() {
		if p := recover(); p != nil {
			if ferr := end(errors.New("the job panicked")); ferr != nil {
				err = errors.Join(ErrJobPanicked, ferr)
				return
			}
			err = fmt.Errorf("%w: %s on %s", ErrJobPanicked, r.Job, r.Date())
		}
	}()
	return end(j.Run(ctx, r))
}

// failureAlert is the alert a failed run of j sends.
func failureAlert(j Job) notify.Alert {
	if j.Alert != (notify.Alert{}) {
		return j.Alert
	}
	return notify.JobFailed()
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

// finish records how a run ended, from the error its job returned. It
// reports whether the run failed.
func (s *Scheduler) finish(ctx context.Context, log *slog.Logger, r Run, started time.Time, runErr error) (bool, error) {
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
		return outcome == "failed", fmt.Errorf("scheduler: record end of %s on %s: %w", r.Job, r.Date(), err)
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
	return outcome == "failed", nil
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
