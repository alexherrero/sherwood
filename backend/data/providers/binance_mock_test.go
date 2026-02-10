package providers

import (
	"testing"
	"time"

	binance "github.com/adshao/go-binance/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockBinanceAPI implements BinanceAPI interface for testing
type MockBinanceAPI struct {
	mock.Mock
}

func (m *MockBinanceAPI) GetKlines(symbol, interval string, start, end int64, limit int) ([]*binance.Kline, error) {
	args := m.Called(symbol, interval, start, end, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*binance.Kline), args.Error(1)
}

func (m *MockBinanceAPI) GetPrices(symbol string) ([]*binance.SymbolPrice, error) {
	args := m.Called(symbol)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*binance.SymbolPrice), args.Error(1)
}

func (m *MockBinanceAPI) GetExchangeInfo(symbol string) (*binance.ExchangeInfo, error) {
	args := m.Called(symbol)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*binance.ExchangeInfo), args.Error(1)
}

func TestBinanceProvider_GetHistoricalData_Mock(t *testing.T) {
	mockAPI := new(MockBinanceAPI)
	p := NewBinanceProvider("", "")
	p.api = mockAPI

	start := time.UnixMilli(1600000000000)
	end := time.UnixMilli(1600003600000) // +1 hour

	expectedKlines := []*binance.Kline{
		{
			OpenTime: 1600000000000,
			Open:     "100.0",
			High:     "110.0",
			Low:      "90.0",
			Close:    "105.0",
			Volume:   "1000.0",
		},
	}

	mockAPI.On("GetKlines", "BTCUSDT", "1h", start.UnixMilli(), end.UnixMilli(), 1000).
		Return(expectedKlines, nil)

	data, err := p.GetHistoricalData("BTC/USD", start, end, "1h")
	require.NoError(t, err)
	assert.Len(t, data, 1)
	assert.Equal(t, 105.0, data[0].Close)

	mockAPI.AssertExpectations(t)
}

func TestBinanceProvider_GetLatestPrice_Mock(t *testing.T) {
	mockAPI := new(MockBinanceAPI)
	p := NewBinanceProvider("", "")
	p.api = mockAPI

	expectedPrices := []*binance.SymbolPrice{
		{Symbol: "BTCUSDT", Price: "50000.0"},
	}

	mockAPI.On("GetPrices", "BTCUSDT").Return(expectedPrices, nil)

	price, err := p.GetLatestPrice("BTC/USD")
	require.NoError(t, err)
	assert.Equal(t, 50000.0, price)
}

func TestBinanceProvider_GetTicker_Mock(t *testing.T) {
	mockAPI := new(MockBinanceAPI)
	p := NewBinanceProvider("", "")
	p.api = mockAPI

	baseAsset := "BTC"
	quoteAsset := "USDT"
	expectedInfo := &binance.ExchangeInfo{
		Symbols: []binance.Symbol{
			{
				Symbol:     "BTCUSDT",
				BaseAsset:  baseAsset,
				QuoteAsset: quoteAsset,
			},
		},
	}

	mockAPI.On("GetExchangeInfo", "BTCUSDT").Return(expectedInfo, nil)

	ticker, err := p.GetTicker("BTC/USD")
	require.NoError(t, err)
	assert.Equal(t, "BTC/USD", ticker.Symbol)
	assert.Equal(t, "BTC/USDT", ticker.Name)
	assert.Equal(t, "crypto", ticker.AssetType)
}
