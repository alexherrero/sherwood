package orders

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

const orderColumns = `id, idempotency_key, strategy, symbol, trading_date, side, kind, order_type,
	quantity, price, state, reason, risk_verdict, strategy_hash, created_at, updated_at`

// Get reads one order by id, or ErrNotFound.
func Get(ctx context.Context, q Querier, id int64) (Order, error) {
	o, err := scanOrder(q.QueryRowContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, fmt.Errorf("%w: id %d", ErrNotFound, id)
	}
	return o, err
}

// ByKey reads one order by its idempotency key, or ErrNotFound.
func ByKey(ctx context.Context, q Querier, key string) (Order, error) {
	o, err := scanOrder(q.QueryRowContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE idempotency_key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, fmt.Errorf("%w: key %s", ErrNotFound, key)
	}
	return o, err
}

// ForDate lists the orders made for a trading date, oldest first.
func ForDate(ctx context.Context, q Querier, date time.Time) ([]Order, error) {
	return list(ctx, q, `SELECT `+orderColumns+` FROM orders WHERE trading_date = ? ORDER BY id`,
		clock.DateOf(date).Format(clock.DateLayout))
}

// InState lists every order in any of the given states, oldest first.
func InState(ctx context.Context, q Querier, states ...State) ([]Order, error) {
	if len(states) == 0 {
		return nil, nil
	}
	args := make([]any, len(states))
	for i, st := range states {
		if !st.Valid() {
			return nil, fmt.Errorf("%w: unknown state %q", ErrInvalid, st)
		}
		args[i] = string(st)
	}
	marks := strings.Repeat(", ?", len(states))[2:]
	return list(ctx, q, `SELECT `+orderColumns+` FROM orders WHERE state IN (`+marks+`) ORDER BY id`, args...)
}

func list(ctx context.Context, q Querier, query string, args ...any) ([]Order, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("orders: reading orders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("orders: reading orders: %w", err)
	}
	return out, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanOrder(r scanner) (Order, error) {
	var (
		o                         Order
		date, side, kind, typ, st string
		price, verdict            sql.NullString
		created, updated          string
	)
	err := r.Scan(&o.ID, &o.Key, &o.Strategy, &o.Symbol, &date, &side, &kind, &typ,
		&o.Quantity, &price, &st, &o.Reason, &verdict, &o.StrategyHash, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, err
	}
	if err != nil {
		return Order{}, fmt.Errorf("orders: reading an order: %w", err)
	}
	o.Side, o.Kind, o.Type, o.State = signals.Side(side), signals.Kind(kind), Type(typ), State(st)
	o.Verdict = verdict.String
	if price.Valid {
		p, err := money.ParsePrice(price.String)
		if err != nil {
			return Order{}, fmt.Errorf("orders: reading order %d's price: %w", o.ID, err)
		}
		o.Price = &p
	}
	if o.Date, err = parseDate(date); err != nil {
		return Order{}, fmt.Errorf("orders: reading order %d: %w", o.ID, err)
	}
	if o.Created, err = store.ParseTime(created); err != nil {
		return Order{}, fmt.Errorf("orders: reading order %d: %w", o.ID, err)
	}
	if o.Updated, err = store.ParseTime(updated); err != nil {
		return Order{}, fmt.Errorf("orders: reading order %d: %w", o.ID, err)
	}
	return o, nil
}

// History lists an order's changes, oldest first. The first is its creation.
func History(ctx context.Context, q Querier, id int64) ([]Event, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, order_id, from_state, to_state, actor, reason, risk_verdict, at
		FROM order_history WHERE order_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("orders: reading order %d's history: %w", id, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		var (
			e             Event
			from, verdict sql.NullString
			to, actor, at string
		)
		if err := rows.Scan(&e.ID, &e.OrderID, &from, &to, &actor, &e.Reason, &verdict, &at); err != nil {
			return nil, fmt.Errorf("orders: reading order %d's history: %w", id, err)
		}
		e.From, e.To, e.Actor, e.Verdict = State(from.String), State(to), Actor(actor), verdict.String
		if e.At, err = store.ParseTime(at); err != nil {
			return nil, fmt.Errorf("orders: reading order %d's history: %w", id, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("orders: reading order %d's history: %w", id, err)
	}
	return out, nil
}

// Fills lists an order's fills, oldest first.
func Fills(ctx context.Context, q Querier, id int64) ([]Fill, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, order_id, trading_date, quantity, price, value, reason, filled_at
		FROM fills WHERE order_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("orders: reading order %d's fills: %w", id, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Fill
	for rows.Next() {
		var (
			f        Fill
			date, at string
		)
		if err := rows.Scan(&f.ID, &f.OrderID, &date, &f.Quantity, &f.Price, &f.Value, &f.Reason, &at); err != nil {
			return nil, fmt.Errorf("orders: reading order %d's fills: %w", id, err)
		}
		if f.Date, err = parseDate(date); err != nil {
			return nil, fmt.Errorf("orders: reading order %d's fills: %w", id, err)
		}
		if f.At, err = store.ParseTime(at); err != nil {
			return nil, fmt.Errorf("orders: reading order %d's fills: %w", id, err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("orders: reading order %d's fills: %w", id, err)
	}
	return out, nil
}

// parseDate reads a stored trading date as midnight Eastern.
func parseDate(s string) (time.Time, error) {
	return time.ParseInLocation(clock.DateLayout, s, clock.Eastern)
}
