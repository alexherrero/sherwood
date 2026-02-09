package strategies

import (
	"testing"
)

// TestNewStrategyByName_ValidNames tests creating strategies with valid names.
func TestNewStrategyByName_ValidNames(t *testing.T) {
	testCases := []struct {
		name         string
		expectedType string
	}{
		{"ma_crossover", "*strategies.MACrossover"},
		{"rsi_momentum", "*strategies.RSIStrategy"},
		{"bb_mean_reversion", "*strategies.BollingerBandsStrategy"},
		{"macd_trend_follower", "*strategies.MACDStrategy"},
		{"nyc_close_open", "*strategies.NYCCloseOpen"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			strategy, err := NewStrategyByName(tc.name)
			if err != nil {
				t.Fatalf("Expected no error for valid strategy name %s, got: %v", tc.name, err)
			}
			if strategy == nil {
				t.Fatalf("Expected strategy instance, got nil")
			}
			if strategy.Name() != tc.name {
				t.Errorf("Expected strategy name %s, got %s", tc.name, strategy.Name())
			}
		})
	}
}

// TestNewStrategyByName_InvalidName tests error handling for invalid names.
func TestNewStrategyByName_InvalidName(t *testing.T) {
	invalidNames := []string{
		"invalid_strategy",
		"",
		"unknown",
		"MA_CROSSOVER", // Case sensitive
	}

	for _, name := range invalidNames {
		t.Run(name, func(t *testing.T) {
			strategy, err := NewStrategyByName(name)
			if err == nil {
				t.Errorf("Expected error for invalid strategy name %s, got nil", name)
			}
			if strategy != nil {
				t.Errorf("Expected nil strategy for invalid name, got %v", strategy)
			}
		})
	}
}

// TestAvailableStrategies tests that all available strategies are listed.
func TestAvailableStrategies(t *testing.T) {
	strategies := AvailableStrategies()

	expectedCount := 5
	if len(strategies) != expectedCount {
		t.Errorf("Expected %d strategies, got %d", expectedCount, len(strategies))
	}

	// Verify each listed strategy can be created
	for _, name := range strategies {
		t.Run(name, func(t *testing.T) {
			strategy, err := NewStrategyByName(name)
			if err != nil {
				t.Errorf("Strategy %s is listed but cannot be created: %v", name, err)
			}
			if strategy == nil {
				t.Errorf("Strategy %s returned nil", name)
			}
		})
	}
}
