package providers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNewExchangeProvider_Binance verifies creating a Binance provider.
func TestNewExchangeProvider_Binance(t *testing.T) {
	provider, err := NewExchangeProvider("binance", "key", "secret")
	assert.NoError(t, err)
	assert.NotNil(t, provider)
	assert.Equal(t, "binance", provider.Exchange())
	assert.Equal(t, "exchange-binance", provider.Name())
	assert.NotNil(t, provider.Provider())
}

// TestNewExchangeProvider_Unsupported verifies error for unsupported exchange.
func TestNewExchangeProvider_Unsupported(t *testing.T) {
	provider, err := NewExchangeProvider("unsupported", "key", "secret")
	assert.Error(t, err)
	assert.Nil(t, provider)
	assert.Contains(t, err.Error(), "unsupported exchange")
}

// TestExchangeProvider_GetHistoricalData verifies legacy method returns error.
func TestExchangeProvider_GetHistoricalData(t *testing.T) {
	provider, _ := NewExchangeProvider("binance", "key", "secret")
	data, err := provider.GetHistoricalData("BTC/USD", nil, nil, "1d")
	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "use NewBinanceProvider directly")
}
