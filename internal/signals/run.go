package signals

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/store"
)

// DefaultHistoryDays is how many calendar days of bars a run reads before the
// trading date when Runner.HistoryDays is zero. The longest look-back a rule
// can have is 302 trading days, which is about 440 calendar days with
// weekends and holidays.
const DefaultHistoryDays = 460

// Runner runs the rules for a trading date and records the outcome.
type Runner struct {
	Store *store.Store
	Clock clock.Clock
	Log   *slog.Logger
	// HistoryDays is how many calendar days of bars to read before the
	// trading date. Zero means DefaultHistoryDays. A run reads further back
	// when a holding was opened earlier, so a trailing stop sees every close
	// since the buy.
	HistoryDays int
}

// Result is a run's plan and how much of it was new.
type Result struct {
	*Plan
	// NewSignals and NewSuggestions count the rows this run inserted. On a
	// second run of the same inputs both are zero.
	NewSignals     int
	NewSuggestions int
}

// Run evaluates the rules for date and records the signals and suggestions
// in one transaction. Rows already recorded for the date are left as they
// are. A date whose prices are missing or were never checked records nothing
// and is not an error.
func (r *Runner) Run(ctx context.Context, date time.Time, strategy *config.Strategy, held []holdings.Holding) (*Result, error) {
	if r.Store == nil || r.Clock == nil || r.Log == nil {
		return nil, errors.New("signals: the runner needs a store, a clock and a logger")
	}
	date = clock.DateOf(date)
	in := Inputs{Date: date, Strategy: strategy, Holdings: held}
	q := r.Store.Reader()
	day, err := prices.LoadDay(ctx, q, date)
	switch {
	case errors.Is(err, prices.ErrNotChecked):
	case err != nil:
		return nil, err
	default:
		in.Day = day
	}
	if in.Day != nil && in.Day.Usable && strategy != nil {
		if in.Bars, err = r.loadBars(ctx, q, in); err != nil {
			return nil, err
		}
	}
	plan, err := Evaluate(in)
	if err != nil {
		return nil, err
	}
	res := &Result{Plan: plan}
	if plan.Missing == "" {
		if err := r.write(ctx, res); err != nil {
			return nil, err
		}
	}
	r.log(ctx, res)
	return res, nil
}

// loadBars reads the primary source's bars for every strategy stock.
func (r *Runner) loadBars(ctx context.Context, q prices.Querier, in Inputs) (map[string][]prices.Bar, error) {
	days := r.HistoryDays
	if days <= 0 {
		days = DefaultHistoryDays
	}
	from := in.Date.AddDate(0, 0, -days)
	for _, h := range in.Holdings {
		if o := clock.DateOf(h.Opened); o.Before(from) {
			from = o
		}
	}
	out := map[string][]prices.Bar{}
	for _, def := range in.Strategy.Strategies {
		for _, sym := range def.Stocks {
			if _, done := out[sym]; done {
				continue
			}
			bars, err := prices.History(ctx, q, in.Day.PrimarySource, sym, from, in.Date)
			if err != nil {
				return nil, err
			}
			out[sym] = bars
		}
	}
	return out, nil
}

func (r *Runner) write(ctx context.Context, res *Result) (err error) {
	tx, err := r.Store.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("signals: starting the write: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	at := store.FormatTime(r.Clock.Now())
	for _, s := range res.Signals {
		n, err := insert(ctx, tx,
			`INSERT INTO signals (idempotency_key, strategy, symbol, trading_date, side, kind, rule_type, reason,
			     strategy_hash, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			s.Key, s.Strategy, s.Symbol, dateText(s.Date), string(s.Side), string(s.Kind), s.Rule, s.Reason,
			s.StrategyHash, at)
		if err != nil {
			return fmt.Errorf("signals: recording %s %s %s: %w", s.Strategy, s.Symbol, s.Kind, err)
		}
		res.NewSignals += n
	}
	for _, s := range res.Suggestions {
		var stop any
		if s.Stop != nil {
			stop = *s.Stop
		}
		n, err := insert(ctx, tx,
			`INSERT INTO holding_suggestions (strategy, symbol, trading_date, suggestion, reason, stop, strategy_hash,
			     created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			s.Strategy, s.Symbol, dateText(s.Date), string(s.Suggestion), s.Reason, stop, s.StrategyHash, at)
		if err != nil {
			return fmt.Errorf("signals: recording the suggestion for %s %s: %w", s.Strategy, s.Symbol, err)
		}
		res.NewSuggestions += n
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("signals: committing: %w", err)
	}
	return nil
}

func insert(ctx context.Context, tx *sql.Tx, query string, args ...any) (int, error) {
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func (r *Runner) log(ctx context.Context, res *Result) {
	date := dateText(res.Date)
	if res.Missing != "" {
		r.Log.WarnContext(ctx, "rules run: no signals", "trading_date", date, "reason", res.Missing)
		return
	}
	for _, s := range res.Signals {
		r.Log.InfoContext(ctx, "signal", "trading_date", date, "strategy", s.Strategy, "symbol", s.Symbol,
			"side", string(s.Side), "signal", string(s.Kind), "rule", s.Rule, "reason", s.Reason,
			"order_key", s.Key, "strategy_hash", s.StrategyHash)
	}
	summary := fmt.Sprintf("%d signals (%d new), %d holding suggestions (%d new)",
		len(res.Signals), res.NewSignals, len(res.Suggestions), res.NewSuggestions)
	if len(res.Excluded) > 0 {
		summary += "; no signals for " + strings.Join(res.Excluded, ", ") + " (prices not usable)"
	}
	r.Log.InfoContext(ctx, "rules run", "trading_date", date, "reason", summary)
}

func dateText(t time.Time) string { return t.In(clock.Eastern).Format(clock.DateLayout) }

// Querier is what the readers need: a *sql.DB (such as store.Reader()) or a
// *sql.Tx.
type Querier = prices.Querier

// Load reads the signals recorded for a trading date, in the order they were
// recorded.
func Load(ctx context.Context, q Querier, date time.Time) ([]Signal, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT idempotency_key, strategy, symbol, trading_date, side, kind, rule_type, reason, strategy_hash
		 FROM signals WHERE trading_date = ? ORDER BY id`, dateText(clock.DateOf(date)))
	if err != nil {
		return nil, fmt.Errorf("signals: reading: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Signal
	for rows.Next() {
		var s Signal
		var d, side, kind string
		if err := rows.Scan(&s.Key, &s.Strategy, &s.Symbol, &d, &side, &kind, &s.Rule, &s.Reason, &s.StrategyHash); err != nil {
			return nil, fmt.Errorf("signals: reading: %w", err)
		}
		if s.Date, err = time.ParseInLocation(clock.DateLayout, d, clock.Eastern); err != nil {
			return nil, fmt.Errorf("signals: reading: date %q: %w", d, err)
		}
		s.Side, s.Kind = Side(side), Kind(kind)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("signals: reading: %w", err)
	}
	return out, nil
}

// LoadSuggestions reads the holding suggestions recorded for a trading date,
// in the order they were recorded.
func LoadSuggestions(ctx context.Context, q Querier, date time.Time) ([]HoldingSuggestion, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT strategy, symbol, trading_date, suggestion, reason, stop, strategy_hash
		 FROM holding_suggestions WHERE trading_date = ? ORDER BY id`, dateText(clock.DateOf(date)))
	if err != nil {
		return nil, fmt.Errorf("signals: reading suggestions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []HoldingSuggestion
	for rows.Next() {
		var s HoldingSuggestion
		var d, sug string
		var stop sql.Null[money.Price]
		if err := rows.Scan(&s.Strategy, &s.Symbol, &d, &sug, &s.Reason, &stop, &s.StrategyHash); err != nil {
			return nil, fmt.Errorf("signals: reading suggestions: %w", err)
		}
		if s.Date, err = time.ParseInLocation(clock.DateLayout, d, clock.Eastern); err != nil {
			return nil, fmt.Errorf("signals: reading suggestions: date %q: %w", d, err)
		}
		s.Suggestion = Suggestion(sug)
		if stop.Valid {
			p := stop.V
			s.Stop = &p
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("signals: reading suggestions: %w", err)
	}
	return out, nil
}
