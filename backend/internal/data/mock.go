package data

import (
	"context"
	"math/rand"
	"time"
)

// MockProvider generates synthetic market data
type MockProvider struct{}

func NewMockProvider() *MockProvider {
	return &MockProvider{}
}

func (p *MockProvider) GetHistoricalData(ctx context.Context, symbol string, start, end time.Time) ([]Candle, error) {
	var candles []Candle
	currentTime := start
	price := 100.0

	for currentTime.Before(end) {
		// Random walk
		change := (rand.Float64() - 0.5) * 2
		price += change
		
		candles = append(candles, Candle{
			Timestamp: currentTime,
			Open:      price,
			High:      price + rand.Float64(),
			Low:       price - rand.Float64(),
			Close:     price + (rand.Float64()-0.5),
			Volume:    1000 + rand.Float64()*1000,
		})
		
		currentTime = currentTime.Add(time.Minute)
	}
	
	return candles, nil
}

func (p *MockProvider) GetLatestPrice(ctx context.Context, symbol string) (float64, error) {
	return 100.0 + (rand.Float64()-0.5)*10, nil
}
