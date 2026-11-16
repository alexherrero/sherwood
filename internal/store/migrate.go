package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Migrations are numbered SQL files in migrations/, named NNNN_what_it_does.sql
// and numbered from 0001 with no gaps. They only ever move forward: a shipped
// migration is never edited or removed, and a change is a new file. Each one
// runs inside its own transaction together with the row that records it, so a
// migration is either wholly applied and recorded or not applied at all. That
// rules out statements SQLite refuses inside a transaction (VACUUM, changing
// the journal mode) and BEGIN or COMMIT in a migration file.
//
//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// migrationSource is a file system holding the migration files at its root.
type migrationSource = fs.FS

var migrationFiles = mustSub(embeddedMigrations, "migrations")

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// ErrMigration marks every failure to bring the database to the latest
// version. Start-up stops on it.
var ErrMigration = errors.New("store: migration failed")

var migrationName = regexp.MustCompile(`^([0-9]{4})_[a-z0-9_]+\.sql$`)

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads, checks and orders the migration files.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var ms []migration
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			return nil, fmt.Errorf("migration file %q is not named NNNN_name.sql", e.Name())
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("migration file %q: %w", e.Name(), err)
		}
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", e.Name(), err)
		}
		if strings.TrimSpace(string(body)) == "" {
			return nil, fmt.Errorf("migration file %q is empty", e.Name())
		}
		ms = append(ms, migration{version: version, name: e.Name(), sql: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration %q: want version %04d; versions must run from 0001 with no gaps or repeats", m.name, i+1)
		}
	}
	return ms, nil
}

// appliedMigrations returns the names of the recorded migrations by version,
// or none when the database is new.
func appliedMigrations(ctx context.Context, db *sql.DB) (map[int]string, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&n)
	if err != nil {
		return nil, fmt.Errorf("look for schema_migrations: %w", err)
	}
	applied := map[int]string{}
	if n == 0 {
		return applied, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT version, name FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v int
		var name string
		if err := rows.Scan(&v, &name); err != nil {
			return nil, fmt.Errorf("read schema_migrations: %w", err)
		}
		applied[v] = name
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	return applied, nil
}

// migrate applies every migration in fsys the database has not yet recorded,
// in order, each in its own transaction, and returns the names it applied.
// It refuses a database that records a migration this build does not have,
// since that database was made by a newer build.
func migrate(ctx context.Context, db *sql.DB, fsys fs.FS, now time.Time) ([]string, error) {
	ms, err := loadMigrations(fsys)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMigration, err)
	}
	applied, err := appliedMigrations(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMigration, err)
	}
	for v := 1; v <= len(applied); v++ {
		name, ok := applied[v]
		if !ok {
			return nil, fmt.Errorf("%w: database records %d migrations but not version %04d", ErrMigration, len(applied), v)
		}
		if v > len(ms) {
			return nil, fmt.Errorf("%w: database records migration %q, which this build does not have; it was made by a newer build", ErrMigration, name)
		}
		if ms[v-1].name != name {
			return nil, fmt.Errorf("%w: database records version %04d as %q but this build has %q", ErrMigration, v, name, ms[v-1].name)
		}
	}

	var done []string
	for _, m := range ms[len(applied):] {
		if err := applyMigration(ctx, db, m, now); err != nil {
			return done, fmt.Errorf("%w: %s: %w", ErrMigration, m.name, err)
		}
		done = append(done, m.name)
	}
	return done, nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration, now time.Time) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, rollback(tx))
		}
	}()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, FormatTime(now)); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func rollback(tx *sql.Tx) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("rollback: %w", err)
	}
	return nil
}
