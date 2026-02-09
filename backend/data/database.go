// Package data provides database connection and OHLCV storage.
package data

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
	_ "modernc.org/sqlite"
)

// DB wraps the sqlx database connection.
type DB struct {
	*sqlx.DB
}

// NewDB creates a new database connection.
//
// Args:
//   - databasePath: Path to the SQLite database file
//
// Returns:
//   - *DB: Database wrapper
//   - error: Any error encountered
func NewDB(databasePath string) (*DB, error) {
	// Ensure the data directory exists
	dir := filepath.Dir(databasePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := sqlx.Connect("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	log.Info().Str("path", databasePath).Msg("Connected to database")

	wrapper := &DB{db}
	if err := wrapper.Migrate(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return wrapper, nil
}

// Migrate runs database migrations to ensure schema is up to date.
//
// Returns:
//   - error: Any error encountered during migration
func (db *DB) Migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS ohlcv (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		symbol TEXT NOT NULL,
		timestamp DATETIME NOT NULL,
		open REAL NOT NULL,
		high REAL NOT NULL,
		low REAL NOT NULL,
		close REAL NOT NULL,
		volume REAL NOT NULL,
		UNIQUE(symbol, timestamp)
	);

	CREATE INDEX IF NOT EXISTS idx_ohlcv_symbol ON ohlcv(symbol);
	CREATE INDEX IF NOT EXISTS idx_ohlcv_timestamp ON ohlcv(timestamp);

	CREATE TABLE IF NOT EXISTS tickers (
		symbol TEXT PRIMARY KEY,
		name TEXT,
		asset_type TEXT,
		exchange TEXT
	);

	CREATE TABLE IF NOT EXISTS orders (
		id TEXT PRIMARY KEY,
		symbol TEXT NOT NULL,
		side TEXT NOT NULL,
		type TEXT NOT NULL,
		quantity REAL NOT NULL,
		price REAL NOT NULL,
		status TEXT NOT NULL,
		filled_quantity REAL DEFAULT 0,
		average_price REAL DEFAULT 0,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS trades (
		id TEXT PRIMARY KEY,
		order_id TEXT NOT NULL,
		symbol TEXT NOT NULL,
		side TEXT NOT NULL,
		quantity REAL NOT NULL,
		price REAL NOT NULL,
		executed_at DATETIME NOT NULL,
		FOREIGN KEY (order_id) REFERENCES orders(id)
	);

	CREATE TABLE IF NOT EXISTS positions (
		symbol TEXT PRIMARY KEY,
		quantity REAL NOT NULL,
		average_cost REAL NOT NULL,
		updated_at DATETIME NOT NULL
	);
	`

	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("schema migration failed: %w", err)
	}

	log.Info().Msg("Database migrations complete")
	return nil
}

// SaveOHLCV stores OHLCV data in the database.
//
// Args:
//   - data: Slice of OHLCV records to store
//
// Returns:
//   - error: Any error encountered
func (db *DB) SaveOHLCV(data []models.OHLCV) error {
	query := `
		INSERT OR REPLACE INTO ohlcv (symbol, timestamp, open, high, low, close, volume)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	for _, d := range data {
		_, err := tx.Exec(query, d.Symbol, d.Timestamp, d.Open, d.High, d.Low, d.Close, d.Volume)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to insert OHLCV: %w", err)
		}
	}

	return tx.Commit()
}

// GetOHLCV retrieves OHLCV data from the database.
//
// Args:
//   - symbol: Ticker symbol
//   - start: Start of date range
//   - end: End of date range
//
// Returns:
//   - []models.OHLCV: Historical data
//   - error: Any error encountered
func (db *DB) GetOHLCV(symbol string, start, end time.Time) ([]models.OHLCV, error) {
	var data []models.OHLCV
	query := `
		SELECT symbol, timestamp, open, high, low, close, volume
		FROM ohlcv
		WHERE symbol = ? AND timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp ASC
	`
	err := db.Select(&data, query, symbol, start, end)
	if err != nil {
		return nil, fmt.Errorf("failed to query OHLCV: %w", err)
	}
	return data, nil
}
