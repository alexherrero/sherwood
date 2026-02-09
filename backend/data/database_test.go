package data

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewDB verifies database creation and migration.
func TestNewDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	assert.NotNil(t, db)

	// Verify file was created
	_, err = os.Stat(dbPath)
	assert.NoError(t, err)
}

// TestNewDB_CreatesDirectory verifies directory creation.
func TestNewDB_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "nested", "path", "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Verify nested directory was created
	_, err = os.Stat(filepath.Dir(dbPath))
	assert.NoError(t, err)
}

// TestDB_Migrate verifies schema creation.
func TestDB_Migrate(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Verify tables exist by querying them
	var count int
	err = db.Get(&count, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('ohlcv', 'tickers', 'orders', 'trades', 'positions')")
	require.NoError(t, err)
	assert.Equal(t, 5, count) // All 5 tables should exist
}

// TestDB_SaveOHLCV verifies saving OHLCV data.
func TestDB_SaveOHLCV(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	data := []models.OHLCV{
		{
			Symbol:    "AAPL",
			Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Open:      150.0,
			High:      155.0,
			Low:       149.0,
			Close:     154.0,
			Volume:    1000000,
		},
		{
			Symbol:    "AAPL",
			Timestamp: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
			Open:      154.0,
			High:      158.0,
			Low:       153.0,
			Close:     157.0,
			Volume:    1200000,
		},
	}

	err = db.SaveOHLCV(data)
	require.NoError(t, err)

	// Verify data was saved
	var count int
	err = db.Get(&count, "SELECT COUNT(*) FROM ohlcv WHERE symbol = 'AAPL'")
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

// TestDB_SaveOHLCV_Upsert verifies upsert behavior.
func TestDB_SaveOHLCV_Upsert(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	timestamp := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Insert original
	_ = db.SaveOHLCV([]models.OHLCV{{
		Symbol:    "AAPL",
		Timestamp: timestamp,
		Close:     150.0,
	}})

	// Update with same symbol/timestamp
	_ = db.SaveOHLCV([]models.OHLCV{{
		Symbol:    "AAPL",
		Timestamp: timestamp,
		Close:     160.0, // Different close
	}})

	// Should still have only 1 record
	var count int
	_ = db.Get(&count, "SELECT COUNT(*) FROM ohlcv WHERE symbol = 'AAPL'")
	assert.Equal(t, 1, count)
}

// TestDB_GetOHLCV verifies retrieving OHLCV data.
func TestDB_GetOHLCV(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Insert test data
	data := []models.OHLCV{
		{Symbol: "AAPL", Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Close: 150},
		{Symbol: "AAPL", Timestamp: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Close: 155},
		{Symbol: "AAPL", Timestamp: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC), Close: 160},
		{Symbol: "GOOGL", Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Close: 140}, // Different symbol
	}
	_ = db.SaveOHLCV(data)

	// Query range
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	result, err := db.GetOHLCV("AAPL", start, end)
	require.NoError(t, err)
	assert.Len(t, result, 2) // Should only get 2 AAPL records in range
}

// TestDB_GetOHLCV_Empty verifies empty result.
func TestDB_GetOHLCV_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	result, err := db.GetOHLCV("NONEXISTENT", start, end)
	require.NoError(t, err)
	assert.Empty(t, result)
}

// TestDB_GetOHLCV_Ordered verifies results are ordered by timestamp.
func TestDB_GetOHLCV_Ordered(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Insert out of order
	data := []models.OHLCV{
		{Symbol: "AAPL", Timestamp: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC), Close: 160},
		{Symbol: "AAPL", Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), Close: 150},
		{Symbol: "AAPL", Timestamp: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Close: 155},
	}
	_ = db.SaveOHLCV(data)

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)

	result, err := db.GetOHLCV("AAPL", start, end)
	require.NoError(t, err)
	require.Len(t, result, 3)

	// Should be ordered by timestamp ASC
	assert.Equal(t, 150.0, result[0].Close)
	assert.Equal(t, 155.0, result[1].Close)
	assert.Equal(t, 160.0, result[2].Close)
}
