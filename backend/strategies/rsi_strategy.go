package strategies

import (
	"fmt"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/alexherrero/sherwood/backend/utils/indicators"
)

// RSIStrategy implements a trading strategy based on the Relative Strength Index.
type RSIStrategy struct {
	*BaseStrategy
	Period              int
	OverboughtThreshold float64
	OversoldThreshold   float64
}

// NewRSIStrategy creates a new RSI strategy.
func NewRSIStrategy() *RSIStrategy {
	return &RSIStrategy{
		BaseStrategy: NewBaseStrategy(
			"rsi_momentum",
			"RSI Momentum Strategy - Buy when oversold, Sell when overbought",
		),
		Period:              14,
		OverboughtThreshold: 70.0,
		OversoldThreshold:   30.0,
	}
}

// Init initializes the strategy with configuration.
func (s *RSIStrategy) Init(config map[string]interface{}) error {
	if err := s.BaseStrategy.Init(config); err != nil {
		return err
	}

	if val, ok := config["period"].(float64); ok {
		s.Period = int(val)
	}
	if val, ok := config["overbought"].(float64); ok {
		s.OverboughtThreshold = val
	}
	if val, ok := config["oversold"].(float64); ok {
		s.OversoldThreshold = val
	}

	return nil
}

// Validate checks availability of parameters.
func (s *RSIStrategy) Validate() error {
	if s.Period <= 0 {
		return fmt.Errorf("RSI period must be positive")
	}
	if s.OverboughtThreshold <= s.OversoldThreshold {
		return fmt.Errorf("overbought threshold must be greater than oversold threshold")
	}
	return nil
}

// GetParameters returns the strategy parameters.
func (s *RSIStrategy) GetParameters() map[string]Parameter {
	return map[string]Parameter{
		"period": {
			Description: "RSI Period",
			Type:        "int",
			Default:     14,
		},
		"overbought": {
			Description: "Level above which asset is considered overbought",
			Type:        "float",
			Default:     70.0,
		},
		"oversold": {
			Description: "Level below which asset is considered oversold",
			Type:        "float",
			Default:     30.0,
		},
	}
}

// OnData processes new market data and generates signals.
func (s *RSIStrategy) OnData(data []models.OHLCV) models.Signal {
	signal := models.Signal{
		Type:         models.SignalHold,
		Strength:     models.SignalStrengthWeak,
		StrategyName: s.Name(),
		Reason:       "RSI neutral",
	}

	if len(data) < s.Period+1 {
		signal.Reason = "Not enough data"
		return signal
	}

	// Extract closing prices
	closes := make([]float64, len(data))
	for i, candle := range data {
		closes[i] = candle.Close
	}

	// Calculate RSI
	rsiValues := indicators.RSI(closes, s.Period)
	currentRSI := rsiValues[len(rsiValues)-1]

	lastCandle := data[len(data)-1]
	signal.Symbol = lastCandle.Symbol
	signal.Price = lastCandle.Close

	if currentRSI < s.OversoldThreshold {
		signal.Type = models.SignalBuy
		signal.Strength = models.SignalStrengthStrong
		signal.Reason = fmt.Sprintf("RSI (%.2f) oversold < %.2f", currentRSI, s.OversoldThreshold)
	} else if currentRSI > s.OverboughtThreshold {
		signal.Type = models.SignalSell
		signal.Strength = models.SignalStrengthStrong
		signal.Reason = fmt.Sprintf("RSI (%.2f) overbought > %.2f", currentRSI, s.OverboughtThreshold)
	}

	return signal
}
