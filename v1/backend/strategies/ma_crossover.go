// Package strategies provides trading strategy implementations.
package strategies

import (
	"fmt"

	"github.com/alexherrero/sherwood/backend/models"
)

// MACrossover implements a moving average crossover strategy.
// Generates buy signals when short MA crosses above long MA,
// and sell signals when short MA crosses below long MA.
type MACrossover struct {
	*BaseStrategy
	shortPeriod int
	longPeriod  int
}

// NewMACrossover creates a new Moving Average Crossover strategy.
//
// Returns:
//   - *MACrossover: The strategy instance
func NewMACrossover() *MACrossover {
	return &MACrossover{
		BaseStrategy: NewBaseStrategy(
			"ma_crossover",
			"Moving Average Crossover Strategy - generates signals on MA crossovers",
		),
		shortPeriod: 10,
		longPeriod:  20,
	}
}

// Init initializes the MA crossover strategy with configuration.
//
// Args:
//   - config: Configuration with "short_period" and "long_period"
//
// Returns:
//   - error: Any initialization error
func (s *MACrossover) Init(config map[string]interface{}) error {
	if err := s.BaseStrategy.Init(config); err != nil {
		return err
	}

	s.shortPeriod = s.GetConfigInt("short_period", 10)
	s.longPeriod = s.GetConfigInt("long_period", 20)

	return s.Validate()
}

// Validate checks if the strategy configuration is valid.
//
// Returns:
//   - error: Validation error if configuration is invalid
func (s *MACrossover) Validate() error {
	if s.shortPeriod <= 0 {
		return fmt.Errorf("short_period must be positive: %d", s.shortPeriod)
	}
	if s.longPeriod <= 0 {
		return fmt.Errorf("long_period must be positive: %d", s.longPeriod)
	}
	if s.shortPeriod >= s.longPeriod {
		return fmt.Errorf("short_period (%d) must be less than long_period (%d)",
			s.shortPeriod, s.longPeriod)
	}
	return nil
}

// GetParameters returns the strategy's parameter definitions.
//
// Returns:
//   - map[string]Parameter: Parameter specifications
func (s *MACrossover) GetParameters() map[string]Parameter {
	return map[string]Parameter{
		"short_period": {
			Type:        "int",
			Default:     10,
			Min:         2,
			Max:         50,
			Description: "Short moving average period",
		},
		"long_period": {
			Type:        "int",
			Default:     20,
			Min:         5,
			Max:         200,
			Description: "Long moving average period",
		},
	}
}

// OnData processes OHLCV data and generates trading signals.
//
// Args:
//   - data: Historical price data (oldest first)
//
// Returns:
//   - models.Signal: The trading signal
func (s *MACrossover) OnData(data []models.OHLCV) models.Signal {
	signal := models.Signal{
		Type:         models.SignalHold,
		Strength:     models.SignalStrengthModerate,
		StrategyName: s.Name(),
		Reason:       "Insufficient data or no crossover detected",
	}

	if len(data) < s.longPeriod+1 {
		signal.Reason = fmt.Sprintf("Need at least %d data points, got %d",
			s.longPeriod+1, len(data))
		return signal
	}

	// Calculate current and previous MAs
	currentShortMA := calculateSMA(data, s.shortPeriod, 0)
	currentLongMA := calculateSMA(data, s.longPeriod, 0)
	prevShortMA := calculateSMA(data, s.shortPeriod, 1)
	prevLongMA := calculateSMA(data, s.longPeriod, 1)

	latest := data[len(data)-1]
	signal.Symbol = latest.Symbol
	signal.Price = latest.Close

	// Detect crossover
	if prevShortMA <= prevLongMA && currentShortMA > currentLongMA {
		// Bullish crossover - short MA crossed above long MA
		signal.Type = models.SignalBuy
		signal.Strength = models.SignalStrengthModerate
		signal.Reason = fmt.Sprintf("Bullish crossover: Short MA (%.2f) crossed above Long MA (%.2f)",
			currentShortMA, currentLongMA)
	} else if prevShortMA >= prevLongMA && currentShortMA < currentLongMA {
		// Bearish crossover - short MA crossed below long MA
		signal.Type = models.SignalSell
		signal.Strength = models.SignalStrengthModerate
		signal.Reason = fmt.Sprintf("Bearish crossover: Short MA (%.2f) crossed below Long MA (%.2f)",
			currentShortMA, currentLongMA)
	} else {
		signal.Reason = fmt.Sprintf("No crossover: Short MA=%.2f, Long MA=%.2f",
			currentShortMA, currentLongMA)
	}

	return signal
}

// calculateSMA calculates Simple Moving Average.
//
// Args:
//   - data: OHLCV data
//   - period: MA period
//   - offset: Bars back from the end (0 = current)
//
// Returns:
//   - float64: The SMA value
func calculateSMA(data []models.OHLCV, period, offset int) float64 {
	if len(data) < period+offset {
		return 0
	}

	endIdx := len(data) - offset
	startIdx := endIdx - period

	sum := 0.0
	for i := startIdx; i < endIdx; i++ {
		sum += data[i].Close
	}
	return sum / float64(period)
}
