package providers

import (
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	finance "github.com/piquette/finance-go"
	"github.com/piquette/finance-go/chart"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockYahooAPI implements YahooAPI interface for testing
type MockYahooAPI struct {
	mock.Mock
}

func (m *MockYahooAPI) GetQuote(symbol string) (*finance.Quote, error) {
	args := m.Called(symbol)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*finance.Quote), args.Error(1)
}

func (m *MockYahooAPI) GetChartData(params *chart.Params) ([]models.OHLCV, error) {
	args := m.Called(params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.OHLCV), args.Error(1)
}

func TestYahooProvider_GetHistoricalData_Mock(t *testing.T) {
	mockAPI := new(MockYahooAPI)
	p := NewYahooProvider()
	p.api = mockAPI

	start := time.Now().AddDate(0, 0, -5)
	end := time.Now()

	expectedData := []models.OHLCV{
		{Symbol: "AAPL", Close: 150.0},
		{Symbol: "AAPL", Close: 151.0},
	}

	// Expectation
	mockAPI.On("GetChartData", mock.MatchedBy(func(params *chart.Params) bool {
		return params.Symbol == "AAPL"
	})).Return(expectedData, nil)

	data, err := p.GetHistoricalData("AAPL", start, end, "1d")
	require.NoError(t, err)
	assert.Len(t, data, 2)
	assert.Equal(t, 150.0, data[0].Close)

	mockAPI.AssertExpectations(t)
}

func TestYahooProvider_GetLatestPrice_Mock(t *testing.T) {
	mockAPI := new(MockYahooAPI)
	p := NewYahooProvider()
	p.api = mockAPI

	expectedQuote := &finance.Quote{
		Symbol:             "AAPL",
		RegularMarketPrice: 155.5,
	}

	mockAPI.On("GetQuote", "AAPL").Return(expectedQuote, nil)

	price, err := p.GetLatestPrice("AAPL")
	require.NoError(t, err)
	assert.Equal(t, 155.5, price)

	mockAPI.AssertExpectations(t)
}

func TestYahooProvider_GetTicker_Mock(t *testing.T) {
	mockAPI := new(MockYahooAPI)
	p := NewYahooProvider()
	p.api = mockAPI

	expectedQuote := &finance.Quote{
		Symbol:           "AAPL",
		ShortName:        "Apple Inc",
		FullExchangeName: "NasdaqGS",
		QuoteType:        "EQUITY",
	}

	mockAPI.On("GetQuote", "AAPL").Return(expectedQuote, nil)

	ticker, err := p.GetTicker("AAPL")
	require.NoError(t, err)
	assert.Equal(t, "AAPL", ticker.Symbol)
	assert.Equal(t, "Apple Inc", ticker.Name)
	assert.Equal(t, "stock", ticker.AssetType)

	mockAPI.AssertExpectations(t)
}

func TestYahooProvider_GetTicker_Crypto_Mock(t *testing.T) {
	mockAPI := new(MockYahooAPI)
	p := NewYahooProvider()
	p.api = mockAPI

	expectedQuote := &finance.Quote{
		Symbol:           "BTC-USD",
		ShortName:        "Bitcoin USD",
		FullExchangeName: "CCC",
		QuoteType:        "CRYPTOCURRENCY",
	}

	mockAPI.On("GetQuote", "BTC-USD").Return(expectedQuote, nil)

	ticker, err := p.GetTicker("BTC-USD")
	require.NoError(t, err)
	assert.Equal(t, "crypto", ticker.AssetType)
}
