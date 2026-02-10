package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

	handler := NewHandler(nil, nil, cfg, nil, nil, nil)

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
