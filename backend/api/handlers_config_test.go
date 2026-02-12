package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRotateAPIKeyHandler(t *testing.T) {
	// Setup
	tmpEnv := "test_env_rotate_key.env"
	err := os.WriteFile(tmpEnv, []byte("API_KEY=old-key\nSERVER_PORT=8099\n"), 0644)
	require.NoError(t, err)
	defer os.Remove(tmpEnv)

	cfg := &config.Config{
		ServerPort:        8099,
		TradingMode:       config.ModeDryRun,
		APIKey:            "old-key",
		EnabledStrategies: []string{},
		EnvFile:           tmpEnv,
	}

	handler := NewHandler(nil, nil, cfg, nil, nil, nil, nil)

	// Create Request
	req := httptest.NewRequest("POST", "/api/v1/config/rotate-key", nil)
	w := httptest.NewRecorder()

	// Execute
	handler.RotateAPIKeyHandler(w, req)

	// Verify Response
	res := w.Result()
	assert.Equal(t, http.StatusOK, res.StatusCode)

	var response map[string]string
	err = json.NewDecoder(res.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, "success", response["status"])
	newKey := response["api_key"]
	assert.NotEmpty(t, newKey)
	assert.NotEqual(t, "old-key", newKey)

	// Verify Config Update
	assert.Equal(t, newKey, cfg.APIKey)

	// Verify File Update
	content, err := os.ReadFile(tmpEnv)
	require.NoError(t, err)
	assert.Contains(t, string(content), "API_KEY="+newKey)
}

func TestGenerateConfigWarnings(t *testing.T) {
	tests := []struct {
		name         string
		cfg          *config.Config
		enabledCount int
		wantWarning  string
	}{
		{
			name: "NoStrategies",
			cfg: &config.Config{
				TradingMode: config.ModeDryRun,
			},
			enabledCount: 0,
			wantWarning:  "No strategies enabled",
		},
		{
			name: "LiveNoKey",
			cfg: &config.Config{
				TradingMode: config.ModeLive,
				APIKey:      "",
			},
			enabledCount: 1,
			wantWarning:  "Running in LIVE mode without API_KEY",
		},
		{
			name: "TiingoNoKey",
			cfg: &config.Config{
				TradingMode:  config.ModeDryRun,
				DataProvider: "tiingo",
				TiingoAPIKey: "",
			},
			enabledCount: 1,
			wantWarning:  "Tiingo provider selected but TIINGO_API_KEY not set",
		},
		{
			name: "BinanceNoKey",
			cfg: &config.Config{
				TradingMode:   config.ModeDryRun,
				DataProvider:  "binance",
				BinanceAPIKey: "",
			},
			enabledCount: 1,
			wantWarning:  "Binance provider selected but API credentials not set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := generateConfigWarnings(tt.cfg, tt.enabledCount)
			found := false
			for _, w := range warnings {
				if strings.Contains(w, tt.wantWarning) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected warning containing '%s', got %v", tt.wantWarning, warnings)
			}
		})
	}
}

// TestGetConfigHandler verifies config retrieval endpoint.
func TestGetConfigHandler(t *testing.T) {
	cfg := &config.Config{
		TradingMode: "test",
		ServerPort:  8080,
		LogLevel:    "info",
		APIKey:      "secret-key",
	}
	handler := NewHandler(nil, nil, cfg, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	rec := httptest.NewRecorder()

	handler.GetConfigHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "test", response["trading_mode"])
	assert.Equal(t, "info", response["log_level"])
	assert.NotContains(t, response, "api_key", "Secrets should not be exposed")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[0:len(substr)] == substr // Prefix check is enough for these messages
}
