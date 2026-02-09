package providers

import (
	"time"

	"github.com/alexherrero/sherwood/backend/data"
	"github.com/alexherrero/sherwood/backend/models"
)

// Ensure MockProvider satisfies DataProvider interface
var _ data.DataProvider = (*MockProvider)(nil)

// MockProvider is a simple mock for testing when env vars are missing.
type MockProvider struct {
	NameVal string
}

// NewMockProvider creates a new mock provider.
func NewMockProvider() *MockProvider {
	return &MockProvider{NameVal: "mock"}
}

func (m *MockProvider) Name() string {
	return m.NameVal
}

func (m *MockProvider) GetHistoricalData(symbol string, start, end time.Time, interval string) ([]models.OHLCV, error) {
	// Return some dummy data
	return []models.OHLCV{
		{
			Symbol:    symbol,
			Open:      100.0,
			High:      105.0,
			Low:       95.0,
			Close:     102.0,
			Volume:    1000,
			Timestamp: start,
		},
		{
			Symbol:    symbol,
			Open:      102.0,
			High:      108.0,
			Low:       101.0,
			Close:     107.0,
			Volume:    1200,
			Timestamp: start.Add(time.Hour),
		},
	}, nil
}

func (m *MockProvider) GetLatestPrice(symbol string) (float64, error) {
	return 150.0, nil
}

func (m *MockProvider) GetTicker(symbol string) (*models.Ticker, error) {
	return &models.Ticker{
		Symbol:    symbol,
		Name:      "Mock Asset",
		Exchange:  "MockExchange",
		AssetType: "MockType",
	}, nil
}
