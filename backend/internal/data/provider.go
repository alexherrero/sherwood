package data

import (
	"context"
	"time"
)

// Candle represents a single candlestick data point
type Candle struct {
	Timestamp time.Time `json:"timestamp"`
	Open      float64   `json:"open"`
	High      float64   `json:"high"`
	Low       float64   `json:"low"`
	Close     float64   `json:"close"`
	Volume    float64   `json:"volume"`
}

// Provider defines the interface for market data providers
type Provider interface {
	// GetHistoricalData fetches historical candles for a symbol
	GetHistoricalData(ctx context.Context, symbol string, start, end time.Time) ([]Candle, error)
	
	// GetLatestPrice returns the current price for a symbol
	GetLatestPrice(ctx context.Context, symbol string) (float64, error)
}

// IngestionService manages data providers and storage
type IngestionService struct {
	providers map[string]Provider
}

func NewIngestionService() *IngestionService {
	return &IngestionService{
		providers: make(map[string]Provider),
	}
}

func (s *IngestionService) RegisterProvider(name string, p Provider) {
	s.providers[name] = p
}
