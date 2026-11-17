package prices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/store"
)

// ErrNotChecked means a trading date's prices have not been fetched and
// cross-checked. For the rules it means the same as a missing day.
var ErrNotChecked = errors.New("prices: the date has not been checked")

// Querier is what the readers need: a *sql.DB (such as store.Reader()) or a
// *sql.Tx.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// storedBar is a bar with the source it came from.
type storedBar struct {
	source string
	Bar
}

// writeDay records the bars and the day's cross-check in one transaction,
// replacing any earlier record of the same date. A bar already stored for the
// same stock, date and source is overwritten, so history fetched again after
// a split takes the new basis.
func writeDay(ctx context.Context, s *store.Store, d *Day, bars []storedBar, now time.Time) (err error) {
	tx, err := s.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("prices: starting the write: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	at := store.FormatTime(now)
	for _, b := range bars {
		if _, err = tx.ExecContext(ctx,
			`INSERT INTO price_bars (symbol, trading_date, source, open, high, low, close, volume, fetched_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (symbol, trading_date, source) DO UPDATE SET
			     open = excluded.open, high = excluded.high, low = excluded.low, close = excluded.close,
			     volume = excluded.volume, fetched_at = excluded.fetched_at`,
			b.Symbol, b.DateString(), b.source, b.Open, b.High, b.Low, b.Close, b.Volume, at); err != nil {
			return fmt.Errorf("prices: storing %s %s from %s: %w", b.Symbol, b.DateString(), b.source, err)
		}
	}
	date := d.DateString()
	if _, err = tx.ExecContext(ctx, `DELETE FROM price_days WHERE trading_date = ?`, date); err != nil {
		return fmt.Errorf("prices: clearing %s: %w", date, err)
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO price_days (trading_date, primary_source, check_source, tolerance, usable, detail, checked_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		date, d.PrimarySource, d.CheckSource, d.Tolerance, boolInt(d.Usable), d.Detail, at); err != nil {
		return fmt.Errorf("prices: recording %s: %w", date, err)
	}
	for _, c := range d.Checks {
		if _, err = tx.ExecContext(ctx,
			`INSERT INTO price_checks (trading_date, symbol, held, outcome, halt, detail) VALUES (?, ?, ?, ?, ?, ?)`,
			date, c.Symbol, boolInt(c.Held), string(c.Outcome), boolInt(c.Halt), c.Detail); err != nil {
			return fmt.Errorf("prices: recording %s %s: %w", date, c.Symbol, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("prices: committing %s: %w", date, err)
	}
	return nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// LoadDay reads a trading date's cross-check. It returns ErrNotChecked if the
// date was never checked.
func LoadDay(ctx context.Context, q Querier, date time.Time) (*Day, error) {
	date = clock.DateOf(date)
	d := &Day{Date: date}
	var usable int64
	err := q.QueryRowContext(ctx,
		`SELECT primary_source, check_source, tolerance, usable, detail FROM price_days WHERE trading_date = ?`,
		d.DateString()).Scan(&d.PrimarySource, &d.CheckSource, &d.Tolerance, &usable, &d.Detail)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrNotChecked, d.DateString())
	}
	if err != nil {
		return nil, fmt.Errorf("prices: reading %s: %w", d.DateString(), err)
	}
	d.Usable = usable == 1
	rows, err := q.QueryContext(ctx,
		`SELECT symbol, held, outcome, halt, detail FROM price_checks WHERE trading_date = ? ORDER BY symbol`,
		d.DateString())
	if err != nil {
		return nil, fmt.Errorf("prices: reading %s checks: %w", d.DateString(), err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var c Check
		var held, halt int64
		var outcome string
		if err := rows.Scan(&c.Symbol, &held, &outcome, &halt, &c.Detail); err != nil {
			return nil, fmt.Errorf("prices: reading %s checks: %w", d.DateString(), err)
		}
		c.Held, c.Halt, c.Outcome = held == 1, halt == 1, Outcome(outcome)
		d.Checks = append(d.Checks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("prices: reading %s checks: %w", d.DateString(), err)
	}
	return d, nil
}

// UsableBars returns the bars the rules may use on a trading date, sorted by
// symbol: the primary source's bar of each stock whose sources agreed, and
// nothing at all if the day is missing or was never checked.
func UsableBars(ctx context.Context, q Querier, date time.Time) ([]Bar, error) {
	return queryBars(ctx, q,
		`SELECT symbol, trading_date, open, high, low, close, volume FROM usable_bars
		 WHERE trading_date = ? ORDER BY symbol`,
		clock.DateOf(date).Format(clock.DateLayout))
}

// History returns one stock's stored bars from one source between two dates,
// both inclusive, oldest first. It is history for the rules to look back on;
// whether the latest date may be acted on is UsableBars' question.
func History(ctx context.Context, q Querier, source, symbol string, from, to time.Time) ([]Bar, error) {
	return queryBars(ctx, q,
		`SELECT symbol, trading_date, open, high, low, close, volume FROM price_bars
		 WHERE source = ? AND symbol = ? AND trading_date BETWEEN ? AND ? ORDER BY trading_date`,
		source, symbol, clock.DateOf(from).Format(clock.DateLayout), clock.DateOf(to).Format(clock.DateLayout))
}

func queryBars(ctx context.Context, q Querier, query string, args ...any) ([]Bar, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("prices: reading bars: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Bar
	for rows.Next() {
		var b Bar
		var date string
		var o, h, l, c money.Price
		if err := rows.Scan(&b.Symbol, &date, &o, &h, &l, &c, &b.Volume); err != nil {
			return nil, fmt.Errorf("prices: reading bars: %w", err)
		}
		if b.Date, err = time.ParseInLocation(clock.DateLayout, date, clock.Eastern); err != nil {
			return nil, fmt.Errorf("prices: reading bars: date %q: %w", date, err)
		}
		b.Open, b.High, b.Low, b.Close = o, h, l, c
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("prices: reading bars: %w", err)
	}
	return out, nil
}
