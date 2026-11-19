package backup

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
	"github.com/alexherrero/sherwood/internal/scheduler"
	"github.com/alexherrero/sherwood/internal/store"
)

func et(y int, m time.Month, d, hour, minute int) time.Time {
	return time.Date(y, m, d, hour, minute, 0, 0, clock.Eastern)
}

// openStore opens a fresh, migrated database with a few made-up job runs in
// it, so there are rows to count.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sherwood.db"), et(2026, 11, 1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for i, date := range []string{"2026-11-02", "2026-11-03", "2026-11-04"} {
		_, err := st.Writer().Exec(`INSERT INTO job_runs (job_name, trading_date, input_hash, started_at, finished_at, outcome)
VALUES ('after-close', ?, 'made-up-hash', ?, ?, 'succeeded')`,
			date, store.FormatTime(et(2026, 11, 2+i, 16, 30)), store.FormatTime(et(2026, 11, 2+i, 16, 31)))
		if err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func newBacker(t *testing.T, st *store.Store, dir string, keep int) *Backer {
	t.Helper()
	logger, _ := loggingtest.New(t)
	b, err := New(Config{Store: st, Dir: dir, Keep: keep, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBackupRestoresWithMatchingRowCounts(t *testing.T) {
	st := openStore(t)
	dir := t.TempDir()
	b := newBacker(t, st, dir, 7)
	ctx := context.Background()

	res, err := b.Take(ctx, "2026-11-18")
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != filepath.Join(dir, "sherwood-2026-11-18.db") {
		t.Errorf("copy is at %s", res.Path)
	}
	if res.Counts["job_runs"] != 3 || res.Counts["schema_migrations"] == 0 {
		t.Errorf("counts = %v, want 3 job runs and the migrations", res.Counts)
	}
	live, err := count(ctx, st.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != len(res.Counts) {
		t.Errorf("the copy counted %d tables, the live database has %d", len(res.Counts), len(live))
	}

	// Restore it as the runbook says: copy the file in as the database, and
	// start on it. Nothing is left to migrate and every row is there.
	restored := filepath.Join(t.TempDir(), "sherwood.db")
	copyFile(t, res.Path, restored)
	rst, err := store.Open(ctx, restored, et(2026, 11, 19, 0, 0))
	if err != nil {
		t.Fatalf("the restored database does not open: %v", err)
	}
	defer func() { _ = rst.Close() }()
	if m := rst.Migrated(); len(m) != 0 {
		t.Errorf("the restored database applied migrations %v, want none", m)
	}
	got, err := count(ctx, rst.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if !equalCounts(got, live) {
		t.Errorf("restored counts %v, want %v", got, live)
	}
	if err := Check(ctx, restored, live); err != nil {
		t.Errorf("the restored database fails the check: %v", err)
	}
}

func TestBackupDoesNotHoldTheWriter(t *testing.T) {
	st := openStore(t)
	b := newBacker(t, st, t.TempDir(), 7)
	ctx := context.Background()
	// Hold the single writer in a transaction for the whole backup: if the
	// backup needed it, this would wait out the busy timeout and fail.
	tx, err := st.Writer().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO job_runs (job_name, trading_date, input_hash, started_at) VALUES ('x', '2026-11-05', 'h', ?)`,
		store.FormatTime(et(2026, 11, 5, 9, 0))); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := b.Take(ctx, "2026-11-18")
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Errorf("the backup took %v, as though it waited for the writer", took)
	}
	// The uncommitted row is not in the copy.
	if res.Counts["job_runs"] != 3 {
		t.Errorf("the copy has %d job runs, want the 3 committed", res.Counts["job_runs"])
	}
}

func TestCorruptedCopyFailsTheCheckAndKeepsOlderCopies(t *testing.T) {
	st := openStore(t)
	dir := t.TempDir()
	b := newBacker(t, st, dir, 7)
	ctx := context.Background()
	if _, err := b.Take(ctx, "2026-11-17"); err != nil {
		t.Fatal(err)
	}

	b.afterCopy = corrupt(t)
	_, err := b.Take(ctx, "2026-11-18")
	if !errors.Is(err, ErrCheck) {
		t.Fatalf("Take = %v, want ErrCheck", err)
	}
	if got := names(t, dir); !slices.Equal(got, []string{"sherwood-2026-11-17.db"}) {
		t.Errorf("backup folder = %q, want only the older good copy", got)
	}
}

func TestCopyMissingRowsFailsTheCheck(t *testing.T) {
	st := openStore(t)
	b := newBacker(t, st, t.TempDir(), 7)
	b.afterCopy = func(path string) {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		if _, err := db.Exec(`DELETE FROM job_runs WHERE trading_date = '2026-11-03'`); err != nil {
			t.Fatal(err)
		}
	}
	_, err := b.Take(context.Background(), "2026-11-18")
	if !errors.Is(err, ErrCheck) || !strings.Contains(err.Error(), "table job_runs has 2 rows, want 3") {
		t.Fatalf("Take = %v, want a row-count difference on job_runs", err)
	}
}

// TestCorruptedCopyAlertsOnceThroughTheNightlyJob runs the backup as the
// scheduler runs it, on a Sunday, with a copy that is corrupted after it is
// written.
func TestCorruptedCopyAlertsOnceThroughTheNightlyJob(t *testing.T) {
	st := openStore(t)
	b := newBacker(t, st, t.TempDir(), 7)
	b.afterCopy = corrupt(t)
	alerts := &notifytest.Fake{}
	logger, _ := loggingtest.New(t)
	clk := clock.NewFake(et(2026, 11, 22, 2, 0)) // a Sunday
	s, err := scheduler.New(scheduler.Config{Store: st, Clock: clk, Calendar: clock.NYSE(), Logger: logger, Alerts: alerts})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Register(b.Job()); err != nil {
		t.Fatal(err)
	}
	if err := s.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	var outcome, detail string
	if err := st.Reader().QueryRow(`SELECT outcome, detail FROM job_runs WHERE job_name = 'backup' AND trading_date = '2026-11-22'`).
		Scan(&outcome, &detail); err != nil {
		t.Fatal(err)
	}
	if outcome != "failed" || !strings.Contains(detail, "the copy failed its check") {
		t.Errorf("backup run = %s %q, want failed by the check", outcome, detail)
	}
	sent := alerts.Sent()
	if len(sent) != 1 || sent[0].Name() != "backup_failed" {
		t.Fatalf("alerts = %v, want one backup_failed", sent)
	}

	// A restart the same night tries again, fails again, and stays quiet.
	clk.Advance(time.Hour)
	again, err := scheduler.New(scheduler.Config{Store: st, Clock: clk, Calendar: clock.NYSE(), Logger: logger, Alerts: alerts})
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Register(b.Job()); err != nil {
		t.Fatal(err)
	}
	if err := again.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(alerts.Sent()); n != 1 {
		t.Errorf("%d alerts after the retry, want still 1", n)
	}
}

func TestKeepsOnlyTheNewestCopies(t *testing.T) {
	st := openStore(t)
	dir := t.TempDir()
	// Something else in the folder is never touched.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := newBacker(t, st, dir, 2)
	ctx := context.Background()
	var removed []string
	for _, d := range []string{"2026-11-15", "2026-11-16", "2026-11-17", "2026-11-18", "2026-11-18"} {
		res, err := b.Take(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		removed = append(removed, res.Removed...)
	}
	if got := names(t, dir); !slices.Equal(got, []string{"notes.txt", "sherwood-2026-11-17.db", "sherwood-2026-11-18.db"}) {
		t.Errorf("backup folder = %q", got)
	}
	if !slices.Equal(removed, []string{"sherwood-2026-11-15.db", "sherwood-2026-11-16.db"}) {
		t.Errorf("removed = %q", removed)
	}
}

func TestRefusesAMissingFolder(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if _, err := newBacker(t, st, "", 7).Take(ctx, "2026-11-18"); !errors.Is(err, ErrNoFolder) {
		t.Errorf("no folder: %v, want ErrNoFolder", err)
	}
	missing := filepath.Join(t.TempDir(), "not-mounted")
	_, err := newBacker(t, st, missing, 7).Take(ctx, "2026-11-18")
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unmounted folder: %v", err)
	}
	if _, statErr := os.Stat(missing); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("the backup created the missing folder")
	}
}

func TestNewRefusesMissingParts(t *testing.T) {
	logger, _ := loggingtest.New(t)
	st := openStore(t)
	for _, cfg := range []Config{
		{Dir: "/x", Keep: 1, Logger: logger},
		{Store: st, Dir: "/x", Keep: 1},
		{Store: st, Dir: "/x", Logger: logger},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) accepted it", cfg)
		}
	}
}

// corrupt returns an afterCopy hook that overwrites the middle of the copy
// with junk.
func corrupt(t *testing.T) func(string) {
	return func(path string) {
		f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // a test's own temporary file
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		junk := []byte(strings.Repeat("not a database page ", 400))
		// Keep the header page, so the file still opens, and break the rest.
		if _, err := f.WriteAt(junk, min(4096, info.Size()/2)); err != nil {
			t.Fatal(err)
		}
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from) //nolint:gosec // a test's own temporary file
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(to) //nolint:gosec // a test's own temporary file
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func equalCounts(a, b Counts) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
