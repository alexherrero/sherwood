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
		ServerPort:   getEnvInt("PORT", 8080),
		ServerHost:   getEnv("HOST", "0.0.0.0"),
		TradingMode:  TradingMode(getEnv("TRADING_MODE", "dry_run")),
		DatabasePath: getEnv("DATABASE_PATH", "./data/sherwood.db"),
		RedisURL:     getEnv("REDIS_URL", ""),
		LogLevel:     getEnv("LOG_LEVEL", "info"),

		// Sensitive credentials from environment only
		RobinhoodUsername: os.Getenv("RH_USERNAME"),
		RobinhoodPassword: os.Getenv("RH_PASSWORD"),
		RobinhoodMFACode:  os.Getenv("RH_MFA_CODE"),
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
