package strategies

import (
	"fmt"
	"math"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/alexherrero/sherwood/backend/utils/indicators"
)

// MACDStrategy implements a trend following strategy using MACD crossovers.
type MACDStrategy struct {
	*BaseStrategy
	FastPeriod   int
	SlowPeriod   int
	SignalPeriod int
}

// NewMACDStrategy creates a new MACD strategy.
func NewMACDStrategy() *MACDStrategy {
	return &MACDStrategy{
		BaseStrategy: NewBaseStrategy(
			"macd_trend_follower",
			"MACD Trend Follower - Buy on bullish crossover, Sell on bearish crossover",
		),
		FastPeriod:   12,
		SlowPeriod:   26,
		SignalPeriod: 9,
	}
}

// Init initializes the strategy with configuration.
func (s *MACDStrategy) Init(config map[string]interface{}) error {
	if err := s.BaseStrategy.Init(config); err != nil {
		return err
	}

	if val, ok := config["fastPeriod"].(float64); ok {
		s.FastPeriod = int(val)
	}
	if val, ok := config["slowPeriod"].(float64); ok {
		s.SlowPeriod = int(val)
	}
	if val, ok := config["signalPeriod"].(float64); ok {
		s.SignalPeriod = int(val)
	}

	return nil
}

// Validate checks availability of parameters.
func (s *MACDStrategy) Validate() error {
	if s.FastPeriod <= 0 || s.SlowPeriod <= 0 || s.SignalPeriod <= 0 {
		return fmt.Errorf("all periods must be positive")
	}
	if s.FastPeriod >= s.SlowPeriod {
		return fmt.Errorf("fast period must be less than slow period")
	}
	return nil
}

// GetParameters returns the strategy parameters.
func (s *MACDStrategy) GetParameters() map[string]Parameter {
	return map[string]Parameter{
		"fastPeriod": {
			Description: "Fast EMA Period",
			Type:        "int",
			Default:     12,
		},
		"slowPeriod": {
			Description: "Slow EMA Period",
			Type:        "int",
			Default:     26,
		},
		"signalPeriod": {
			Description: "Signal Line Period",
			Type:        "int",
			Default:     9,
		},
	}
}

// OnData processes new market data and generates signals.
func (s *MACDStrategy) OnData(data []models.OHLCV) models.Signal {
	signal := models.Signal{
		Type:         models.SignalHold,
		Strength:     models.SignalStrengthWeak,
		StrategyName: s.Name(),
		Reason:       "No crossover",
	}

	// We need enough data for the Slow Period + Signal Period roughly to have valid values
	minData := s.SlowPeriod + s.SignalPeriod
	if len(data) < minData {
		signal.Reason = "Not enough data"
		return signal
	}

	// Extract closing prices
	closes := make([]float64, len(data))
	for i, candle := range data {
		closes[i] = candle.Close
	}

	// Calculate MACD
	macdLine, signalLine, _ := indicators.MACD(closes, s.FastPeriod, s.SlowPeriod, s.SignalPeriod)

	lastIdx := len(data) - 1
	prevIdx := len(data) - 2

	currentMACD := macdLine[lastIdx]
	currentSignal := signalLine[lastIdx]
	prevMACD := macdLine[prevIdx]
	prevSignal := signalLine[prevIdx]

	if math.IsNaN(currentMACD) || math.IsNaN(currentSignal) || math.IsNaN(prevMACD) || math.IsNaN(prevSignal) {
		signal.Reason = "Indicators not ready"
		return signal
	}

	signal.Symbol = data[lastIdx].Symbol
	signal.Price = closes[lastIdx]

	// Crossover Logic:
	// Bullish Crossover: MACD crosses ABOVE Signal Line (Prev: MACD < Signal, Curr: MACD > Signal)
	// Bearish Crossover: MACD crosses BELOW Signal Line (Prev: MACD > Signal, Curr: MACD < Signal)

	if prevMACD <= prevSignal && currentMACD > currentSignal {
		signal.Type = models.SignalBuy
		signal.Strength = models.SignalStrengthStrong
		signal.Reason = fmt.Sprintf("Bullish MACD Crossover (%.4f > %.4f)", currentMACD, currentSignal)
	} else if prevMACD >= prevSignal && currentMACD < currentSignal {
		signal.Type = models.SignalSell
		signal.Strength = models.SignalStrengthStrong
		signal.Reason = fmt.Sprintf("Bearish MACD Crossover (%.4f < %.4f)", currentMACD, currentSignal)
	}

	return signal
}
