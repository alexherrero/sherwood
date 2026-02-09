package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoad verifies that configuration loads correctly from environment.
func TestLoad(t *testing.T) {
	// Set up test environment
	os.Setenv("PORT", "9000")
	os.Setenv("TRADING_MODE", "dry_run")
	os.Setenv("DATABASE_PATH", "/tmp/test.db")
	defer func() {
		os.Unsetenv("PORT")
		os.Unsetenv("TRADING_MODE")
		os.Unsetenv("DATABASE_PATH")
	}()

	config, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 9000, config.ServerPort)
	assert.Equal(t, ModeDryRun, config.TradingMode)
	assert.Equal(t, "/tmp/test.db", config.DatabasePath)
}

// TestLoadDefaults verifies default values are applied when env vars are missing.
func TestLoadDefaults(t *testing.T) {
	// Clear any existing env vars
	os.Unsetenv("PORT")
	os.Unsetenv("TRADING_MODE")
	os.Unsetenv("DATABASE_PATH")

	config, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 8080, config.ServerPort)
	assert.Equal(t, ModeDryRun, config.TradingMode)
	assert.Equal(t, "./data/sherwood.db", config.DatabasePath)
}

// TestValidateInvalidTradingMode checks that invalid trading modes are rejected.
func TestValidateInvalidTradingMode(t *testing.T) {
	config := &Config{
		ServerPort:  8080,
		TradingMode: "invalid",
	}
	err := config.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid trading mode")
}

// TestValidateInvalidPort checks that invalid ports are rejected.
func TestValidateInvalidPort(t *testing.T) {
	config := &Config{
		ServerPort:  0,
		TradingMode: ModeDryRun,
	}
	err := config.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid server port")
}

// TestIsDryRun verifies the IsDryRun helper method.
func TestIsDryRun(t *testing.T) {
	config := &Config{TradingMode: ModeDryRun}
	assert.True(t, config.IsDryRun())
	assert.False(t, config.IsLive())
}

// TestIsLive verifies the IsLive helper method.
func TestIsLive(t *testing.T) {
	config := &Config{TradingMode: ModeLive}
	assert.True(t, config.IsLive())
	assert.False(t, config.IsDryRun())
}
