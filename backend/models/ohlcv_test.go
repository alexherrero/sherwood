package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOHLCV_JSON verifies JSON marshaling of OHLCV.
func TestOHLCV_JSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	ohlcv := OHLCV{
		Timestamp: now,
		Symbol:    "AAPL",
		Open:      150.0,
		High:      155.0,
		Low:       149.0,
		Close:     154.0,
		Volume:    1000000,
	}

	data, err := json.Marshal(ohlcv)
	require.NoError(t, err)

	var parsed OHLCV
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, ohlcv.Symbol, parsed.Symbol)
	assert.Equal(t, ohlcv.Close, parsed.Close)
	assert.True(t, ohlcv.Timestamp.Equal(parsed.Timestamp))
}

// TestTicker_JSON verifies JSON marshaling of Ticker.
func TestTicker_JSON(t *testing.T) {
	ticker := Ticker{
		Symbol:    "AAPL",
		Name:      "Apple Inc.",
		AssetType: "stock",
		Exchange:  "NASDAQ",
	}

	data, err := json.Marshal(ticker)
	require.NoError(t, err)

	var parsed Ticker
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, ticker.Symbol, parsed.Symbol)
	assert.Equal(t, ticker.Name, parsed.Name)
}
