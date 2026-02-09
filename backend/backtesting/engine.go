// Package backtesting provides backtesting functionality for trading strategies.
package backtesting

import (
	"fmt"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/rs/zerolog/log"
)

// BacktestConfig holds configuration for a backtest run.
type BacktestConfig struct {
	// Symbol is the ticker symbol to backtest.
	Symbol string
	// StartDate is the start of the backtest period.
	StartDate time.Time
	// EndDate is the end of the backtest period.
	EndDate time.Time
	// InitialCapital is the starting capital.
	InitialCapital float64
	// PositionSize is the fixed position size (0 = use all capital).
	PositionSize float64
	// Commission is the commission per trade.
	Commission float64
}

// BacktestResult holds the results of a backtest run.
type BacktestResult struct {
	// ID is a unique identifier for this backtest.
	ID string
	// Config holds the backtest configuration.
	Config BacktestConfig
	// Strategy is the name of the strategy tested.
	Strategy string
	// Metrics holds performance metrics.
	Metrics *Metrics
	// Trades is the list of simulated trades.
	Trades []SimulatedTrade
	// EquityCurve tracks equity over time.
	EquityCurve []EquityPoint
	// StartedAt is when the backtest started.
	StartedAt time.Time
	// CompletedAt is when the backtest completed.
	CompletedAt time.Time
}

// SimulatedTrade represents a trade during backtesting.
type SimulatedTrade struct {
	EntryTime  time.Time        `json:"entry_time"`
	ExitTime   time.Time        `json:"exit_time"`
	Symbol     string           `json:"symbol"`
	Side       models.OrderSide `json:"side"`
	EntryPrice float64          `json:"entry_price"`
	ExitPrice  float64          `json:"exit_price"`
	Quantity   float64          `json:"quantity"`
	PnL        float64          `json:"pnl"`
	PnLPercent float64          `json:"pnl_percent"`
}

// EquityPoint represents equity at a point in time.
type EquityPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Equity    float64   `json:"equity"`
}

// Engine runs backtests for trading strategies.
type Engine struct {
	idCounter int
}

// NewEngine creates a new backtest engine.
//
// Returns:
//   - *Engine: The backtest engine
func NewEngine() *Engine {
	return &Engine{idCounter: 0}
}

// Run executes a backtest for a strategy against historical data.
//
// Args:
//   - strategy: The trading strategy to test
//   - data: Historical OHLCV data (oldest first)
//   - config: Backtest configuration
//
// Returns:
//   - *BacktestResult: Backtest results and metrics
//   - error: Any error encountered
func (e *Engine) Run(strategy strategies.Strategy, data []models.OHLCV, config BacktestConfig) (*BacktestResult, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("no data provided for backtest")
	}

	e.idCounter++
	result := &BacktestResult{
		ID:          fmt.Sprintf("bt-%06d", e.idCounter),
		Config:      config,
		Strategy:    strategy.Name(),
		Trades:      []SimulatedTrade{},
		EquityCurve: []EquityPoint{},
		StartedAt:   time.Now(),
	}

	capital := config.InitialCapital
	cash := capital
	position := 0.0
	positionCost := 0.0
	var entryTime time.Time
	var entryPrice float64

	log.Info().
		Str("strategy", strategy.Name()).
		Str("symbol", config.Symbol).
		Int("data_points", len(data)).
		Msg("Starting backtest")

	// Iterate through data
	for i := 1; i < len(data); i++ {
		// Get signal from strategy using data up to current bar
		signal := strategy.OnData(data[:i+1])
		bar := data[i]

		// Record equity
		currentEquity := cash
		if position > 0 {
			currentEquity += position * bar.Close
		}
		result.EquityCurve = append(result.EquityCurve, EquityPoint{
			Timestamp: bar.Timestamp,
			Equity:    currentEquity,
		})

		// Process signals
		switch signal.Type {
		case models.SignalBuy:
			if position == 0 { // Only enter if flat
				positionSize := config.PositionSize
				if positionSize == 0 {
					positionSize = cash * 0.95 // Use 95% of capital
				}
				quantity := positionSize / bar.Close
				cost := quantity*bar.Close + config.Commission

				if cost <= cash {
					position = quantity
					positionCost = cost
					entryPrice = bar.Close
					entryTime = bar.Timestamp
					cash -= cost

					log.Debug().
						Time("time", bar.Timestamp).
						Float64("price", bar.Close).
						Float64("quantity", quantity).
						Msg("BUY signal executed")
				}
			}

		case models.SignalSell:
			if position > 0 { // Only exit if have position
				exitPrice := bar.Close
				proceeds := position*exitPrice - config.Commission
				pnl := proceeds - positionCost
				pnlPercent := (exitPrice - entryPrice) / entryPrice * 100

				trade := SimulatedTrade{
					EntryTime:  entryTime,
					ExitTime:   bar.Timestamp,
					Symbol:     config.Symbol,
					Side:       models.OrderSideBuy,
					EntryPrice: entryPrice,
					ExitPrice:  exitPrice,
					Quantity:   position,
					PnL:        pnl,
					PnLPercent: pnlPercent,
				}
				result.Trades = append(result.Trades, trade)

				cash += proceeds
				position = 0
				positionCost = 0

				log.Debug().
					Time("time", bar.Timestamp).
					Float64("price", bar.Close).
					Float64("pnl", pnl).
					Msg("SELL signal executed")
			}
		}
	}

	// Close any open position at end
	if position > 0 {
		lastBar := data[len(data)-1]
		exitPrice := lastBar.Close
		proceeds := position*exitPrice - config.Commission
		pnl := proceeds - positionCost
		pnlPercent := (exitPrice - entryPrice) / entryPrice * 100

		trade := SimulatedTrade{
			EntryTime:  entryTime,
			ExitTime:   lastBar.Timestamp,
			Symbol:     config.Symbol,
			Side:       models.OrderSideBuy,
			EntryPrice: entryPrice,
			ExitPrice:  exitPrice,
			Quantity:   position,
			PnL:        pnl,
			PnLPercent: pnlPercent,
		}
		result.Trades = append(result.Trades, trade)
		cash += proceeds
	}

	// Calculate metrics
	result.Metrics = CalculateMetrics(result.Trades, result.EquityCurve, config.InitialCapital)
	result.CompletedAt = time.Now()

	log.Info().
		Str("id", result.ID).
		Float64("total_return", result.Metrics.TotalReturn).
		Int("total_trades", result.Metrics.TotalTrades).
		Float64("win_rate", result.Metrics.WinRate).
		Msg("Backtest complete")

	return result, nil
}
