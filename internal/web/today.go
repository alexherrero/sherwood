package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/store"
)

// todayData is what the Today view shows.
type todayData struct {
	// Live is true in live mode. The banner says which mode in words.
	Live bool
	// Halt is the halt found by the latest price check, or nil.
	Halt *haltView
	// PricesChecked is the date of the latest price check, or "" if prices
	// have never been checked.
	PricesChecked string
	// Jobs is the last run of each job that has ever run, by name.
	Jobs []jobView
}

// haltView is a halt and its cause.
type haltView struct {
	Date   string
	Stocks []haltStock
}

// haltStock is one held stock whose price sources disagreed.
type haltStock struct {
	Symbol string
	Detail string
}

// jobView is one job's last run.
type jobView struct {
	Name string
	// Date is the trading date the run was for.
	Date string
	// Outcome is the run's outcome in words; Badge only reinforces it.
	Outcome string
	Badge   string
	// Started and Finished are when it ran; Finished is "" while running.
	Started  string
	Finished string
	// Detail is what went wrong or what the run did, as the job recorded it.
	Detail string
}

// outcomes words each job_runs outcome and picks its badge.
var outcomes = map[string]struct{ words, badge string }{
	"succeeded": {"Succeeded", "badge--passed"},
	"failed":    {"Failed", "badge--blocked"},
	"skipped":   {"Skipped", "badge--hold"},
	"running":   {"Running now", "badge--hold"},
}

// loadToday reads Today's data in one read transaction, so every part of the
// page comes from the same moment.
func loadToday(s *Server, r *http.Request) (any, error) {
	ctx := r.Context()
	tx, err := s.store.Reader().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	d := todayData{Live: s.mode == config.ModeLive}
	if d.PricesChecked, d.Halt, err = latestHalt(ctx, tx); err != nil {
		return nil, err
	}
	if d.Jobs, err = lastRuns(ctx, tx); err != nil {
		return nil, err
	}
	return d, nil
}

// latestHalt reads the latest price check. A held stock whose two sources
// disagreed halts trading until you review it.
//
// The price check is the only halt recorded so far. Paper trading adds the
// stop switch and its own record of halts, and this view will read that too.
func latestHalt(ctx context.Context, tx *sql.Tx) (string, *haltView, error) {
	var date string
	err := tx.QueryRowContext(ctx,
		`SELECT trading_date FROM price_days ORDER BY trading_date DESC LIMIT 1`).Scan(&date)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("web: reading the latest price check: %w", err)
	}
	at, err := time.ParseInLocation(clock.DateLayout, date, clock.Eastern)
	if err != nil {
		return "", nil, fmt.Errorf("web: reading the latest price check: %w", err)
	}
	pd, err := prices.LoadDay(ctx, tx, at)
	if err != nil {
		return "", nil, err
	}
	if !pd.Halt() {
		return day(at), nil, nil
	}
	h := &haltView{Date: day(at)}
	for _, c := range pd.Checks {
		if c.Halt {
			h.Stocks = append(h.Stocks, haltStock{Symbol: c.Symbol, Detail: c.Detail})
		}
	}
	return day(at), h, nil
}

// lastRuns reads the newest run of each job, by trading date and then start
// time.
func lastRuns(ctx context.Context, tx *sql.Tx) ([]jobView, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT job_name, trading_date, outcome, started_at, finished_at, detail
		 FROM job_runs AS j
		 WHERE id = (SELECT k.id FROM job_runs AS k WHERE k.job_name = j.job_name
		             ORDER BY k.trading_date DESC, k.started_at DESC, k.id DESC LIMIT 1)
		 ORDER BY job_name`)
	if err != nil {
		return nil, fmt.Errorf("web: reading job runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []jobView
	for rows.Next() {
		var name, date, outcome, started, detail string
		var finished sql.NullString
		if err := rows.Scan(&name, &date, &outcome, &started, &finished, &detail); err != nil {
			return nil, fmt.Errorf("web: reading job runs: %w", err)
		}
		v, err := jobRow(name, date, outcome, started, finished, detail)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("web: reading job runs: %w", err)
	}
	return out, nil
}

func jobRow(name, date, outcome, started string, finished sql.NullString, detail string) (jobView, error) {
	o, ok := outcomes[outcome]
	if !ok {
		return jobView{}, fmt.Errorf("web: job %s has an unknown outcome", name)
	}
	on, err := time.ParseInLocation(clock.DateLayout, date, clock.Eastern)
	if err != nil {
		return jobView{}, fmt.Errorf("web: job %s trading date: %w", name, err)
	}
	st, err := store.ParseTime(started)
	if err != nil {
		return jobView{}, fmt.Errorf("web: job %s start: %w", name, err)
	}
	v := jobView{Name: name, Date: day(on), Outcome: o.words, Badge: o.badge, Started: when(st), Detail: detail}
	if finished.Valid {
		ft, err := store.ParseTime(finished.String)
		if err != nil {
			return jobView{}, fmt.Errorf("web: job %s finish: %w", name, err)
		}
		v.Finished = when(ft)
	}
	return v, nil
}
