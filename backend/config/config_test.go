package config

import (
	"os"
	"testing"
)

// TestParseStrategies tests the parseStrategies helper function.
func TestParseStrategies(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single strategy",
			input:    "ma_crossover",
			expected: []string{"ma_crossover"},
		},
		{
			name:     "multiple strategies",
			input:    "ma_crossover,rsi_momentum,bb_mean_reversion",
			expected: []string{"ma_crossover", "rsi_momentum", "bb_mean_reversion"},
		},
		{
			name:     "strategies with spaces",
			input:    "ma_crossover , rsi_momentum , bb_mean_reversion",
			expected: []string{"ma_crossover", "rsi_momentum", "bb_mean_reversion"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "single strategy with spaces",
			input:    "  ma_crossover  ",
			expected: []string{"ma_crossover"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseStrategies(tc.input)
			if len(result) != len(tc.expected) {
				t.Errorf("Expected %d strategies, got %d", len(tc.expected), len(result))
				return
			}
			for i, expected := range tc.expected {
				if result[i] != expected {
					t.Errorf("Expected strategy[%d] = %s, got %s", i, expected, result[i])
				}
			}
		})
	}
}

// TestConfigLoad_DataProvider tests DATA_PROVIDER environment variable parsing.
func TestConfigLoad_DataProvider(t *testing.T) {
	testCases := []struct {
		name     string
		envValue string
		expected string
	}{
		{
			name:     "default provider",
			envValue: "",
			expected: "yahoo",
		},
		{
			name:     "yahoo provider",
			envValue: "yahoo",
			expected: "yahoo",
		},
		{
			name:     "tiingo provider",
			envValue: "tiingo",
			expected: "tiingo",
		},
		{
			name:     "binance provider",
			envValue: "binance",
			expected: "binance",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set environment variable
			if tc.envValue != "" {
				os.Setenv("DATA_PROVIDER", tc.envValue)
				defer os.Unsetenv("DATA_PROVIDER")
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}

			if cfg.DataProvider != tc.expected {
				t.Errorf("Expected DataProvider = %s, got %s", tc.expected, cfg.DataProvider)
			}
		})
	}
}

// TestConfigLoad_EnabledStrategies tests ENABLED_STRATEGIES environment variable parsing.
func TestConfigLoad_EnabledStrategies(t *testing.T) {
	testCases := []struct {
		name     string
		envValue string
		expected []string
	}{
		{
			name:     "default strategy",
			envValue: "",
			expected: []string{"ma_crossover"},
		},
		{
			name:     "single strategy",
			envValue: "rsi_momentum",
			expected: []string{"rsi_momentum"},
		},
		{
			name:     "multiple strategies",
			envValue: "ma_crossover,rsi_momentum,bb_mean_reversion",
			expected: []string{"ma_crossover", "rsi_momentum", "bb_mean_reversion"},
		},
		{
			name:     "strategies with spaces",
			envValue: "  ma_crossover  ,  rsi_momentum  ",
			expected: []string{"ma_crossover", "rsi_momentum"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set environment variable
			if tc.envValue != "" {
				os.Setenv("ENABLED_STRATEGIES", tc.envValue)
				defer os.Unsetenv("ENABLED_STRATEGIES")
			} else {
				os.Unsetenv("ENABLED_STRATEGIES")
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}

			if len(cfg.EnabledStrategies) != len(tc.expected) {
				t.Errorf("Expected %d strategies, got %d", len(tc.expected), len(cfg.EnabledStrategies))
				return
			}

			for i, expected := range tc.expected {
				if cfg.EnabledStrategies[i] != expected {
					t.Errorf("Expected strategy[%d] = %s, got %s", i, expected, cfg.EnabledStrategies[i])
				}
			}
		})
	}
}
