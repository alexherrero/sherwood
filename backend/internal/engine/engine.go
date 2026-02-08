package engine

import (
	"context"
	"log"
	"time"
	
	"github.com/alexherrero/sherwood/backend/internal/data"
)

// Signal represents a trade signal
type Signal struct {
	Symbol    string
	Action    string // BUY, SELL, HOLD
	Price     float64
	Timestamp time.Time
	Strategy  string
}

// Strategy defines the interface for trading strategies
type Strategy interface {
	Name() string
	OnData(ctx context.Context, data interface{}) (*Signal, error)
}

// Engine manages the trading loop and strategies
type Engine struct {
	strategies []Strategy
	provider   data.Provider
	symbol     string
	isRunning  bool
	
	// Channels for communication
	stopChan chan struct{}
}

func NewEngine(provider data.Provider, symbol string) *Engine {
	return &Engine{
		strategies: make([]Strategy, 0),
		provider:   provider,
		symbol:     symbol,
		stopChan:   make(chan struct{}),
	}
}

func (e *Engine) AddStrategy(s Strategy) {
	e.strategies = append(e.strategies, s)
}

func (e *Engine) Start(ctx context.Context) error {
	e.isRunning = true
	ticker := time.NewTicker(1 * time.Second) // 1 second loop for testing
	defer ticker.Stop()

	log.Printf("Engine started for %s", e.symbol)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.stopChan:
			log.Println("Engine stopping...")
			e.isRunning = false
			return nil
		case <-ticker.C:
			// Fetch latest data
			// In a real scenario, this would be more complex (streaming, etc.)
			price, err := e.provider.GetLatestPrice(ctx, e.symbol)
			if err != nil {
				log.Printf("Error fetching price: %v", err)
				continue
			}

			// Create a synthetic candle for now
			candle := data.Candle{
				Timestamp: time.Now(),
				Close:     price,
			}

			// Execute strategies
			for _, s := range e.strategies {
				signal, err := s.OnData(ctx, candle)
				if err != nil {
					log.Printf("Strategy error: %v", err)
					continue
				}
				if signal != nil {
					log.Printf("SIGNAL: %s %s @ %.2f [%s]", signal.Action, signal.Symbol, signal.Price, signal.Strategy)
				}
			}
		}
	}
}

func (e *Engine) Stop() {
	e.isRunning = false
}
