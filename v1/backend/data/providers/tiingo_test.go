package providers

import (
	"os"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTiingoProvider_Name verifies the provider name.
func TestTiingoProvider_Name(t *testing.T) {
	p := NewTiingoProvider("test-key")
	assert.Equal(t, "tiingo", p.Name())
}

// TestTiingoProvider_RequiresAPIKey verifies API key requirement.
func TestTiingoProvider_RequiresAPIKey(t *testing.T) {
	p := NewTiingoProvider("")
	_, err := p.GetLatestPrice("AAPL")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "API key is required")
}

// TestTiingoProvider_UnsupportedInterval verifies interval validation.
func TestTiingoProvider_UnsupportedInterval(t *testing.T) {
	p := NewTiingoProvider("test-key")
	start := time.Now().AddDate(0, 0, -7)
	end := time.Now()

	_, err := p.GetHistoricalData("AAPL", start, end, "1h")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "only supports daily interval")
}

// Integration tests - require TIINGO_API_KEY environment variable
// Get a free API key at: https://www.tiingo.com/

// TestTiingoProvider_GetHistoricalData_Integration tests real API calls.
func TestTiingoProvider_GetHistoricalData_Integration(t *testing.T) {
	apiKey := os.Getenv("TIINGO_API_KEY")

	var p data.DataProvider
	if apiKey == "" {
		t.Log("INFO: TIINGO_API_KEY not set. Using MockProvider. Set this env var to run full integration tests.")
		p = NewMockProvider()
	} else {
		p = NewTiingoProvider(apiKey)
	}

	end := time.Now()
	start := end.AddDate(0, 0, -30) // Last 30 days

	data, err := p.GetHistoricalData("AAPL", start, end, "1d")
	require.NoError(t, err)
	require.NotEmpty(t, data)

	if p.Name() == "mock" {
		assert.Equal(t, 2, len(data))
		return
	}

	// Verify we got data (may be ~20 trading days)
	assert.True(t, len(data) > 15, "Expected at least 15 trading days")

	// Verify data structure
	for _, ohlcv := range data {
		assert.Equal(t, "AAPL", ohlcv.Symbol)
		assert.True(t, ohlcv.Open > 0)
		assert.True(t, ohlcv.High >= ohlcv.Low)
		assert.True(t, ohlcv.Close > 0)
		assert.True(t, ohlcv.Volume >= 0)
	}
}

// TestTiingoProvider_GetLatestPrice_Integration tests real price API.
func TestTiingoProvider_GetLatestPrice_Integration(t *testing.T) {
	apiKey := os.Getenv("TIINGO_API_KEY")

	var p data.DataProvider
	if apiKey == "" {
		t.Log("INFO: TIINGO_API_KEY not set. Using MockProvider. Set this env var to run full integration tests.")
		p = NewMockProvider()
	} else {
		p = NewTiingoProvider(apiKey)
	}

	price, err := p.GetLatestPrice("AAPL")
	require.NoError(t, err)
	assert.True(t, price > 0, "Expected positive price for AAPL")
}

// TestTiingoProvider_GetTicker_Integration tests real ticker info API.
func TestTiingoProvider_GetTicker_Integration(t *testing.T) {
	apiKey := os.Getenv("TIINGO_API_KEY")

	var p data.DataProvider
	if apiKey == "" {
		t.Log("INFO: TIINGO_API_KEY not set. Using MockProvider. Set this env var to run full integration tests.")
		p = NewMockProvider()
	} else {
		p = NewTiingoProvider(apiKey)
	}

	ticker, err := p.GetTicker("AAPL")
	require.NoError(t, err)
	assert.Equal(t, "AAPL", ticker.Symbol)

	if p.Name() == "mock" {
		assert.Equal(t, "Mock Asset", ticker.Name)
		return
	}

	assert.Equal(t, "stock", ticker.AssetType)
	assert.NotEmpty(t, ticker.Name)
}
