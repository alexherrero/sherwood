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
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/store"
)

// todayData is what the Today view shows.
type todayData struct {
	// Live is true in live mode. The banner says which mode in words.
	Live bool
	// Result is the outcome of the last stop switch action, if any.
	Result *notice
	// Switch is the stop switch: on or off, and the halts that turned it on.
	Switch switchView
	// Waiting counts the orders waiting for your approval; the banner links
	// to Approvals when there are any.
	Waiting int
	// PricesChecked is the date of the latest price check, or "" if prices
	// have never been checked.
	PricesChecked string
	// Jobs is the last run of each job that has ever run, by name.
	Jobs []jobView
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
	if res, ok := switchResults[r.URL.Query().Get("switch")]; ok {
		d.Result = &res
	}
	st, err := halt.Active(ctx, tx)
	if err != nil {
		return nil, err
	}
	d.Switch = newSwitchView(st)
	waiting, err := orders.InState(ctx, tx, orders.AwaitingApproval)
	if err != nil {
		return nil, err
	}
	d.Waiting = len(waiting)
	if d.PricesChecked, err = latestPriceCheck(ctx, tx); err != nil {
		return nil, err
	}
	if d.Jobs, err = lastRuns(ctx, tx); err != nil {
		return nil, err
	}
	return d, nil
}

// latestPriceCheck reads the date of the latest price check. A disagreement
// on a held stock is not read here: the after-close job throws the stop
// switch for it, and the halt shows with its evidence.
func latestPriceCheck(ctx context.Context, tx *sql.Tx) (string, error) {
	var date string
	err := tx.QueryRowContext(ctx,
		`SELECT trading_date FROM price_days ORDER BY trading_date DESC LIMIT 1`).Scan(&date)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("web: reading the latest price check: %w", err)
	}
	at, err := time.ParseInLocation(clock.DateLayout, date, clock.Eastern)
	if err != nil {
		return "", fmt.Errorf("web: reading the latest price check: %w", err)
	}
	return day(at), nil
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
