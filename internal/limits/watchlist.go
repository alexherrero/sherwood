package limits

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/store"
)

// WatchSetting is the settings_changes row of a stock added to the watchlist
// from the phone app: new_value is the symbol, old_value is empty and the
// source is phone.
const WatchSetting = "watchlist.add"

// Errors Watch returns. Each is wrapped with a sentence for the phone app.
var (
	// ErrNotSymbol means the text is not written as a stock symbol.
	ErrNotSymbol = errors.New("limits: not a stock symbol")
	// ErrWatched means the stock is already on the watchlist in force.
	ErrWatched = errors.New("limits: already on the watchlist")
	// ErrNoSector means a sector limit is in force and the strategy file
	// gives the stock no sector, so the risk check could not measure a buy
	// of it.
	ErrNoSector = errors.New("limits: the strategy file gives the stock no sector")
)

// Additions returns the stocks added to the watchlist from the phone app
// that are in force for the strategy file s, oldest first: those recorded
// after the newest hash row, and only while that row names the file loaded
// now. When the file changes, the earlier additions no longer apply, as with
// the limits' tightenings. A recorded addition that cannot be read is an
// error, never ignored.
func Additions(ctx context.Context, q Querier, s *config.Strategy) ([]Change, error) {
	from, err := anchor(ctx, q, s.Hash)
	if err != nil || from == 0 {
		return nil, err
	}
	rows, err := q.QueryContext(ctx,
		`SELECT id, changed_at, setting, old_value, new_value FROM settings_changes
		 WHERE setting = ? AND source = ? AND id > ? ORDER BY id`, WatchSetting, string(Phone), from)
	if err != nil {
		return nil, fmt.Errorf("limits: reading watchlist additions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Change
	for rows.Next() {
		c, err := scanChange(rows, Phone)
		if err != nil {
			return nil, err
		}
		if !config.ValidSymbol(c.New) {
			return nil, fmt.Errorf("%w: change %d adds %q, which is not a stock symbol", ErrCorrupt, c.ID, c.New)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("limits: reading watchlist additions: %w", err)
	}
	return out, nil
}

// Watchlist reads the watchlist in force for the strategy file s: the
// file's watchlist, in its order, followed by the phone's additions in
// force (see Additions), each stock once. It is the one reader of the
// watchlist; nothing outside it reads s.Watchlist for trading. Use
// s.WithWatchlist on the result to run the rules of the strategies that opt
// in to the watchlist.
func Watchlist(ctx context.Context, q Querier, s *config.Strategy) ([]string, error) {
	adds, err := Additions(ctx, q, s)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(s.Watchlist)
	for _, c := range adds {
		if !slices.Contains(out, c.New) {
			out = append(out, c.New)
		}
	}
	return out, nil
}

// Watch records sym as added to the watchlist from the phone app, and
// returns the change. It refuses text that is not a stock symbol
// (ErrNotSymbol), a stock already on the watchlist in force (ErrWatched),
// and, while a sector limit is in force, a stock the strategy file gives no
// sector (ErrNoSector). It only records the addition: it creates no order
// and no signal. The watchlist and the limits in force are read in the same
// transaction as the write.
func Watch(ctx context.Context, st *store.Store, s *config.Strategy, at time.Time, sym string) (Change, error) {
	if !config.ValidSymbol(sym) {
		return Change{}, fmt.Errorf("%w: %q", ErrNotSymbol, sym)
	}
	var c Change
	err := write(ctx, st, func(tx *sql.Tx) error {
		if _, err := record(ctx, tx, s, at); err != nil {
			return err
		}
		watched, err := Watchlist(ctx, tx, s)
		if err != nil {
			return err
		}
		if slices.Contains(watched, sym) {
			return fmt.Errorf("%w: %s", ErrWatched, sym)
		}
		eff, err := Effective(ctx, tx, s)
		if err != nil {
			return err
		}
		if eff.MaxSectorShare != nil && s.Sectors[sym] == "" {
			return fmt.Errorf("%w: %s", ErrNoSector, sym)
		}
		id, err := insert(ctx, tx, at, WatchSetting, "", sym, Phone)
		if err != nil {
			return err
		}
		c = Change{ID: id, Setting: WatchSetting, New: sym, Source: Phone, At: at}
		return nil
	})
	return c, err
}
