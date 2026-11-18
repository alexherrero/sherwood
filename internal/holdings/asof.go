package holdings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/alexherrero/sherwood/internal/store"
)

// AsOf returns the newest snapshot of holdings recorded as of t or earlier,
// or ErrNone if there was none by then. A job that runs for an earlier date
// reads the holdings as they were then, so a late or repeated run sees the
// same holdings as an on-time one.
func AsOf(ctx context.Context, q Querier, t time.Time) (Set, error) {
	return readSet(ctx, q,
		`SELECT id, source, as_of, holding_count, over_limit FROM holding_snapshots
		 WHERE as_of <= ? ORDER BY id DESC LIMIT 1`, store.FormatTime(t))
}

// CashAsOf returns the newest cash reading recorded as of t or earlier, or
// ErrNone if there was none by then.
func CashAsOf(ctx context.Context, q Querier, t time.Time) (Cash, error) {
	var (
		c         Cash
		src, asOf string
	)
	err := q.QueryRowContext(ctx,
		`SELECT id, source, as_of, amount FROM cash_snapshots WHERE as_of <= ? ORDER BY id DESC LIMIT 1`,
		store.FormatTime(t)).Scan(&c.ID, &src, &asOf, &c.Amount)
	if errors.Is(err, sql.ErrNoRows) {
		return Cash{}, ErrNone
	}
	if err != nil {
		return Cash{}, fmt.Errorf("holdings: reading cash: %w", err)
	}
	c.Source = Source(src)
	if c.AsOf, err = store.ParseTime(asOf); err != nil {
		return Cash{}, fmt.Errorf("holdings: cash reading %d: %w", c.ID, err)
	}
	return c, nil
}
