// Package config provides configuration management for the Sherwood trading engine.
// It loads settings from environment variables and .env files.
package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// TradingMode represents the operating mode of the trading engine.
type TradingMode string

const (
	// ModeDryRun indicates paper trading mode (no real money).
	ModeDryRun TradingMode = "dry_run"
	// ModeLive indicates live trading mode with real money.
	ModeLive TradingMode = "live"
)

// Config holds all configuration for the Sherwood application.
type Config struct {
	// Server settings
	ServerPort int
	ServerHost string
	// API Key for authentication
	APIKey string

	// Trading settings
	TradingMode TradingMode

	// Database settings
	DatabasePath string

	// Redis settings (optional)
	RedisURL string

	// API Keys (loaded from environment)
	RobinhoodUsername string
	RobinhoodPassword string
	RobinhoodMFACode  string

	// Logging
	LogLevel string

	// Data Provider settings
	BinanceAPIKey    string
	BinanceAPISecret string
	UseBinanceUS     bool   // Set to true for US users (geo-restricted from binance.com)
	TiingoAPIKey     string // Tiingo API key (get free at tiingo.com)

	// Dynamic Configuration (Phase 2)
	DataProvider      string   // Selected data provider (yahoo, tiingo, binance)
	EnabledStrategies []string // List of enabled strategy names
}

// Load reads configuration from environment variables and .env files.
// It returns a Config struct populated with all settings.
//
// Returns:
//   - *Config: The loaded configuration
//   - error: Any error encountered during loading
func Load() (*Config, error) {
	// Load .env file if it exists (ignore error if not found)
	_ = godotenv.Load()

	config := &Config{
		ServerPort:   getEnvInt("PORT", 8099),
		ServerHost:   getEnv("HOST", "0.0.0.0"),
		APIKey:       os.Getenv("API_KEY"),
		TradingMode:  TradingMode(getEnv("TRADING_MODE", "dry_run")),
		DatabasePath: getEnv("DATABASE_PATH", "./data/sherwood.db"),
		RedisURL:     getEnv("REDIS_URL", ""),
		LogLevel:     getEnv("LOG_LEVEL", "info"),

		// Sensitive credentials from environment only
		RobinhoodUsername: os.Getenv("RH_USERNAME"),
		RobinhoodPassword: os.Getenv("RH_PASSWORD"),
		RobinhoodMFACode:  os.Getenv("RH_MFA_CODE"),

		// Binance credentials
		BinanceAPIKey:    os.Getenv("BINANCE_API_KEY"),
		BinanceAPISecret: os.Getenv("BINANCE_API_SECRET"),
		UseBinanceUS:     getEnv("BINANCE_USE_US", "true") == "true", // Default to US for safety

		// Tiingo credentials
		TiingoAPIKey: os.Getenv("TIINGO_API_KEY"),

		// Dynamic Configuration (Phase 2)
		DataProvider:      getEnv("DATA_PROVIDER", "yahoo"),
		EnabledStrategies: parseStrategies(getEnv("ENABLED_STRATEGIES", "ma_crossover")),
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return config, nil
}

// Validate checks that the configuration is valid.
//
// Returns:
//   - error: Validation error if any required fields are invalid
func (c *Config) Validate() error {
	if c.TradingMode != ModeDryRun && c.TradingMode != ModeLive {
		return fmt.Errorf("invalid trading mode: %s (must be 'dry_run' or 'live')", c.TradingMode)
	}

	if c.ServerPort < 1 || c.ServerPort > 65535 {
		return fmt.Errorf("invalid server port: %d", c.ServerPort)
	}

	if c.IsLive() && c.APIKey == "" {
		fmt.Println("WARNING: Running in LIVE mode without an API_KEY set. This is insecure!")
	}

	return nil
}

// IsDryRun returns true if the engine is in paper trading mode.
func (c *Config) IsDryRun() bool {
	return c.TradingMode == ModeDryRun
}

// IsLive returns true if the engine is in live trading mode.
func (c *Config) IsLive() bool {
	return c.TradingMode == ModeLive
}

// getEnv retrieves an environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt retrieves an environment variable as an integer or returns a default.
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

// parseStrategies parses a comma-separated list of strategy names.
func parseStrategies(strategiesStr string) []string {
	if strategiesStr == "" {
		return []string{}
	}

	// Split by comma and trim whitespace
	parts := []string{}
	for _, part := range splitAndTrim(strategiesStr, ",") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// splitAndTrim splits a string by delimiter and trims whitespace.
func splitAndTrim(s, delimiter string) []string {
	var result []string
	for i := 0; i < len(s); {
		// Find next delimiter
		idx := i
		for idx < len(s) && string(s[idx]) != delimiter {
			idx++
		}
		// Extract and trim the part
		part := s[i:idx]
		// Manual trim
		for len(part) > 0 && (part[0] == ' ' || part[0] == '\t' || part[0] == '\n' || part[0] == '\r') {
			part = part[1:]
		}
		for len(part) > 0 && (part[len(part)-1] == ' ' || part[len(part)-1] == '\t' || part[len(part)-1] == '\n' || part[len(part)-1] == '\r') {
			part = part[:len(part)-1]
		}
		if part != "" {
			result = append(result, part)
		}
		// Move past the delimiter
		i = idx
		if i < len(s) {
			i++ // Skip delimiter
		}
	}
	return result
}
