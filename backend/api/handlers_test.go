package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHealthHandler verifies health endpoint.
func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	healthHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]string
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "ok", response["status"])
}

// TestListStrategiesHandler verifies strategies list endpoint.
func TestListStrategiesHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/strategies", nil)
	rec := httptest.NewRecorder()

	listStrategiesHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	strategies, ok := response["strategies"].([]interface{})
	require.True(t, ok)
	assert.Len(t, strategies, 1)
}

// TestGetStrategyHandler verifies strategy details endpoint.
func TestGetStrategyHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/strategies/ma_crossover", nil)
	rec := httptest.NewRecorder()

	getStrategyHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "ma_crossover", response["name"])
	assert.Contains(t, response, "parameters")
}

// TestRunBacktestHandler verifies backtest submission endpoint.
func TestRunBacktestHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backtests", nil)
	rec := httptest.NewRecorder()

	runBacktestHandler(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)

	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "pending", response["status"])
	assert.NotEmpty(t, response["id"])
}

// TestGetBacktestResultHandler verifies backtest result endpoint.
func TestGetBacktestResultHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/backtests/backtest-001", nil)
	rec := httptest.NewRecorder()

	getBacktestResultHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "completed", response["status"])
	assert.Contains(t, response, "metrics")
}

// TestGetConfigHandler verifies config endpoint.
func TestGetConfigHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	rec := httptest.NewRecorder()

	getConfigHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "0.1.0", response["version"])
	assert.Equal(t, "v1", response["api"])
}

// TestWriteJSON verifies JSON response helper.
func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()

	data := map[string]interface{}{
		"message": "hello",
		"count":   42,
	}

	writeJSON(rec, http.StatusCreated, data)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	body, _ := io.ReadAll(rec.Body)
	var response map[string]interface{}
	err := json.Unmarshal(body, &response)
	require.NoError(t, err)
	assert.Equal(t, "hello", response["message"])
	assert.Equal(t, float64(42), response["count"])
}

// TestWriteJSON_ContentType verifies content type is set.
func TestWriteJSON_ContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]string{"test": "value"})

	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

// TestRouterIntegration verifies router setup (basic smoke test).
func TestRouterIntegration(t *testing.T) {
	// Test that NewRouter returns a valid handler
	cfg := &config.Config{TradingMode: "dry_run"}
	router := NewRouter(cfg)
	assert.NotNil(t, router)

	// Test health endpoint through router
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
