package config

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			assert.Equal(t, tc.expected, result)
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envValue != "" {
				t.Setenv("DATA_PROVIDER", tc.envValue)
			}

			cfg, err := Load()
			require.NoError(t, err)
			assert.Equal(t, tc.expected, cfg.DataProvider)
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
			if tc.envValue != "" {
				t.Setenv("ENABLED_STRATEGIES", tc.envValue)
			} else {
				t.Setenv("ENABLED_STRATEGIES", "")
			}

			cfg, err := Load()
			require.NoError(t, err)
			assert.Equal(t, tc.expected, cfg.EnabledStrategies)
		})
	}
}

// TestConfigLoad_Full tests loading with all standard env vars set.
func TestConfigLoad_Full(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("HOST", "0.0.0.0")
	t.Setenv("API_KEY", "secret-key")
	t.Setenv("TRADING_MODE", "live")
	t.Setenv("DATABASE_PATH", "/tmp/test.db")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("ALLOWED_ORIGINS", "http://example.com,http://foo.com")
	t.Setenv("DATA_PROVIDER", "yahoo")
	t.Setenv("ENABLED_STRATEGIES", "ma_crossover")
	// Live mode requires broker creds
	t.Setenv("RH_USERNAME", "testuser")
	t.Setenv("RH_PASSWORD", "testpass")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, 9090, cfg.ServerPort)
	assert.Equal(t, "0.0.0.0", cfg.ServerHost)
	assert.Equal(t, "secret-key", cfg.APIKey)
}

// TestRotateAPIKey tests rotating the API key in the .env file.
func TestRotateAPIKey(t *testing.T) {
	// Create temp .env file
	tmpfile, err := os.CreateTemp("", ".env")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	// Write initial content
	initialContent := []byte("PORT=8080\nAPI_KEY=old-key\nLOG_LEVEL=info")
	_, err = tmpfile.Write(initialContent)
	require.NoError(t, err)
	require.NoError(t, tmpfile.Close())

	cfg := &Config{
		EnvFile: tmpfile.Name(),
		APIKey:  "old-key",
	}

	// Rotate key
	newKey, err := cfg.RotateAPIKey()
	require.NoError(t, err)
	assert.NotEmpty(t, newKey)
	assert.NotEqual(t, "old-key", newKey)
	assert.Equal(t, newKey, cfg.APIKey)

	// Verify file content
	content, err := os.ReadFile(tmpfile.Name())
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "API_KEY="+newKey)
	assert.Contains(t, contentStr, "PORT=8080")
}

// --- Enhanced Validation Tests ---

// TestValidate_ValidDryRunConfig tests that a valid dry_run config passes validation.
func TestValidate_ValidDryRunConfig(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeDryRun,
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "yahoo",
		EnabledStrategies: []string{"ma_crossover"},
	}
	require.NoError(t, cfg.Validate())
}

// TestValidate_ValidLiveConfig tests that a properly configured live config passes.
func TestValidate_ValidLiveConfig(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeLive,
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "yahoo",
		EnabledStrategies: []string{"ma_crossover"},
		APIKey:            "some-secret-key",
		RobinhoodUsername: "user",
		RobinhoodPassword: "pass",
	}
	require.NoError(t, cfg.Validate())
}

// TestValidate_InvalidTradingMode tests that an invalid trading mode is caught.
func TestValidate_InvalidTradingMode(t *testing.T) {
	cfg := &Config{
		TradingMode:       "invalid",
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "yahoo",
		EnabledStrategies: []string{"ma_crossover"},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TRADING_MODE")
	assert.Contains(t, err.Error(), "invalid")
}

// TestValidate_InvalidPort tests that an invalid server port is caught.
func TestValidate_InvalidPort(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeDryRun,
		ServerPort:        0,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "yahoo",
		EnabledStrategies: []string{"ma_crossover"},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PORT")
}

// TestValidate_InvalidLogLevel tests that an invalid log level is caught.
func TestValidate_InvalidLogLevel(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeDryRun,
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "verbose",
		DataProvider:      "yahoo",
		EnabledStrategies: []string{"ma_crossover"},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LOG_LEVEL")
	assert.Contains(t, err.Error(), "verbose")
}

// TestValidate_ValidLogLevels tests that all valid log levels are accepted.
func TestValidate_ValidLogLevels(t *testing.T) {
	levels := []string{"trace", "debug", "info", "warn", "error", "fatal", "panic", "disabled"}
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			cfg := &Config{
				TradingMode:       ModeDryRun,
				ServerPort:        8099,
				DatabasePath:      "./data/sherwood.db",
				LogLevel:          level,
				DataProvider:      "yahoo",
				EnabledStrategies: []string{"ma_crossover"},
			}
			require.NoError(t, cfg.Validate())
		})
	}
}

// TestValidate_InvalidProvider tests that an unknown data provider is caught.
func TestValidate_InvalidProvider(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeDryRun,
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "alphavantage",
		EnabledStrategies: []string{"ma_crossover"},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATA_PROVIDER")
	assert.Contains(t, err.Error(), "alphavantage")
}

// TestValidate_TiingoMissingAPIKey tests that Tiingo requires an API key.
func TestValidate_TiingoMissingAPIKey(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeDryRun,
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "tiingo",
		TiingoAPIKey:      "",
		EnabledStrategies: []string{"ma_crossover"},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TIINGO_API_KEY")
	assert.Contains(t, err.Error(), "tiingo.com")
}

// TestValidate_TiingoWithAPIKey tests that Tiingo passes with an API key.
func TestValidate_TiingoWithAPIKey(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeDryRun,
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "tiingo",
		TiingoAPIKey:      "some-api-key",
		EnabledStrategies: []string{"ma_crossover"},
	}
	require.NoError(t, cfg.Validate())
}

// TestValidate_BinanceMissingCredentials tests Binance requires both key and secret.
func TestValidate_BinanceMissingCredentials(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeDryRun,
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "binance",
		BinanceAPIKey:     "",
		BinanceAPISecret:  "",
		EnabledStrategies: []string{"ma_crossover"},
	}
	err := cfg.Validate()
	require.Error(t, err)

	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	// Both key and secret should be flagged
	assert.GreaterOrEqual(t, len(ve.Errors), 2)
	assert.Contains(t, err.Error(), "BINANCE_API_KEY")
	assert.Contains(t, err.Error(), "BINANCE_API_SECRET")
}

// TestValidate_BinanceWithCredentials tests Binance passes with proper credentials.
func TestValidate_BinanceWithCredentials(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeDryRun,
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "binance",
		BinanceAPIKey:     "key",
		BinanceAPISecret:  "secret",
		EnabledStrategies: []string{"ma_crossover"},
	}
	require.NoError(t, cfg.Validate())
}

// TestValidate_InvalidStrategy tests that unknown strategy names are caught.
func TestValidate_InvalidStrategy(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeDryRun,
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "yahoo",
		EnabledStrategies: []string{"ma_crossover", "fake_strategy"},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fake_strategy")
	assert.Contains(t, err.Error(), "ENABLED_STRATEGIES")
}

// TestValidate_AllValidStrategies tests that all recognized strategy names pass.
func TestValidate_AllValidStrategies(t *testing.T) {
	cfg := &Config{
		TradingMode:  ModeDryRun,
		ServerPort:   8099,
		DatabasePath: "./data/sherwood.db",
		LogLevel:     "info",
		DataProvider: "yahoo",
		EnabledStrategies: []string{
			"ma_crossover", "rsi_momentum", "bb_mean_reversion",
			"macd_trend_follower", "nyc_close_open",
		},
	}
	require.NoError(t, cfg.Validate())
}

// TestValidate_LiveModeMissingAPIKey tests live mode requires API key.
func TestValidate_LiveModeMissingAPIKey(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeLive,
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "yahoo",
		EnabledStrategies: []string{"ma_crossover"},
		APIKey:            "",
		RobinhoodUsername: "user",
		RobinhoodPassword: "pass",
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API_KEY")
	assert.Contains(t, err.Error(), "live mode")
}

// TestValidate_LiveModeMissingBrokerCreds tests live mode requires broker credentials.
func TestValidate_LiveModeMissingBrokerCreds(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeLive,
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "yahoo",
		EnabledStrategies: []string{"ma_crossover"},
		APIKey:            "some-key",
		RobinhoodUsername: "",
		RobinhoodPassword: "",
	}
	err := cfg.Validate()
	require.Error(t, err)

	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	assert.GreaterOrEqual(t, len(ve.Errors), 2)
	assert.Contains(t, err.Error(), "RH_USERNAME")
	assert.Contains(t, err.Error(), "RH_PASSWORD")
}

// TestValidate_EmptyDatabasePath tests that an empty database path is caught.
func TestValidate_EmptyDatabasePath(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeDryRun,
		ServerPort:        8099,
		DatabasePath:      "",
		LogLevel:          "info",
		DataProvider:      "yahoo",
		EnabledStrategies: []string{"ma_crossover"},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DATABASE_PATH")
}

// TestValidate_MultipleErrors tests that all errors are aggregated.
func TestValidate_MultipleErrors(t *testing.T) {
	cfg := &Config{
		TradingMode:       "bogus",
		ServerPort:        0,
		DatabasePath:      "",
		LogLevel:          "verbose",
		DataProvider:      "fake",
		EnabledStrategies: []string{"nonexistent"},
	}
	err := cfg.Validate()
	require.Error(t, err)

	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	// Should have at least 5 errors: mode, port, db path, log level, provider, strategy
	assert.GreaterOrEqual(t, len(ve.Errors), 5, "expected at least 5 aggregated errors, got %d: %v", len(ve.Errors), ve.Errors)
}

// TestValidationError_ErrorFormat tests the multi-line error formatting.
func TestValidationError_ErrorFormat(t *testing.T) {
	ve := &ValidationError{
		Errors: []string{"error one", "error two", "error three"},
	}
	errStr := ve.Error()
	assert.Contains(t, errStr, "3 configuration error(s)")
	assert.Contains(t, errStr, "error one")
	assert.Contains(t, errStr, "error two")
	assert.Contains(t, errStr, "error three")
}

// TestValidate_YahooNoCredsRequired tests yahoo works without any API keys.
func TestValidate_YahooNoCredsRequired(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeDryRun,
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "yahoo",
		EnabledStrategies: []string{"ma_crossover"},
	}
	require.NoError(t, cfg.Validate())
}

// TestValidate_DryRunNoAPIKeyOK tests dry_run mode doesn't require API key.
func TestValidate_DryRunNoAPIKeyOK(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeDryRun,
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "yahoo",
		EnabledStrategies: []string{"ma_crossover"},
		APIKey:            "",
	}
	require.NoError(t, cfg.Validate())
}

// TestValidate_EmptyStrategiesOK tests that no strategies is allowed (engine just won't trade).
func TestValidate_EmptyStrategiesOK(t *testing.T) {
	cfg := &Config{
		TradingMode:       ModeDryRun,
		ServerPort:        8099,
		DatabasePath:      "./data/sherwood.db",
		LogLevel:          "info",
		DataProvider:      "yahoo",
		EnabledStrategies: []string{},
	}
	require.NoError(t, cfg.Validate())
}
