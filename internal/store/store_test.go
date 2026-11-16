package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

var (
	t0 = time.Date(2026, 11, 16, 14, 30, 0, 0, time.UTC)
	t1 = t0.Add(90 * time.Second)
)

func dbPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "sherwood.db")
}

func mustOpen(t *testing.T, path string, migrations migrationSource) *Store {
	t.Helper()
	s, err := open(context.Background(), path, migrations, t0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func latestVersion(t *testing.T) int {
	t.Helper()
	ms, err := loadMigrations(migrationFiles)
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}
	return len(ms)
}

func versions(t *testing.T, db *sql.DB) []int {
	t.Helper()
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var vs []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		vs = append(vs, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return vs
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestFreshDatabaseMigratesToLatest(t *testing.T) {
	s := mustOpen(t, dbPath(t), migrationFiles)

	latest := latestVersion(t)
	var want []int
	for v := 1; v <= latest; v++ {
		want = append(want, v)
	}
	if got := versions(t, s.Reader()); !slices.Equal(got, want) {
		t.Fatalf("recorded versions = %v, want %v", got, want)
	}
	if got := len(s.Migrated()); got != latest {
		t.Fatalf("Migrated() has %d entries, want %d", got, latest)
	}
	for _, table := range []string{"schema_migrations", "job_runs", "settings_changes"} {
		if !tableExists(t, s.Reader(), table) {
			t.Errorf("table %s missing after migrating", table)
		}
	}
	var applied string
	if err := s.Reader().QueryRow(`SELECT applied_at FROM schema_migrations WHERE version = 1`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != FormatTime(t0) {
		t.Errorf("applied_at = %q, want the time passed in, %q", applied, FormatTime(t0))
	}
}

func TestSecondRunAppliesNothing(t *testing.T) {
	path := dbPath(t)
	first := mustOpen(t, path, migrationFiles)
	before := versions(t, first.Reader())
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := open(context.Background(), path, migrationFiles, t1)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if got := s.Migrated(); len(got) != 0 {
		t.Fatalf("second open applied %v, want nothing", got)
	}
	if got := versions(t, s.Reader()); !slices.Equal(got, before) {
		t.Fatalf("versions after second open = %v, want %v", got, before)
	}
	var n int
	if err := s.Reader().QueryRow(`SELECT count(*) FROM schema_migrations WHERE applied_at = ?`, FormatTime(t1)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d migrations re-recorded on the second open", n)
	}
}

func TestBrokenMigrationRollsBackAndStopsStartUp(t *testing.T) {
	path := dbPath(t)
	initSQL := mustReadInit(t)
	// 0002 creates a table, writes to it, then fails part-way. None of it may
	// survive.
	broken := fstest.MapFS{
		"0001_init.sql": {Data: initSQL},
		"0002_broken.sql": {Data: []byte(`
CREATE TABLE half_built (id INTEGER PRIMARY KEY) STRICT;
INSERT INTO half_built (id) VALUES (1);
INSERT INTO no_such_table (id) VALUES (1);
`)},
	}

	s, err := open(context.Background(), path, broken, t0)
	if err == nil {
		_ = s.Close()
		t.Fatal("open with a broken migration succeeded; start-up must stop")
	}
	if !errors.Is(err, ErrMigration) {
		t.Fatalf("error %v does not wrap ErrMigration", err)
	}
	if !strings.Contains(err.Error(), "0002_broken.sql") {
		t.Errorf("error %q does not name the failing migration", err)
	}

	// Reopen with the good migrations only: 0001 stayed applied, 0002 left
	// no trace.
	good := mustOpen(t, path, fstest.MapFS{"0001_init.sql": {Data: initSQL}})
	if got := good.Migrated(); len(got) != 0 {
		t.Fatalf("reopen applied %v; 0001 should have stayed applied", got)
	}
	if got := versions(t, good.Reader()); !slices.Equal(got, []int{1}) {
		t.Fatalf("versions = %v, want [1]", got)
	}
	if tableExists(t, good.Reader(), "half_built") {
		t.Fatal("table from the broken migration survived the rollback")
	}
}

func TestRefusesDatabaseFromNewerBuild(t *testing.T) {
	path := dbPath(t)
	newer := fstest.MapFS{
		"0001_init.sql":  {Data: mustReadInit(t)},
		"0002_extra.sql": {Data: []byte(`CREATE TABLE extra (id INTEGER PRIMARY KEY) STRICT;`)},
	}
	s := mustOpen(t, path, newer)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := open(context.Background(), path, fstest.MapFS{"0001_init.sql": {Data: mustReadInit(t)}}, t1)
	if err == nil {
		_ = s.Close()
		t.Fatal("an older build opened a database a newer build had migrated")
	}
	if !errors.Is(err, ErrMigration) || !strings.Contains(err.Error(), "0002_extra.sql") {
		t.Fatalf("error = %v, want ErrMigration naming 0002_extra.sql", err)
	}
}

func TestRejectsBadMigrationFiles(t *testing.T) {
	initSQL := mustReadInit(t)
	tests := map[string]fstest.MapFS{
		"gap": {
			"0001_init.sql": {Data: initSQL},
			"0003_skip.sql": {Data: []byte(`SELECT 1;`)},
		},
		"repeat": {
			"0001_init.sql":  {Data: initSQL},
			"0001_again.sql": {Data: []byte(`SELECT 1;`)},
		},
		"bad name": {
			"0001_init.sql": {Data: initSQL},
			"0002-dash.sql": {Data: []byte(`SELECT 1;`)},
		},
		"empty": {
			"0001_init.sql":  {Data: initSQL},
			"0002_empty.sql": {Data: []byte("  \n")},
		},
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			s, err := open(context.Background(), dbPath(t), fsys, t0)
			if err == nil {
				_ = s.Close()
				t.Fatal("open succeeded")
			}
			if !errors.Is(err, ErrMigration) {
				t.Fatalf("error %v does not wrap ErrMigration", err)
			}
		})
	}
}

func mustReadInit(t *testing.T) []byte {
	t.Helper()
	b, err := fs.ReadFile(migrationFiles, "0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPragmas(t *testing.T) {
	s := mustOpen(t, dbPath(t), migrationFiles)
	for _, db := range []*sql.DB{s.Writer(), s.Reader()} {
		var mode string
		var fk, busy int
		if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if mode != "wal" || fk != 1 || busy != busyTimeoutMillis {
			t.Errorf("journal_mode=%q foreign_keys=%d busy_timeout=%d; want wal, 1, %d", mode, fk, busy, busyTimeoutMillis)
		}
	}
	if got := s.Writer().Stats().MaxOpenConnections; got != 1 {
		t.Errorf("writer pool allows %d connections, want 1", got)
	}
}

func TestReaderRefusesWrites(t *testing.T) {
	s := mustOpen(t, dbPath(t), migrationFiles)
	_, err := s.Reader().Exec(`INSERT INTO job_runs (job_name, trading_date, input_hash, started_at) VALUES ('x', '2026-11-16', 'h', ?)`, FormatTime(t0))
	if err == nil {
		t.Fatal("a write through the reader pool succeeded")
	}
}

func TestWritersSerialize(t *testing.T) {
	s := mustOpen(t, dbPath(t), migrationFiles)
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Writer().Exec(`INSERT INTO job_runs (job_name, trading_date, input_hash, started_at) VALUES (?, '2026-11-16', 'h', ?)`,
				fmt.Sprintf("job%02d", i), FormatTime(t0))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent write failed: %v", err)
		}
	}
	var count int
	if err := s.Reader().QueryRow(`SELECT count(*) FROM job_runs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != n {
		t.Fatalf("job_runs has %d rows, want %d", count, n)
	}
}

func TestPathWithURICharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "odd?name#with%chars.db")
	s := mustOpen(t, path, migrationFiles)
	if !tableExists(t, s.Reader(), "job_runs") {
		t.Fatal("job_runs missing")
	}
}

func TestNoRealColumns(t *testing.T) {
	s := mustOpen(t, dbPath(t), migrationFiles)
	rows, err := s.Reader().Query(`
SELECT m.name, p.name, p.type
FROM sqlite_schema AS m, pragma_table_info(m.name) AS p
WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var table, col, typ string
		if err := rows.Scan(&table, &col, &typ); err != nil {
			t.Fatal(err)
		}
		// Only INTEGER and TEXT are allowed: REAL, FLOAT, DOUBLE, NUMERIC,
		// DECIMAL and an untyped column could all hold a float.
		if typ != "INTEGER" && typ != "TEXT" {
			t.Errorf("%s.%s has type %q; use TEXT for money and decimals, INTEGER for counts and keys", table, col, typ)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	var notStrict []string
	trows, err := s.Reader().Query(`SELECT name FROM pragma_table_list WHERE schema = 'main' AND type = 'table' AND strict = 0 AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = trows.Close() }()
	for trows.Next() {
		var name string
		if err := trows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		notStrict = append(notStrict, name)
	}
	if err := trows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(notStrict) > 0 {
		t.Errorf("tables not declared STRICT: %v", notStrict)
	}
}

func TestJobRunsOneRunPerJobAndTradingDate(t *testing.T) {
	s := mustOpen(t, dbPath(t), migrationFiles)
	w := s.Writer()
	ins := `INSERT INTO job_runs (job_name, trading_date, input_hash, started_at) VALUES (?, ?, ?, ?)`
	if _, err := w.Exec(ins, "signals", "2026-11-16", "abc", FormatTime(t0)); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if _, err := w.Exec(ins, "signals", "2026-11-16", "abc", FormatTime(t1)); err == nil {
		t.Fatal("a second run of the same job on the same trading date was accepted")
	}
	if _, err := w.Exec(ins, "signals", "2026-11-17", "abc", FormatTime(t1)); err != nil {
		t.Fatalf("same job, next trading date: %v", err)
	}
	if _, err := w.Exec(ins, "prices", "2026-11-16", "abc", FormatTime(t1)); err != nil {
		t.Fatalf("other job, same trading date: %v", err)
	}

	if _, err := w.Exec(`UPDATE job_runs SET outcome = 'succeeded', finished_at = ? WHERE job_name = 'signals' AND trading_date = '2026-11-16'`, FormatTime(t1)); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	var outcome, finished string
	if err := s.Reader().QueryRow(`SELECT outcome, finished_at FROM job_runs WHERE job_name = 'signals' AND trading_date = '2026-11-16'`).Scan(&outcome, &finished); err != nil {
		t.Fatal(err)
	}
	if got, err := ParseTime(finished); err != nil || !got.Equal(t1) {
		t.Fatalf("finished_at = %q (%v), want %v", finished, err, t1)
	}
}

func TestJobRunsRejectsBadRows(t *testing.T) {
	s := mustOpen(t, dbPath(t), migrationFiles)
	tests := map[string]struct {
		query string
		args  []any
	}{
		"date not in layout": {
			`INSERT INTO job_runs (job_name, trading_date, input_hash, started_at) VALUES ('j', '11/16/2026', 'h', ?)`,
			[]any{FormatTime(t0)},
		},
		"time not in layout": {
			`INSERT INTO job_runs (job_name, trading_date, input_hash, started_at) VALUES ('j', '2026-11-16', 'h', ?)`,
			[]any{t0.Format(time.RFC3339)},
		},
		"finished without outcome": {
			`INSERT INTO job_runs (job_name, trading_date, input_hash, started_at, finished_at) VALUES ('j', '2026-11-16', 'h', ?, ?)`,
			[]any{FormatTime(t0), FormatTime(t1)},
		},
		"outcome without finish": {
			`INSERT INTO job_runs (job_name, trading_date, input_hash, started_at, outcome) VALUES ('j', '2026-11-16', 'h', ?, 'failed')`,
			[]any{FormatTime(t0)},
		},
		"unknown outcome": {
			`INSERT INTO job_runs (job_name, trading_date, input_hash, started_at, finished_at, outcome) VALUES ('j', '2026-11-16', 'h', ?, ?, 'maybe')`,
			[]any{FormatTime(t0), FormatTime(t1)},
		},
		"finished before started": {
			`INSERT INTO job_runs (job_name, trading_date, input_hash, started_at, finished_at, outcome) VALUES ('j', '2026-11-16', 'h', ?, ?, 'failed')`,
			[]any{FormatTime(t1), FormatTime(t0)},
		},
		"missing input hash": {
			`INSERT INTO job_runs (job_name, trading_date, input_hash, started_at) VALUES ('j', '2026-11-16', '', ?)`,
			[]any{FormatTime(t0)},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Writer().Exec(tc.query, tc.args...); err == nil {
				t.Fatal("bad row accepted")
			}
		})
	}
}

func TestSettingsChangesAppendOnly(t *testing.T) {
	s := mustOpen(t, dbPath(t), migrationFiles)
	w := s.Writer()
	ins := `INSERT INTO settings_changes (changed_at, setting, old_value, new_value, source) VALUES (?, ?, ?, ?, ?)`
	if _, err := w.Exec(ins, FormatTime(t0), "limits.max_daily_loss", "500.00", "250.00", "phone"); err != nil {
		t.Fatalf("tighten from phone: %v", err)
	}
	if _, err := w.Exec(ins, FormatTime(t1), "limits.max_daily_loss", "250.00", "400.00", "nas"); err != nil {
		t.Fatalf("edit on NAS: %v", err)
	}
	if _, err := w.Exec(ins, FormatTime(t1), "limits.max_open_positions", nil, "10", "nas"); err != nil {
		t.Fatalf("new setting: %v", err)
	}

	// The value comes back exactly as written: text, not a number.
	var v string
	if err := s.Reader().QueryRow(`SELECT new_value FROM settings_changes WHERE source = 'phone'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "250.00" {
		t.Fatalf("new_value = %q, want 250.00", v)
	}

	if _, err := w.Exec(`UPDATE settings_changes SET new_value = '1'`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("update: err = %v, want append-only refusal", err)
	}
	if _, err := w.Exec(`DELETE FROM settings_changes`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("delete: err = %v, want append-only refusal", err)
	}
	for name, args := range map[string][]any{
		"unknown source": {FormatTime(t0), "limits.x", "1", "2", "api"},
		"no change":      {FormatTime(t0), "limits.x", "1", "1", "phone"},
		"empty setting":  {FormatTime(t0), "", "1", "2", "phone"},
		"bad time":       {"yesterday", "limits.x", "1", "2", "phone"},
	} {
		if _, err := w.Exec(ins, args...); err == nil {
			t.Errorf("%s: bad row accepted", name)
		}
	}
}

func TestTimeFormat(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tz data: %v", err)
	}
	in := time.Date(2026, 3, 8, 9, 30, 0, 5, ny)
	got := FormatTime(in)
	if got != "2026-03-08T13:30:00.000000005Z" || len(got) != 30 {
		t.Fatalf("FormatTime = %q", got)
	}
	back, err := ParseTime(got)
	if err != nil || !back.Equal(in) {
		t.Fatalf("ParseTime(%q) = %v, %v; want %v", got, back, err, in)
	}
	if FormatTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) >= FormatTime(time.Date(2026, 1, 1, 0, 0, 0, 1, time.UTC)) {
		t.Fatal("text order differs from time order")
	}
	if d := FormatDate(time.Date(2026, 11, 16, 23, 0, 0, 0, ny)); d != "2026-11-16" {
		t.Fatalf("FormatDate = %q, want the date in the given location", d)
	}
	if _, err := ParseTime("2026-11-16T14:30:00Z"); err == nil {
		t.Fatal("ParseTime accepted a time not in the fixed layout")
	}
}
