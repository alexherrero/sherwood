// Package strategies provides trading strategy implementations.
package strategies

import (
	"fmt"
)

// NewStrategyByName creates a strategy instance by name.
// This factory function enables dynamic strategy instantiation based on configuration.
//
// Args:
//   - name: Strategy identifier (e.g., "ma_crossover", "rsi_momentum")
//
// Returns:
//   - Strategy: The created strategy instance
//   - error: Error if strategy name is unknown
func NewStrategyByName(name string) (Strategy, error) {
	switch name {
	case "ma_crossover":
		return NewMACrossover(), nil
	case "rsi_momentum":
		return NewRSIStrategy(), nil
	case "bb_mean_reversion":
		return NewBollingerBandsStrategy(), nil
	case "macd_trend_follower":
		return NewMACDStrategy(), nil
	case "nyc_close_open":
		return NewNYCCloseOpen(), nil
	default:
		return nil, fmt.Errorf("unknown strategy name: %s (available: %v)", name, AvailableStrategies())
	}
}

// AvailableStrategies returns a list of all available strategy names.
// This is useful for validation and documentation.
//
// Returns:
//   - []string: List of available strategy identifiers
func AvailableStrategies() []string {
	return []string{
		"ma_crossover",
		"rsi_momentum",
		"bb_mean_reversion",
		"macd_trend_follower",
		"nyc_close_open",
	}
}
