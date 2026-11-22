package limits

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/store"
)

// The live-mode gate. Sherwood builds the broker adapter and sends real
// orders only when all three of these hold when it starts:
//
//  1. The settings file names live mode.
//  2. You have confirmed live mode in the app, from an approval device.
//  3. The paper record is at least the strategy file's min_paper_weeks old.
//
// Both the mode and the confirmation are rows in settings_changes, so the
// history says when each changed and from where.
//
// The mode is recorded at start-up (RecordMode, source nas) whenever it
// differs from the newest recorded mode. The confirmation (ConfirmLive,
// source phone) names the strategy file's hash, and it holds only while it is
// newer than both the newest mode row and the newest strategy file row, and
// both still say live and name the file loaded now. So a confirmation lapses,
// and must be given again, when the strategy file changes or the mode
// changes (to paper, and back to live again later): going live is always a
// fresh decision about the file and the limits in force.
//
// The paper record's weeks run from its first holdings or cash reading to
// the start of the current live stretch (the newest mode row, when it says
// live), or to now in paper mode. Waiting in live mode with the gate shut
// is not paper trading, because nothing is simulated then, so it does not
// count.

// The settings_changes rows the gate reads.
const (
	// ModeSetting records the mode the settings file names: new_value is
	// "paper" or "live", and the source is nas.
	ModeSetting = "mode"
	// LiveSetting records your confirmation of live mode: new_value is the
	// strategy file's hash, old_value is empty and the source is phone.
	LiveSetting = "live.confirmed"
)

// Errors ConfirmLive returns.
var (
	// ErrNotLive means the settings file does not name live mode.
	ErrNotLive = errors.New("limits: the settings file does not name live mode")
	// ErrConfirmed means live mode is already confirmed for this strategy
	// file and this live stretch.
	ErrConfirmed = errors.New("limits: live mode is already confirmed")
	// ErrPaperShort means the paper record is shorter than min_paper_weeks.
	ErrPaperShort = errors.New("limits: the paper record is shorter than the minimum paper period")
)

// week is one week of the paper record.
const week = 7 * 24 * time.Hour

// Gate is the live-mode gate as it stands.
type Gate struct {
	// Mode is the mode the settings file names.
	Mode config.Mode
	// Live is true when the settings file names live mode and the newest
	// recorded mode says so too.
	Live bool
	// LiveSince is when the current live stretch began; zero in paper mode.
	LiveSince time.Time
	// Confirmed is true when a confirmation holds; ConfirmedAt is when it
	// was given.
	Confirmed   bool
	ConfirmedAt time.Time
	// PaperStarted is false until holdings or cash have been recorded.
	// PaperSince is the record's first reading.
	PaperStarted bool
	PaperSince   time.Time
	// PaperWeeks is the whole weeks of paper trading so far, and MinWeeks
	// the strategy file's min_paper_weeks. PaperMet is true once the record
	// is at least MinWeeks long.
	PaperWeeks int
	MinWeeks   int
	PaperMet   bool
}

// Open reports whether all three conditions hold.
func (g Gate) Open() bool { return g.Live && g.Confirmed && g.PaperMet }

// Reasons says, one sentence each, which conditions do not hold, or nothing
// when the gate is open.
func (g Gate) Reasons() []string {
	var out []string
	switch {
	case g.Mode != config.ModeLive:
		out = append(out, "The settings file names paper mode, not live mode.")
	case !g.Live:
		out = append(out, "Live mode is not recorded yet; sherwood records it when it starts.")
	}
	if !g.Confirmed {
		out = append(out, "Live mode has not been confirmed in the app for this strategy file.")
	}
	if !g.PaperMet {
		out = append(out, g.paperReason())
	}
	return out
}

// paperReason says why the paper record is too short.
func (g Gate) paperReason() string {
	if !g.PaperStarted {
		return "There is no paper record yet."
	}
	return fmt.Sprintf("The paper record is %s long, and the strategy file asks for at least %s.",
		weeks(g.PaperWeeks), weeks(g.MinWeeks))
}

func weeks(n int) string {
	if n == 1 {
		return "1 week"
	}
	return fmt.Sprintf("%d weeks", n)
}

// row is one settings_changes row the gate reads.
type row struct {
	id    int64
	value string
	at    time.Time
}

// newestRow reads the newest row of setting; ok is false when there is none.
func newestRow(ctx context.Context, q Querier, setting string) (r row, ok bool, err error) {
	var at string
	var v sql.NullString
	err = q.QueryRowContext(ctx,
		`SELECT id, new_value, changed_at FROM settings_changes WHERE setting = ? ORDER BY id DESC LIMIT 1`,
		setting).Scan(&r.id, &v, &at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return row{}, false, nil
	case err != nil:
		return row{}, false, fmt.Errorf("limits: reading %s: %w", setting, err)
	}
	if r.at, err = store.ParseTime(at); err != nil {
		return row{}, false, fmt.Errorf("%w: change %d: %w", ErrCorrupt, r.id, err)
	}
	r.value = v.String
	return r, true, nil
}

// paperStart reads the paper record's first reading: the earlier of the
// first cash reading and the first holdings snapshot.
func paperStart(ctx context.Context, q Querier) (time.Time, bool, error) {
	var first sql.NullString
	err := q.QueryRowContext(ctx, `SELECT min(as_of) FROM (
		SELECT min(as_of) AS as_of FROM cash_snapshots
		UNION ALL SELECT min(as_of) FROM holding_snapshots)`).Scan(&first)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("limits: reading the paper record's start: %w", err)
	}
	if !first.Valid {
		return time.Time{}, false, nil
	}
	t, err := store.ParseTime(first.String)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("%w: the paper record's start: %w", ErrCorrupt, err)
	}
	return t, true, nil
}

// ReadGate reads the live-mode gate for the strategy file s and the mode the
// settings file names, as of now.
func ReadGate(ctx context.Context, q Querier, s *config.Strategy, mode config.Mode, now time.Time) (Gate, error) {
	g := Gate{Mode: mode, MinWeeks: s.Limits.MinPaperWeeks}
	m, hasMode, err := newestRow(ctx, q, ModeSetting)
	if err != nil {
		return Gate{}, err
	}
	g.Live = mode == config.ModeLive && hasMode && m.value == string(config.ModeLive)

	since, started, err := paperStart(ctx, q)
	if err != nil {
		return Gate{}, err
	}
	if started {
		end := now
		if mode == config.ModeLive {
			// Fail closed: without a recorded live stretch, no time counts.
			end = since
			if g.Live {
				end = m.at
				g.LiveSince = m.at
			}
		}
		g.PaperStarted, g.PaperSince = true, since
		if d := end.Sub(since); d > 0 {
			g.PaperWeeks = int(d / week)
			g.PaperMet = d >= time.Duration(g.MinWeeks)*week
		}
	}

	if !g.Live {
		return g, nil
	}
	c, ok, err := newestRow(ctx, q, LiveSetting)
	if err != nil || !ok {
		return g, err
	}
	file, err := anchor(ctx, q, s.Hash)
	if err != nil {
		return Gate{}, err
	}
	if file != 0 && c.value == s.Hash && c.id > file && c.id > m.id {
		g.Confirmed, g.ConfirmedAt = true, c.at
	}
	return g, nil
}

// RecordMode records the mode the settings file names, when it is not the
// newest recorded mode, with source nas. It reports whether it wrote. A
// change of mode lapses any confirmation of live mode.
func RecordMode(ctx context.Context, st *store.Store, mode config.Mode, at time.Time) (bool, error) {
	var wrote bool
	err := write(ctx, st, func(tx *sql.Tx) error {
		prev, _, err := newest(ctx, tx, ModeSetting, "")
		if err != nil || prev == string(mode) {
			return err
		}
		_, err = insert(ctx, tx, at, ModeSetting, prev, string(mode), NAS)
		wrote = err == nil
		return err
	})
	return wrote, err
}

// ConfirmLive records your confirmation of live mode for the strategy file
// s, from the phone app, and returns it. It refuses when the settings file
// does not name live mode (ErrNotLive), when live mode is already confirmed
// (ErrConfirmed) and when the paper record is shorter than min_paper_weeks
// (ErrPaperShort). The gate is read in the same transaction as the write.
// The confirmation takes effect when sherwood next starts.
func ConfirmLive(ctx context.Context, st *store.Store, s *config.Strategy, mode config.Mode, at time.Time) (Change, error) {
	var c Change
	err := write(ctx, st, func(tx *sql.Tx) error {
		if _, err := record(ctx, tx, s, at); err != nil {
			return err
		}
		g, err := ReadGate(ctx, tx, s, mode, at)
		switch {
		case err != nil:
			return err
		case !g.Live:
			return ErrNotLive
		case g.Confirmed:
			return ErrConfirmed
		case !g.PaperMet:
			return fmt.Errorf("%w: %s", ErrPaperShort, g.paperReason())
		}
		id, err := insert(ctx, tx, at, LiveSetting, "", s.Hash, Phone)
		if err != nil {
			return err
		}
		c = Change{ID: id, Setting: LiveSetting, New: s.Hash, Source: Phone, At: at}
		return nil
	})
	return c, err
}
