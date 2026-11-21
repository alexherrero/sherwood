package feed

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// ExportCloses is how many of each stock's recent checked closes the export
// carries: about six weeks of trading days, enough to see a trend without
// sending the whole price history.
const ExportCloses = 30

// BuildExport builds the research export from the database as it stands,
// at time at, for the stocks held now and the given watchlist. Read it
// through one read transaction so the holdings, closes and signals come from
// the same moment.
//
// Each held or watched stock appears once, sorted by symbol, with up to
// ExportCloses of its newest usable closes (from the usable_bars view: a
// complete day, the primary source, both sources agreeing), oldest first.
// The signals are those of the latest trading date with any rules output (a
// signal or a holding suggestion), in the order they were made.
//
// It reads only symbols from the holdings, never their share counts or
// costs, and never reads cash, orders or account details.
func BuildExport(ctx context.Context, q Querier, at time.Time, watchlist []string) (Export, error) {
	e := Export{
		Version:     ExportVersion,
		GeneratedAt: at.UTC(),
		Symbols:     []ExportSymbol{},
		Signals:     []ExportSignal{},
	}

	held, err := heldSymbols(ctx, q)
	if err != nil {
		return Export{}, err
	}
	watched := make(map[string]bool, len(watchlist))
	for _, s := range watchlist {
		watched[s] = true
	}
	all := make([]string, 0, len(held)+len(watched))
	for s := range held {
		all = append(all, s)
	}
	for s := range watched {
		if !held[s] {
			all = append(all, s)
		}
	}
	slices.Sort(all)
	for _, s := range all {
		closes, err := recentCloses(ctx, q, s)
		if err != nil {
			return Export{}, err
		}
		e.Symbols = append(e.Symbols, ExportSymbol{Symbol: s, Held: held[s], Watched: watched[s], Closes: closes})
	}

	if e.SignalDate, err = latestRulesDate(ctx, q); err != nil {
		return Export{}, err
	}
	if e.SignalDate != "" {
		if e.Signals, err = signalsOf(ctx, q, e.SignalDate); err != nil {
			return Export{}, err
		}
	}
	return e, nil
}

// heldSymbols reads the symbols of the current holdings, and nothing else
// about them.
func heldSymbols(ctx context.Context, q Querier) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT symbol FROM current_holdings`)
	if err != nil {
		return nil, fmt.Errorf("feed: reading the held symbols: %w", err)
	}
	defer func() { _ = rows.Close() }()
	held := map[string]bool{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("feed: reading the held symbols: %w", err)
		}
		held[s] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("feed: reading the held symbols: %w", err)
	}
	return held, nil
}

// recentCloses reads a stock's newest usable closes, oldest first.
func recentCloses(ctx context.Context, q Querier, symbol string) ([]Close, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT trading_date, close FROM usable_bars WHERE symbol = ?
		 ORDER BY trading_date DESC LIMIT ?`, symbol, ExportCloses)
	if err != nil {
		return nil, fmt.Errorf("feed: reading the closes of %s: %w", symbol, err)
	}
	defer func() { _ = rows.Close() }()
	out := []Close{}
	for rows.Next() {
		var c Close
		if err := rows.Scan(&c.Date, &c.Close); err != nil {
			return nil, fmt.Errorf("feed: reading the closes of %s: %w", symbol, err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("feed: reading the closes of %s: %w", symbol, err)
	}
	slices.Reverse(out)
	return out, nil
}

// latestRulesDate is the newest trading date the rules recorded anything
// for, or "" when they never have.
func latestRulesDate(ctx context.Context, q Querier) (string, error) {
	var d *string
	err := q.QueryRowContext(ctx,
		`SELECT max(trading_date) FROM (
		     SELECT trading_date FROM signals
		     UNION ALL
		     SELECT trading_date FROM holding_suggestions)`).Scan(&d)
	if err != nil {
		return "", fmt.Errorf("feed: reading the latest rules run: %w", err)
	}
	if d == nil {
		return "", nil
	}
	return *d, nil
}

// signalsOf reads a trading date's signals, in the order they were made.
func signalsOf(ctx context.Context, q Querier, date string) ([]ExportSignal, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT idempotency_key, symbol, side, kind, reason FROM signals
		 WHERE trading_date = ? ORDER BY id`, date)
	if err != nil {
		return nil, fmt.Errorf("feed: reading the signals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []ExportSignal{}
	for rows.Next() {
		var s ExportSignal
		if err := rows.Scan(&s.Key, &s.Symbol, &s.Side, &s.Kind, &s.Reason); err != nil {
			return nil, fmt.Errorf("feed: reading the signals: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("feed: reading the signals: %w", err)
	}
	return out, nil
}
