package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/alexherrero/sherwood/backend/data"
	"github.com/alexherrero/sherwood/backend/execution"
	"github.com/alexherrero/sherwood/backend/models"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/rs/zerolog/log"
)

// TradingEngine manages the core trading loop.
type TradingEngine struct {
	provider     data.DataProvider
	registry     *strategies.Registry
	orderManager *execution.OrderManager
	symbols      []string
	interval     time.Duration
	stopCh       chan struct{}
	wg           sync.WaitGroup
	mu           sync.RWMutex
	running      bool
}

// NewTradingEngine creates a new trading engine instance.
//
// Args:
//   - provider: Data provider for market data
//   - registry: Strategy registry
//   - orderManager: Execution order manager
//   - symbols: List of symbols to trade
//   - interval: Polling interval
//
// Returns:
//   - *TradingEngine: The engine instance
func NewTradingEngine(
	provider data.DataProvider,
	registry *strategies.Registry,
	orderManager *execution.OrderManager,
	symbols []string,
	interval time.Duration,
) *TradingEngine {
	return &TradingEngine{
		provider:     provider,
		registry:     registry,
		orderManager: orderManager,
		symbols:      symbols,
		interval:     interval,
		stopCh:       make(chan struct{}),
	}
}

// Start begins the trading loop.
// It runs until the context is cancelled or Stop() is called.
func (e *TradingEngine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return fmt.Errorf("trading engine already running")
	}
	e.running = true
	e.mu.Unlock()

	e.wg.Add(1)
	go e.loop(ctx)

	log.Info().
		Dur("interval", e.interval).
		Int("strategies", len(e.registry.List())).
		Int("symbols", len(e.symbols)).
		Msg("Trading Engine started")

	return nil
}

// Stop gracefully stops the trading engine.
func (e *TradingEngine) Stop() {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return
	}
	e.running = false
	close(e.stopCh)
	e.mu.Unlock()

	e.wg.Wait()
	log.Info().Msg("Trading Engine stopped")
}

// loop is the main trading loop.
func (e *TradingEngine) loop(ctx context.Context) {
	defer e.wg.Done()

	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.processTick()
		}
	}
}

// processTick runs one iteration of the trading logic.
func (e *TradingEngine) processTick() {
	var wg sync.WaitGroup

	// Process symbols concurrently
	for _, symbol := range e.symbols {
		wg.Add(1)
		go func(sym string) {
			defer wg.Done()
			if err := e.processSymbol(sym); err != nil {
				log.Error().Err(err).Str("symbol", sym).Msg("Failed to process symbol")
			}
		}(symbol)
	}

	wg.Wait()
}

// processSymbol handles data fetching and strategy execution for a single symbol.
func (e *TradingEngine) processSymbol(symbol string) error {
	// 1. Fetch latest data
	// Fetch enough candles for strategies (e.g., 100)
	// TODO: Make lookback configurable or dynamic
	end := time.Now()
	start := end.Add(-100 * 24 * time.Hour) // Rough estimate for daily candles

	// Assume generic timeframe (Daily) for now.
	// In a real system, we'd need to handle multiple timeframes.
	candles, err := e.provider.GetHistoricalData(symbol, start, end, "1d")
	if err != nil {
		return fmt.Errorf("failed to fetch data: %w", err)
	}

	if len(candles) == 0 {
		return fmt.Errorf("no data returned")
	}

	// 2. Iterate over strategies
	for _, strategy := range e.registry.All() {
		// 3. Generate Signal
		signal := strategy.OnData(candles)

		// 4. Handle Signal
		if signal.Type != models.SignalHold {
			if err := e.executeSignal(signal); err != nil {
				log.Error().
					Err(err).
					Str("strategy", strategy.Name()).
					Str("symbol", symbol).
					Msg("Failed to execute signal")
			}
		}
	}

	return nil
}

// executeSignal converts a signal into an order and submits it.
func (e *TradingEngine) executeSignal(signal models.Signal) error {
	// 1. Validate signal
	if signal.Quantity <= 0 {
		return fmt.Errorf("invalid quantity: %f", signal.Quantity)
	}

	// 2. Determine Order Side
	var side models.OrderSide
	switch signal.Type {
	case models.SignalBuy:
		side = models.OrderSideBuy
	case models.SignalSell:
		side = models.OrderSideSell
	default:
		return fmt.Errorf("unknown signal type: %s", signal.Type)
	}

	// 3. Create Order
	// Assuming Market orders for now unless Price is set
	var order *models.Order
	var err error

	if signal.Price > 0 {
		order, err = e.orderManager.CreateLimitOrder(signal.Symbol, side, signal.Quantity, signal.Price)
	} else {
		order, err = e.orderManager.CreateMarketOrder(signal.Symbol, side, signal.Quantity)
	}

	if err != nil {
		return fmt.Errorf("failed to create order: %w", err)
	}

	log.Info().
		Str("strategy", signal.StrategyName).
		Str("symbol", signal.Symbol).
		Str("side", string(side)).
		Str("order_id", order.ID).
		Msg("Signal executed")

	return nil
}
