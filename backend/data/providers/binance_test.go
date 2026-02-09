package providers

import (
	"os"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBinanceProvider_Name verifies the provider name.
func TestBinanceProvider_Name(t *testing.T) {
	p := NewBinanceProvider("", "")
	assert.Equal(t, "binance", p.Name())
}

// TestConvertSymbol verifies symbol conversion to Binance format.
func TestConvertSymbol(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"BTC/USD", "BTCUSDT"},
		{"ETH/USDT", "ETHUSDT"},
		{"BTC/USDT", "BTCUSDT"},
		{"ETH/BTC", "ETHBTC"},
		{"SOL/USD", "SOLUSDT"},
		{"btc/usd", "BTCUSDT"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := convertSymbol(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestMapBinanceInterval verifies interval mapping for Binance.
func TestMapBinanceInterval(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"1m", "1m", false},
		{"5m", "5m", false},
		{"15m", "15m", false},
		{"1h", "1h", false},
		{"4h", "4h", false},
		{"1d", "1d", false},
		{"1w", "1w", false},
		{"1wk", "1w", false},
		{"1M", "1M", false},
		{"1mo", "1M", false},
		{"invalid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := mapBinanceInterval(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

// TestBinanceProvider_GetHistoricalData_InvalidInterval tests error handling.
func TestBinanceProvider_GetHistoricalData_InvalidInterval(t *testing.T) {
	p := NewBinanceProvider("", "")
	start := time.Now().AddDate(0, 0, -7)
	end := time.Now()

	_, err := p.GetHistoricalData("BTC/USD", start, end, "invalid_interval")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported interval")
}

// TestNewExchangeProvider verifies exchange provider factory.
func TestNewExchangeProvider(t *testing.T) {
	// Valid exchange
	ep, err := NewExchangeProvider("binance", "", "")
	require.NoError(t, err)
	assert.NotNil(t, ep)
	assert.Equal(t, "binance", ep.Exchange())

	// Invalid exchange
	_, err = NewExchangeProvider("unsupported", "", "")
	assert.Error(t, err)
}

// Integration tests - skipped by default, run with: go test -tags=integration
//

// TestBinanceProvider_GetHistoricalData_Integration tests real Binance API.
// Uses Binance.US for US-based users.
func TestBinanceProvider_GetHistoricalData_Integration(t *testing.T) {
	apiKey := os.Getenv("BINANCE_API_KEY")
	apiSecret := os.Getenv("BINANCE_API_SECRET")

	var p data.DataProvider
	if apiKey == "" || apiSecret == "" {
		t.Log("INFO: BINANCE_API_KEY or BINANCE_API_SECRET not set. Using MockProvider. Set these env vars to run full integration tests.")
		p = NewMockProvider()
	} else {
		p = NewBinanceUSProvider(apiKey, apiSecret)
	}

	end := time.Now()
	start := end.AddDate(0, 0, -7) // Last 7 days

	data, err := p.GetHistoricalData("BTC/USD", start, end, "1h")
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// If using mock, we expect fewer candles
	if p.Name() == "mock" {
		assert.Equal(t, 2, len(data))
		return
	}

	// Should have approximately 168 candles (7 days * 24 hours)
	assert.True(t, len(data) > 100, "Expected at least 100 hourly candles for 7 days")

	// Verify data structure
	for _, ohlcv := range data {
		assert.Equal(t, "BTC/USD", ohlcv.Symbol)
		assert.True(t, ohlcv.Open > 0)
		assert.True(t, ohlcv.High >= ohlcv.Low)
		assert.True(t, ohlcv.Close > 0)
		assert.True(t, ohlcv.Volume >= 0)
	}
}

// TestBinanceProvider_GetLatestPrice_Integration tests real price API.
func TestBinanceProvider_GetLatestPrice_Integration(t *testing.T) {
	apiKey := os.Getenv("BINANCE_API_KEY")
	apiSecret := os.Getenv("BINANCE_API_SECRET")

	var p data.DataProvider
	if apiKey == "" || apiSecret == "" {
		t.Log("INFO: BINANCE_API_KEY or BINANCE_API_SECRET not set. Using MockProvider. Set these env vars to run full integration tests.")
		p = NewMockProvider()
	} else {
		p = NewBinanceUSProvider(apiKey, apiSecret)
	}

	price, err := p.GetLatestPrice("BTC/USD")
	require.NoError(t, err)
	assert.True(t, price > 0, "Expected positive price for BTC")
}

// TestBinanceProvider_GetTicker_Integration tests real ticker info API.
func TestBinanceProvider_GetTicker_Integration(t *testing.T) {
	apiKey := os.Getenv("BINANCE_API_KEY")
	apiSecret := os.Getenv("BINANCE_API_SECRET")

	var p data.DataProvider
	if apiKey == "" || apiSecret == "" {
		t.Log("INFO: BINANCE_API_KEY or BINANCE_API_SECRET not set. Using MockProvider. Set these env vars to run full integration tests.")
		p = NewMockProvider()
	} else {
		p = NewBinanceUSProvider(apiKey, apiSecret)
	}

	ticker, err := p.GetTicker("BTC/USD")
	require.NoError(t, err)
	assert.Equal(t, "BTC/USD", ticker.Symbol)

	if p.Name() == "mock" {
		assert.Equal(t, "Mock Asset", ticker.Name)
		return
	}

	assert.Equal(t, "crypto", ticker.AssetType)
	assert.Equal(t, "binance", ticker.Exchange)
}

// TestBinanceProvider_Pagination_Integration tests large date range pagination.
func TestBinanceProvider_Pagination_Integration(t *testing.T) {
	apiKey := os.Getenv("BINANCE_API_KEY")
	apiSecret := os.Getenv("BINANCE_API_SECRET")

	var p data.DataProvider
	if apiKey == "" || apiSecret == "" {
		t.Log("INFO: BINANCE_API_KEY or BINANCE_API_SECRET not set. Using MockProvider. Set these env vars to run full integration tests.")
		p = NewMockProvider()
	} else {
		p = NewBinanceUSProvider(apiKey, apiSecret)
	}

	end := time.Now()
	start := end.AddDate(0, -2, 0) // Last 2 months

	data, err := p.GetHistoricalData("BTC/USD", start, end, "1h")
	require.NoError(t, err)

	if p.Name() == "mock" {
		assert.NotEmpty(t, data)
		return
	}

	// Should have many more than 1000 candles (proving pagination works)
	// 2 months * 30 days * 24 hours = ~1440 candles
	assert.True(t, len(data) > 1000, "Expected more than 1000 candles (pagination test)")
}
