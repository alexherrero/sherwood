// Package config provides configuration management for the Sherwood trading engine.
// It loads settings from environment variables and .env files.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

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

// validLogLevels is the set of accepted zerolog log levels.
var validLogLevels = map[string]bool{
	"trace": true, "debug": true, "info": true,
	"warn": true, "error": true, "fatal": true,
	"panic": true, "disabled": true,
}

// validProviders is the set of accepted data provider names.
var validProviders = map[string]bool{
	"yahoo": true, "tiingo": true, "binance": true,
}

// validStrategies is the set of accepted strategy names.
var validStrategies = map[string]bool{
	"ma_crossover":        true,
	"rsi_momentum":        true,
	"bb_mean_reversion":   true,
	"macd_trend_follower": true,
	"nyc_close_open":      true,
}

// ValidationError holds multiple configuration validation errors.
// It aggregates all issues so operators can fix everything in one pass.
type ValidationError struct {
	// Errors is the list of individual validation error messages.
	Errors []string
}

// Error returns a formatted multi-line error message listing all issues.
func (ve *ValidationError) Error() string {
	return fmt.Sprintf("%d configuration error(s):\n  - %s",
		len(ve.Errors), strings.Join(ve.Errors, "\n  - "))
}

// Config holds all configuration for the Sherwood application.
type Config struct {
	// Server settings
	ServerPort int
	ServerHost string
	// API Key for authentication
	APIKey string

	// CORS settings
	AllowedOrigins []string // Comma-separated list of allowed origins for CORS

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

	// Shutdown settings
	CloseOnShutdown bool          // If true, close all positions on graceful shutdown
	ShutdownTimeout time.Duration // Maximum time for graceful shutdown (default: 30s)

	// Internal settings
	EnvFile string // Path to .env file (default: .env)
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

		// CORS settings - default to allow localhost for development
		AllowedOrigins: parseStrategies(getEnv("ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:8080")),

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

		EnvFile: ".env",

		// Shutdown settings
		CloseOnShutdown: getEnv("CLOSE_ON_SHUTDOWN", "false") == "true",
		ShutdownTimeout: getEnvDuration("SHUTDOWN_TIMEOUT", 30*time.Second),
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return config, nil
}

// Validate performs comprehensive configuration validation with fail-fast behavior.
// It checks trading mode, server port, data provider credentials, strategy names,
// log level, and mode-specific requirements. All errors are aggregated and returned
// as a single ValidationError so operators can fix everything in one pass.
//
// Validation rules:
//   - Trading mode must be "dry_run" or "live"
//   - Server port must be 1-65535
//   - Log level must be a valid zerolog level
//   - Data provider must be "yahoo", "tiingo", or "binance"
//   - Tiingo requires TIINGO_API_KEY
//   - Binance requires BINANCE_API_KEY and BINANCE_API_SECRET
//   - Live mode requires API_KEY and broker credentials (RH_USERNAME, RH_PASSWORD)
//   - All enabled strategies must be recognized names
//   - Database path must not be empty
//
// Returns:
//   - error: ValidationError if any checks fail, nil otherwise
func (c *Config) Validate() error {
	var errs []string

	// --- Core settings ---
	if c.TradingMode != ModeDryRun && c.TradingMode != ModeLive {
		errs = append(errs,
			fmt.Sprintf("invalid TRADING_MODE '%s': must be 'dry_run' or 'live'", c.TradingMode))
	}

	if c.ServerPort < 1 || c.ServerPort > 65535 {
		errs = append(errs,
			fmt.Sprintf("invalid PORT %d: must be between 1 and 65535", c.ServerPort))
	}

	if c.DatabasePath == "" {
		errs = append(errs,
			"DATABASE_PATH is empty: set DATABASE_PATH in .env (e.g., DATABASE_PATH=./data/sherwood.db)")
	}

	// --- Log level ---
	if !validLogLevels[strings.ToLower(c.LogLevel)] {
		errs = append(errs,
			fmt.Sprintf("invalid LOG_LEVEL '%s': must be one of trace, debug, info, warn, error, fatal, panic, disabled", c.LogLevel))
	}

	// --- Data provider validation ---
	if !validProviders[c.DataProvider] {
		errs = append(errs,
			fmt.Sprintf("invalid DATA_PROVIDER '%s': must be one of yahoo, tiingo, binance", c.DataProvider))
	} else {
		errs = append(errs, c.validateProvider()...)
	}

	// --- Strategy validation ---
	errs = append(errs, c.validateStrategies()...)

	// --- Mode-specific validation ---
	errs = append(errs, c.validateMode()...)

	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}

	return nil
}

// validateProvider checks that provider-specific credentials are present.
// Called only after the provider name itself has been validated.
//
// Returns:
//   - []string: List of error messages (empty if valid)
func (c *Config) validateProvider() []string {
	var errs []string

	switch c.DataProvider {
	case "tiingo":
		if c.TiingoAPIKey == "" {
			errs = append(errs,
				"Tiingo provider requires TIINGO_API_KEY: get a free key at https://www.tiingo.com and set TIINGO_API_KEY in .env")
		}
	case "binance":
		if c.BinanceAPIKey == "" {
			errs = append(errs,
				"Binance provider requires BINANCE_API_KEY: set BINANCE_API_KEY in .env")
		}
		if c.BinanceAPISecret == "" {
			errs = append(errs,
				"Binance provider requires BINANCE_API_SECRET: set BINANCE_API_SECRET in .env")
		}
	}
	// yahoo requires no credentials

	return errs
}

// validateStrategies checks that all enabled strategy names are recognized.
//
// Returns:
//   - []string: List of error messages (empty if valid)
func (c *Config) validateStrategies() []string {
	var errs []string

	for _, name := range c.EnabledStrategies {
		if !validStrategies[name] {
			available := make([]string, 0, len(validStrategies))
			for k := range validStrategies {
				available = append(available, k)
			}
			errs = append(errs,
				fmt.Sprintf("unknown strategy '%s' in ENABLED_STRATEGIES: available strategies are %v", name, available))
		}
	}

	return errs
}

// validateMode checks mode-specific requirements.
// Live mode requires authentication and broker credentials.
//
// Returns:
//   - []string: List of error messages (empty if valid)
func (c *Config) validateMode() []string {
	var errs []string

	if c.IsLive() {
		if c.APIKey == "" {
			errs = append(errs,
				"live mode requires API_KEY for authentication: generate one with the /api/v1/config/rotate-key endpoint or set API_KEY in .env")
		}
		if c.RobinhoodUsername == "" {
			errs = append(errs,
				"live mode requires RH_USERNAME: set your Robinhood username in .env")
		}
		if c.RobinhoodPassword == "" {
			errs = append(errs,
				"live mode requires RH_PASSWORD: set your Robinhood password in .env")
		}
	}

	return errs
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

// getEnvDuration retrieves an environment variable as a time.Duration or returns a default.
// The value should be a Go duration string (e.g., "30s", "5m", "1h").
func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
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

// GenerateAPIKey generates a secure random API key of 32 bytes (64 hex characters).
func GenerateAPIKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// RotateAPIKey generates a new API key, updates the config, and saves it to the .env file.
func (c *Config) RotateAPIKey() (string, error) {
	newKey, err := GenerateAPIKey()
	if err != nil {
		return "", err
	}

	c.APIKey = newKey

	// Update .env file
	envFile := c.EnvFile
	if envFile == "" {
		envFile = ".env"
	}

	content, err := os.ReadFile(envFile)
	if err != nil {
		// If .env doesn't exist, create it
		if os.IsNotExist(err) {
			return newKey, os.WriteFile(envFile, []byte("API_KEY="+newKey+"\n"), 0644)
		}
		return "", err
	}

	lines := strings.Split(string(content), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, "API_KEY=") {
			lines[i] = "API_KEY=" + newKey
			found = true
			break
		}
	}

	if !found {
		lines = append(lines, "API_KEY="+newKey)
	}

	err = os.WriteFile(envFile, []byte(strings.Join(lines, "\n")), 0644)
	if err != nil {
		return "", fmt.Errorf("failed to write .env file: %w", err)
	}

	return newKey, nil
}
