package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/alexherrero/sherwood/backend/data"
	"github.com/alexherrero/sherwood/backend/execution"
	"github.com/alexherrero/sherwood/backend/models"
	"github.com/alexherrero/sherwood/backend/realtime"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/rs/zerolog/log"
)

// TradingEngine manages the core trading loop.
type TradingEngine struct {
	provider     data.DataProvider
	registry     *strategies.Registry
	orderManager *execution.OrderManager
	wsManager    *realtime.WebSocketManager
	symbols      []string
	interval     time.Duration
	lookback     time.Duration
	stopCh       chan struct{}
	wg           sync.WaitGroup
	mu           sync.RWMutex
	running      bool
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewTradingEngine creates a new trading engine instance.
//
// Args:
//   - provider: Data provider for market data
//   - registry: Strategy registry
//   - orderManager: Execution order manager
//   - wsManager: WebSocket manager for real-time updates (can be nil)
//   - symbols: List of symbols to trade
//   - interval: Polling interval
//   - lookback: Historical data lookback period
//
// Returns:
//   - *TradingEngine: The engine instance
func NewTradingEngine(
	provider data.DataProvider,
	registry *strategies.Registry,
	orderManager *execution.OrderManager,
	wsManager *realtime.WebSocketManager,
	symbols []string,
	interval time.Duration,
	lookback time.Duration,
) *TradingEngine {
	return &TradingEngine{
		provider:     provider,
		registry:     registry,
		orderManager: orderManager,
		wsManager:    wsManager,
		symbols:      symbols,
		interval:     interval,
		lookback:     lookback,
		stopCh:       make(chan struct{}),
		running:      false,
		ctx:          nil,
		cancel:       nil,
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
	// Re-initialize stopCh to allow restart
	e.stopCh = make(chan struct{})
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
			// Iterate over symbols
			for _, symbol := range e.symbols {
				if err := e.processSymbol(symbol); err != nil {
					log.Error().Err(err).Str("symbol", symbol).Msg("Error processing symbol")
				}
			}
		}
	}
}

// processSymbol handles data fetching and strategy execution for a single symbol.
func (e *TradingEngine) processSymbol(symbol string) error {
	// 1. Fetch latest data
	// Fetch enough candles for strategies
	end := time.Now()
	start := end.Add(-e.lookback)

	// Assume generic timeframe (Daily) for now.
	// In a real system, we'd need to handle multiple timeframes.
	candles, err := e.provider.GetHistoricalData(symbol, start, end, "1d")
	if err != nil {
		return fmt.Errorf("failed to fetch data: %w", err)
	}

	if len(candles) == 0 {
		return fmt.Errorf("no data returned")
	}

	// Broadcast latest candle
	if e.wsManager != nil {
		latest := candles[len(candles)-1]
		e.wsManager.Broadcast("market_data", map[string]interface{}{
			"symbol": symbol,
			"candle": latest,
		})
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

// executeSignal handles the execution of a trading signal.
func (e *TradingEngine) executeSignal(signal models.Signal) error {
	log.Info().
		Str("symbol", signal.Symbol).
		Str("type", string(signal.Type)).
		Float64("price", signal.Price).
		Str("strategy", signal.StrategyName).
		Msg("Signal generated")

	// Determine quantity
	quantity := 1.0
	if signal.Quantity > 0 {
		quantity = signal.Quantity
	}

	var side models.OrderSide
	if signal.Type == models.SignalBuy {
		side = models.OrderSideBuy
	} else if signal.Type == models.SignalSell {
		side = models.OrderSideSell
	} else {
		return nil // Should be filtered already
	}

	// Create Market Order standardizes execution for Phase 1.
	// TODO: Support Limit Orders when signal.Price is set.

	_, err := e.orderManager.CreateMarketOrder(execution.NewEngineContext(), signal.Symbol, side, quantity)
	if err != nil {
		return fmt.Errorf("failed to submit order: %w", err)
	}

	return nil
}
