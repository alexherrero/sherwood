package strategies

import (
	"fmt"
	"math"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/alexherrero/sherwood/backend/utils/indicators"
)

// BollingerBandsStrategy implements a mean reversion strategy using Bollinger Bands.
type BollingerBandsStrategy struct {
	*BaseStrategy
	Period           int
	StdDevMultiplier float64
}

// NewBollingerBandsStrategy creates a new Bollinger Bands strategy.
func NewBollingerBandsStrategy() *BollingerBandsStrategy {
	return &BollingerBandsStrategy{
		BaseStrategy: NewBaseStrategy(
			"bb_mean_reversion",
			"Bollinger Bands Mean Reversion - Buy at lower band, Sell at upper band",
		),
		Period:           20,
		StdDevMultiplier: 2.0,
	}
}

// Init initializes the strategy with configuration.
func (s *BollingerBandsStrategy) Init(config map[string]interface{}) error {
	if err := s.BaseStrategy.Init(config); err != nil {
		return err
	}

	if val, ok := config["period"].(float64); ok {
		s.Period = int(val)
	}
	if val, ok := config["stdDevMultiplier"].(float64); ok {
		s.StdDevMultiplier = val
	}

	return nil
}

// Validate checks availability of parameters.
func (s *BollingerBandsStrategy) Validate() error {
	if s.Period <= 0 {
		return fmt.Errorf("period must be positive")
	}
	if s.StdDevMultiplier <= 0 {
		return fmt.Errorf("stdDevMultiplier must be positive")
	}
	return nil
}

// GetParameters returns the strategy parameters.
func (s *BollingerBandsStrategy) GetParameters() map[string]Parameter {
	return map[string]Parameter{
		"period": {
			Description: "Moving Average Period",
			Type:        "int",
			Default:     20,
		},
		"stdDevMultiplier": {
			Description: "Standard Deviation Multiplier",
			Type:        "float",
			Default:     2.0,
		},
	}
}

// OnData processes new market data and generates signals.
func (s *BollingerBandsStrategy) OnData(data []models.OHLCV) models.Signal {
	signal := models.Signal{
		Type:         models.SignalHold,
		Strength:     models.SignalStrengthWeak,
		StrategyName: s.Name(),
		Reason:       "Price within bands",
	}

	if len(data) < s.Period {
		signal.Reason = "Not enough data"
		return signal
	}

	// Extract closing prices
	closes := make([]float64, len(data))
	for i, candle := range data {
		closes[i] = candle.Close
	}

	// Calculate Bollinger Bands
	upper, _, lower := indicators.BollingerBands(closes, s.Period, s.StdDevMultiplier)

	lastIdx := len(data) - 1
	currentPrice := closes[lastIdx]
	currentUpper := upper[lastIdx]
	currentLower := lower[lastIdx]

	if math.IsNaN(currentUpper) || math.IsNaN(currentLower) {
		signal.Reason = "Indicators not ready"
		return signal
	}

	signal.Symbol = data[lastIdx].Symbol
	signal.Price = currentPrice

	// Mean Reversion Logic:
	// If Price <= Lower Band, it's "cheap" -> Buy
	// If Price >= Upper Band, it's "expensive" -> Sell

	if currentPrice <= currentLower {
		signal.Type = models.SignalBuy
		signal.Strength = models.SignalStrengthStrong
		signal.Reason = fmt.Sprintf("Price (%.2f) hit Lower Band (%.2f)", currentPrice, currentLower)
	} else if currentPrice >= currentUpper {
		signal.Type = models.SignalSell
		signal.Strength = models.SignalStrengthStrong
		signal.Reason = fmt.Sprintf("Price (%.2f) hit Upper Band (%.2f)", currentPrice, currentUpper)
	}

	return signal
}
