// Package backup makes the nightly copy of the database and checks it.
//
// The copy is made with SQLite's VACUUM INTO on one of the store's read-only
// connections. In write-ahead mode that is an ordinary read, so it never
// takes the single writer: a job or a tap on the phone that writes while the
// copy is being made carries on. The copy is a consistent snapshot of one
// moment, and it is written under a temporary name and renamed into place
// only once it has passed its check.
//
// The check opens the copy as a database of its own, runs SQLite's quick
// integrity check, and counts the rows of every table, which must match the
// counts read from the live database at the moment the copy was made. A copy
// that fails is removed, and the older copies are kept: only a copy that
// passed ever pushes an old one out.
package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/scheduler"
	"github.com/alexherrero/sherwood/internal/store"
)

// prefix and suffix make a copy's file name: sherwood-YYYY-MM-DD.db.
const (
	prefix = "sherwood-"
	suffix = ".db"
)

// stableTries is how many times Take tries to make a copy while writes keep
// landing between the counts and the copy.
const stableTries = 3

// ErrNoFolder means the settings file names no backup folder.
var ErrNoFolder = errors.New("backup: no backup folder is set; add backup.dir to the settings file")

// ErrCheck means a copy failed its check. The copy has been removed.
var ErrCheck = errors.New("backup: the copy failed its check")

// Config is what a Backer needs. Every field is required.
type Config struct {
	Store  *store.Store
	Dir    string // the backup folder; it must already exist
	Keep   int    // how many copies to keep
	Logger *slog.Logger
}

// Backer makes and checks backups.
type Backer struct {
	cfg Config

	// afterCopy, when set by a test, is called with the temporary copy's
	// path once it is written and before it is checked.
	afterCopy func(path string)
}

// New returns a Backer. A missing folder is not refused here: it makes every
// backup fail with ErrNoFolder, so you hear about it each night.
func New(cfg Config) (*Backer, error) {
	switch {
	case cfg.Store == nil:
		return nil, errors.New("backup: no store")
	case cfg.Logger == nil:
		return nil, errors.New("backup: no logger")
	case cfg.Keep < 1:
		return nil, fmt.Errorf("backup: keep %d is less than 1", cfg.Keep)
	}
	return &Backer{cfg: cfg}, nil
}

// Counts is the number of rows in each table, by table name.
type Counts map[string]int64

// Result is what one backup made.
type Result struct {
	Path    string   // the checked copy
	Counts  Counts   // its rows per table
	Removed []string // older copies removed to keep Keep
}

// FileName is the name of date's copy (YYYY-MM-DD).
func FileName(date string) string { return prefix + date + suffix }

// Take copies the database to the backup folder as date's copy, checks it,
// and removes the oldest copies beyond Keep. Taking the same date again
// replaces that date's copy once the new one has passed.
func (b *Backer) Take(ctx context.Context, date string) (Result, error) {
	if b.cfg.Dir == "" {
		return Result{}, ErrNoFolder
	}
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return Result{}, fmt.Errorf("backup: date %q is not YYYY-MM-DD", date)
	}
	info, err := os.Stat(b.cfg.Dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return Result{}, errors.New("backup: the backup folder does not exist; check that the share is mounted into the container")
	case err != nil:
		return Result{}, fmt.Errorf("backup: the backup folder: %w", err)
	case !info.IsDir():
		return Result{}, errors.New("backup: the backup folder is not a folder")
	}

	final := filepath.Join(b.cfg.Dir, FileName(date))
	tmp := final + ".partial"
	want, err := b.copy(ctx, tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}
	if b.afterCopy != nil {
		b.afterCopy(tmp)
	}
	if err := Check(ctx, tmp, want); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return Result{}, fmt.Errorf("backup: putting the copy in place: %w", err)
	}
	removed, err := b.prune()
	if err != nil {
		return Result{Path: final, Counts: want}, err
	}
	return Result{Path: final, Counts: want, Removed: removed}, nil
}

// copy writes the database to path with VACUUM INTO and returns the row
// counts of the moment it copied. It uses a connection of its own, opened
// read-only on the store's file, because the store's readers refuse even the
// write VACUUM INTO makes to its new file. The counts are read before and
// after the copy on the same connection; if a write landed in between, the
// copy may not match them, so it is made again.
func (b *Backer) copy(ctx context.Context, path string) (Counts, error) {
	src, err := databaseFile(ctx, b.cfg.Store.Reader())
	if err != nil {
		return nil, err
	}
	db, err := openReadOnly(src)
	if err != nil {
		return nil, fmt.Errorf("backup: opening the database to copy it: %w", err)
	}
	defer func() { _ = db.Close() }()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("backup: opening the database to copy it: %w", err)
	}
	defer func() { _ = conn.Close() }()

	for range stableTries {
		before, err := count(ctx, conn)
		if err != nil {
			return nil, fmt.Errorf("backup: counting the database's rows: %w", err)
		}
		_ = os.Remove(path) // VACUUM INTO refuses a file that exists
		if _, err := conn.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
			return nil, fmt.Errorf("backup: copying the database: %w", err)
		}
		after, err := count(ctx, conn)
		if err != nil {
			return nil, fmt.Errorf("backup: counting the database's rows: %w", err)
		}
		if maps.Equal(before, after) {
			return after, nil
		}
		b.cfg.Logger.Info("the database changed while it was copied, so the copy is made again")
	}
	return nil, fmt.Errorf("backup: the database kept changing while it was copied (%d tries)", stableTries)
}

// databaseFile is the path of the main database file behind q.
func databaseFile(ctx context.Context, q querier) (string, error) {
	var seq int
	var name, file string
	if err := q.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		return "", fmt.Errorf("backup: finding the database file: %w", err)
	}
	if name != "main" || file == "" {
		return "", errors.New("backup: the database has no file to copy")
	}
	return file, nil
}

// openReadOnly opens the database file at path read-only, as a pool of one
// connection that waits on a lock rather than failing at once.
func openReadOnly(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// Check opens the database file at path read-only, runs SQLite's quick
// integrity check, and compares the rows in every table with want. Any
// difference, a table too many or too few included, fails with ErrCheck.
func Check(ctx context.Context, path string, want Counts) error {
	db, err := openReadOnly(path)
	if err != nil {
		return fmt.Errorf("%w: opening it: %w", ErrCheck, err)
	}
	defer func() { _ = db.Close() }()

	var verdict string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&verdict); err != nil {
		return fmt.Errorf("%w: the integrity check could not run: %w", ErrCheck, err)
	}
	if verdict != "ok" {
		return fmt.Errorf("%w: the integrity check says %q", ErrCheck, verdict)
	}
	got, err := count(ctx, db)
	if err != nil {
		return fmt.Errorf("%w: counting its rows: %w", ErrCheck, err)
	}
	var diffs []string
	for _, t := range slices.Sorted(maps.Keys(union(want, got))) {
		w, inWant := want[t]
		g, inGot := got[t]
		switch {
		case !inGot:
			diffs = append(diffs, fmt.Sprintf("table %s is missing", t))
		case !inWant:
			diffs = append(diffs, fmt.Sprintf("table %s should not be there", t))
		case w != g:
			diffs = append(diffs, fmt.Sprintf("table %s has %d rows, want %d", t, g, w))
		}
	}
	if len(diffs) > 0 {
		return fmt.Errorf("%w: %s", ErrCheck, strings.Join(diffs, "; "))
	}
	return nil
}

func union(a, b Counts) Counts {
	out := maps.Clone(a)
	maps.Copy(out, b)
	return out
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// count reads the row count of every table, SQLite's own tables aside.
func count(ctx context.Context, q querier) (Counts, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		tables = append(tables, name)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	out := make(Counts, len(tables))
	for _, t := range tables {
		var n int64
		// The name comes from sqlite_schema, not from a caller, and is
		// quoted as an identifier.
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM "`+strings.ReplaceAll(t, `"`, `""`)+`"`).Scan(&n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, nil
}

// prune removes the oldest copies beyond Keep and returns their names. Only
// files named like a copy are touched; anything else in the folder is left.
func (b *Backer) prune() ([]string, error) {
	entries, err := os.ReadDir(b.cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("backup: listing the backup folder: %w", err)
	}
	var copies []string
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		date := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			continue
		}
		copies = append(copies, name)
	}
	// The names sort by date.
	sort.Sort(sort.Reverse(sort.StringSlice(copies)))
	var removed []string
	for _, name := range copies[min(b.cfg.Keep, len(copies)):] {
		if err := os.Remove(filepath.Join(b.cfg.Dir, name)); err != nil {
			return removed, fmt.Errorf("backup: removing an old copy: %w", err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}

// JobName is the nightly backup's job name. Never change it once it has run.
const JobName = "backup"

// The backup's time of day, US Eastern, on every calendar date.
const (
	JobHour   = 2
	JobMinute = 0
)

// Job is the nightly backup as a scheduler job: every day at 02:00 US
// Eastern, weekends and holidays included. A failed run sends
// notify.BackupFailed once for the date, and the reason is kept on the run.
func (b *Backer) Job() scheduler.Job {
	return scheduler.Job{
		Name:     JobName,
		Hour:     JobHour,
		Minute:   JobMinute,
		EveryDay: true,
		Alert:    notify.BackupFailed(),
		Run: func(ctx context.Context, r scheduler.Run) error {
			res, err := b.Take(ctx, r.Date())
			if err != nil {
				return err
			}
			b.cfg.Logger.Info("backup made and checked", "job", r.Job, "trading_date", r.Date(),
				"backup_file", filepath.Base(res.Path), "backup_kept", b.cfg.Keep)
			return nil
		},
	}
}
