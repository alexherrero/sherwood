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
	"github.com/alexherrero/sherwood/backend/tracing"
	"github.com/rs/zerolog/log"
)

// TradingEngine manages the core trading loop.
type TradingEngine struct {
	provider        data.DataProvider
	registry        *strategies.Registry
	orderManager    *execution.OrderManager
	wsManager       *realtime.WebSocketManager
	symbols         []string
	interval        time.Duration
	lookback        time.Duration
	closeOnShutdown bool
	stopCh          chan struct{}
	wg              sync.WaitGroup
	mu              sync.RWMutex
	running         bool
	ctx             context.Context
	cancel          context.CancelFunc
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
//   - closeOnShutdown: If true, close all positions on graceful shutdown
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
	closeOnShutdown bool,
) *TradingEngine {
	return &TradingEngine{
		provider:        provider,
		registry:        registry,
		orderManager:    orderManager,
		wsManager:       wsManager,
		symbols:         symbols,
		interval:        interval,
		lookback:        lookback,
		closeOnShutdown: closeOnShutdown,
		stopCh:          make(chan struct{}),
		running:         false,
		ctx:             nil,
		cancel:          nil,
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

// IsRunning returns whether the trading engine is currently running.
//
// Returns:
//   - bool: true if the engine is running
func (e *TradingEngine) IsRunning() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.running
}

// Stop gracefully stops the trading engine loop.
// It signals the loop to exit and waits for the current tick to complete.
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

// Shutdown performs a full graceful shutdown of the trading engine.
// It stops the engine loop, cancels pending orders, optionally closes all
// positions, and checkpoints state to the database. The provided context
// controls the overall shutdown deadline.
//
// Shutdown sequence:
//  1. Stop accepting new ticks (signal the loop to exit)
//  2. Wait for in-flight tick processing to complete
//  3. Cancel all pending/submitted orders
//  4. If closeOnShutdown is true, close all open positions via market sell
//  5. Checkpoint all orders to the database
//  6. Disconnect the broker
//
// Args:
//   - ctx: Context with deadline for the shutdown process
//
// Returns:
//   - error: First error encountered during shutdown (best-effort)
func (e *TradingEngine) Shutdown(ctx context.Context) error {
	log.Info().Bool("close_positions", e.closeOnShutdown).Msg("Engine graceful shutdown initiated")

	// Step 1-2: Stop the trading loop and wait for in-flight work
	e.Stop()

	// Check if context is already done
	select {
	case <-ctx.Done():
		return fmt.Errorf("shutdown deadline exceeded before cleanup: %w", ctx.Err())
	default:
	}

	// Create an engine context for audit
	shutdownCtx := execution.NewEngineContextWithTrace(ctx)

	// Step 3: Cancel all pending/submitted orders
	cancelled, err := e.orderManager.CancelAllPendingOrders(shutdownCtx)
	if err != nil {
		log.Warn().Err(err).Int("cancelled", cancelled).Msg("Some pending orders could not be cancelled during shutdown")
	}

	// Step 4: Close all positions if configured
	if e.closeOnShutdown {
		if closeErr := e.closeAllPositions(shutdownCtx); closeErr != nil {
			log.Error().Err(closeErr).Msg("Failed to close all positions during shutdown")
			if err == nil {
				err = closeErr
			}
		}
	}

	// Step 5: Checkpoint all orders to database
	if saveErr := e.orderManager.SaveOrders(); saveErr != nil {
		log.Error().Err(saveErr).Msg("Failed to checkpoint orders during shutdown")
		if err == nil {
			err = saveErr
		}
	}

	log.Info().Msg("Engine graceful shutdown complete")
	return err
}

// closeAllPositions closes all open positions by placing market sell orders.
//
// Args:
//   - ctx: Context for order placement
//
// Returns:
//   - error: First error encountered (other closes continue)
func (e *TradingEngine) closeAllPositions(ctx context.Context) error {
	positions, posErr := e.orderManager.GetPositions()
	if posErr != nil {
		return fmt.Errorf("failed to get positions for closure: %w", posErr)
	}

	var firstErr error
	closed := 0
	for _, pos := range positions {
		if pos.Quantity <= 0 {
			continue
		}

		log.Info().
			Str("symbol", pos.Symbol).
			Float64("quantity", pos.Quantity).
			Msg("Closing position on shutdown")

		_, orderErr := e.orderManager.CreateMarketOrder(
			ctx,
			pos.Symbol,
			models.OrderSideSell,
			pos.Quantity,
		)
		if orderErr != nil {
			log.Error().Err(orderErr).Str("symbol", pos.Symbol).Msg("Failed to close position")
			if firstErr == nil {
				firstErr = orderErr
			}
			continue
		}
		closed++
	}

	log.Info().Int("closed", closed).Int("total", len(positions)).Msg("Position closure complete")
	return firstErr
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
			// Generate a unique trace ID for this tick
			tickTraceID := tracing.NewTraceID()
			tickCtx := tracing.WithTraceID(ctx, tickTraceID)
			tickLogger := tracing.Logger(tickCtx)

			tickLogger.Debug().
				Int("symbols", len(e.symbols)).
				Msg("Engine tick started")

			// Process symbols concurrently
			var wg sync.WaitGroup
			for _, symbol := range e.symbols {
				wg.Add(1)
				go func(sym string) {
					defer wg.Done()
					if err := e.processSymbol(tickCtx, sym); err != nil {
						tickLogger.Error().Err(err).Str("symbol", sym).Msg("Error processing symbol")
					}
				}(symbol)
			}
			wg.Wait()

			tickLogger.Debug().Msg("Engine tick completed")
		}
	}
}

// processSymbol handles data fetching and strategy execution for a single symbol.
// The context carries the tick's trace ID for log correlation.
func (e *TradingEngine) processSymbol(ctx context.Context, symbol string) error {
	logger := tracing.Logger(ctx)

	// 1. Fetch latest data
	// Fetch enough candles for strategies
	end := time.Now()
	start := end.Add(-e.lookback)

	// 2. Iterate over strategies, grouping by timeframe would be ideal, but for now we assume a primary timeframe derived from the first available strategy or default to "1d"
	timeframe := "1d"
	strategiesList := e.registry.All()
	if len(strategiesList) > 0 {
		for _, s := range strategiesList {
			timeframe = s.Timeframe()
			break // Use the first strategy's timeframe for now
		}
	}

	// Assume generic timeframe (Daily) for now.
	// In a real system, we'd need to handle multiple timeframes.
	candles, err := e.provider.GetHistoricalData(symbol, start, end, timeframe)
	if err != nil {
		return fmt.Errorf("failed to fetch data: %w", err)
	}

	if len(candles) == 0 {
		return fmt.Errorf("no data returned")
	}

	logger.Debug().
		Str("symbol", symbol).
		Int("candles", len(candles)).
		Msg("Data fetched for symbol")

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
			logger.Info().
				Str("strategy", strategy.Name()).
				Str("symbol", symbol).
				Str("signal", string(signal.Type)).
				Msg("Strategy signal generated")

			if err := e.executeSignal(ctx, signal); err != nil {
				logger.Error().
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
// The context carries the tick's trace ID for log correlation.
func (e *TradingEngine) executeSignal(ctx context.Context, signal models.Signal) error {
	logger := tracing.Logger(ctx)

	logger.Info().
		Str("symbol", signal.Symbol).
		Str("type", string(signal.Type)).
		Float64("price", signal.Price).
		Str("strategy", signal.StrategyName).
		Msg("Executing signal")

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

	// Create engine context that inherits the tick's trace ID
	engineCtx := execution.NewEngineContextWithTrace(ctx)

	var err error
	// If price is specified, use Limit Order, otherwise Market Order
	if signal.Price > 0 {
		_, err = e.orderManager.CreateLimitOrder(engineCtx, signal.Symbol, side, quantity, signal.Price)
	} else {
		_, err = e.orderManager.CreateMarketOrder(engineCtx, signal.Symbol, side, quantity)
	}

	if err != nil {
		return fmt.Errorf("failed to submit order: %w", err)
	}

	return nil
}
