package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	// The pure-Go SQLite driver keeps cgo out of the container build.
	_ "modernc.org/sqlite"
)

// busyTimeoutMillis is how long a connection waits on a lock held by another
// connection before giving up with SQLITE_BUSY.
const busyTimeoutMillis = 5000

// Store holds the two handles on the one SQLite file: a writer pool limited to
// a single connection, so the app is the database's one writer, and a reader
// pool that write-ahead mode lets run alongside it.
type Store struct {
	write    *sql.DB
	read     *sql.DB
	migrated []string
}

// Open opens (creating if needed) the database at path in write-ahead mode,
// applies every pending migration, and returns the store. now is the time
// recorded against each migration it applies; the store never reads the wall
// clock itself.
//
// An error from Open means the database is not at the version this build
// expects, and start-up must stop.
func Open(ctx context.Context, path string, now time.Time) (*Store, error) {
	return open(ctx, path, migrationFiles, now)
}

func open(ctx context.Context, path string, migrations migrationSource, now time.Time) (*Store, error) {
	writeDSN, readDSN, err := dsns(path)
	if err != nil {
		return nil, err
	}

	write, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		return nil, fmt.Errorf("store: open writer: %w", err)
	}
	// One connection, kept for the life of the process: every write goes
	// through it, so writes are serialized in the app rather than racing for
	// SQLite's lock.
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	write.SetConnMaxLifetime(0)
	write.SetConnMaxIdleTime(0)

	s := &Store{write: write}
	if err := s.checkJournalMode(ctx); err != nil {
		_ = write.Close()
		return nil, err
	}

	s.migrated, err = migrate(ctx, write, migrations, now)
	if err != nil {
		_ = write.Close()
		return nil, err
	}

	// The readers open after migrating, so they never see a half-built schema.
	read, err := sql.Open("sqlite", readDSN)
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("store: open readers: %w", err)
	}
	if err := read.PingContext(ctx); err != nil {
		_ = write.Close()
		_ = read.Close()
		return nil, fmt.Errorf("store: open readers: %w", err)
	}
	s.read = read
	return s, nil
}

// dsns builds the writer and reader connection strings. The path goes in as
// an escaped file: URI so characters such as '?' or '#' in it cannot be read
// as URI syntax.
func dsns(path string) (writeDSN, readDSN string, err error) {
	if path == "" {
		return "", "", errors.New("store: empty database path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("store: database path: %w", err)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}

	pragmas := func(extra ...string) string {
		q := url.Values{}
		q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMillis))
		q.Add("_pragma", "foreign_keys(1)")
		for _, p := range extra {
			k, v, _ := strings.Cut(p, "=")
			q.Add(k, v)
		}
		return q.Encode()
	}

	w := u
	// _txlock=immediate makes every transaction take the write lock at BEGIN,
	// so a transaction never fails to upgrade a read lock part-way through.
	w.RawQuery = pragmas("_pragma=journal_mode(WAL)", "_pragma=synchronous(NORMAL)", "_txlock=immediate")
	r := u
	r.RawQuery = pragmas("_pragma=query_only(1)")
	return w.String(), r.String(), nil
}

func (s *Store) checkJournalMode(ctx context.Context) error {
	var mode string
	if err := s.write.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("store: read journal mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("store: journal mode is %q, want wal", mode)
	}
	return nil
}

// Writer returns the single-connection pool every write must go through.
// Hold a transaction on it only as long as the write needs: while it is open,
// every other writer waits.
func (s *Store) Writer() *sql.DB { return s.write }

// Reader returns the read-only pool. Its connections refuse writes.
func (s *Store) Reader() *sql.DB { return s.read }

// Migrated lists the migrations Open applied, oldest first, by file name. It
// is empty when the database was already up to date.
func (s *Store) Migrated() []string {
	return append([]string(nil), s.migrated...)
}

// Close closes both pools.
func (s *Store) Close() error {
	var errs []error
	if s.read != nil {
		errs = append(errs, s.read.Close())
	}
	errs = append(errs, s.write.Close())
	return errors.Join(errs...)
}
