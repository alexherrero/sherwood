package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/alexherrero/sherwood/backend/models"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockDataProvider for testing
type MockDataProvider struct {
	mock.Mock
}

func (m *MockDataProvider) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockDataProvider) GetHistoricalData(symbol string, start, end time.Time, interval string) ([]models.OHLCV, error) {
	args := m.Called(symbol, start, end, interval)
	return args.Get(0).([]models.OHLCV), args.Error(1)
}

func (m *MockDataProvider) GetLatestPrice(symbol string) (float64, error) {
	args := m.Called(symbol)
	return args.Get(0).(float64), args.Error(1)
}

func (m *MockDataProvider) GetTicker(symbol string) (*models.Ticker, error) {
	args := m.Called(symbol)
	return args.Get(0).(*models.Ticker), args.Error(1)
}

func setupTestHandler() (*Handler, *MockDataProvider, *strategies.Registry) {
	cfg := &config.Config{TradingMode: "test"}
	registry := strategies.NewRegistry()

	// Register a mock strategy
	strategy := strategies.NewMACrossover()
	_ = registry.Register(strategy)

	mockProvider := new(MockDataProvider)

	handler := NewHandler(registry, mockProvider, cfg)
	return handler, mockProvider, registry
}

// TestHealthHandler verifies health endpoint.
func TestHealthHandler(t *testing.T) {
	handler, _, _ := setupTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.HealthHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]string
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "ok", response["status"])
	assert.Equal(t, "test", response["mode"])
}

// TestListStrategiesHandler verifies strategies list endpoint.
func TestListStrategiesHandler(t *testing.T) {
	handler, _, _ := setupTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/strategies", nil)
	rec := httptest.NewRecorder()

	handler.ListStrategiesHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	strategies, ok := response["strategies"].([]interface{})
	require.True(t, ok)
	assert.Len(t, strategies, 1) // Should have the mock strategy
}

// TestGetStrategyHandler verifies strategy details endpoint.
func TestGetStrategyHandler(t *testing.T) {
	// Need router for URL params
	cfg := &config.Config{}
	registry := strategies.NewRegistry()
	_ = registry.Register(strategies.NewMACrossover())
	mockProvider := new(MockDataProvider)

	router := NewRouter(cfg, registry, mockProvider)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/strategies/ma_crossover", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "ma_crossover", response["name"])
}

// TestRunBacktestHandler verifies backtest submission endpoint.
func TestRunBacktestHandler(t *testing.T) {
	handler, mockProvider, _ := setupTestHandler()

	// Mock data provider response
	mockData := []models.OHLCV{
		{Timestamp: time.Now(), Close: 100},
		{Timestamp: time.Now().Add(time.Hour), Close: 101},
	}
	mockProvider.On("GetHistoricalData", "AAPL", mock.Anything, mock.Anything, "1d").Return(mockData, nil)

	payload := RunBacktestRequest{
		Strategy:       "ma_crossover",
		Symbol:         "AAPL",
		Start:          time.Now().AddDate(0, -1, 0),
		End:            time.Now(),
		InitialCapital: 10000,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backtests", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.RunBacktestHandler(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)

	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "completed", response["status"])
	assert.NotEmpty(t, response["id"])
	mockProvider.AssertExpectations(t)
}

// TestGetBacktestResultHandler verifies backtest result endpoint.
func TestGetBacktestResultHandler(t *testing.T) {
	// Handle URL params via router integration or manual setup
	// Easier to test manually by pre-populating results cache

	// Let's use router to property parse URL params
	cfg := &config.Config{}
	registry := strategies.NewRegistry()
	mockProvider := new(MockDataProvider)
	router := NewRouter(cfg, registry, mockProvider)

	// We need to inject a result into the handler used by the router.
	// Since NewRouter creates its own handler, we can't easily access it.
	// But we can test the handler logic directly if we had a way to inject results.
	// Or we can just run a backtest first then get it.

	// Mock data for run
	mockData := []models.OHLCV{{Timestamp: time.Now(), Close: 100}}
	mockProvider.On("GetHistoricalData", "AAPL", mock.Anything, mock.Anything, "1d").Return(mockData, nil)

	// 1. Run backtest
	payload := RunBacktestRequest{
		Strategy:       "ma_crossover",
		Symbol:         "AAPL",
		Start:          time.Now(),
		End:            time.Now(),
		InitialCapital: 10000,
	}
	// We need to register strategy first
	_ = registry.Register(strategies.NewMACrossover())

	body, _ := json.Marshal(payload)
	runReq := httptest.NewRequest(http.MethodPost, "/api/v1/backtests", bytes.NewReader(body))
	runRec := httptest.NewRecorder()

	router.ServeHTTP(runRec, runReq)

	var runResp map[string]interface{}
	_ = json.Unmarshal(runRec.Body.Bytes(), &runResp)
	id := runResp["id"].(string)

	// 2. Get result
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/backtests/"+id, nil)
	getRec := httptest.NewRecorder()

	router.ServeHTTP(getRec, getReq)

	assert.Equal(t, http.StatusOK, getRec.Code)
	var getResp map[string]interface{}
	err := json.Unmarshal(getRec.Body.Bytes(), &getResp)
	require.NoError(t, err)
	assert.Equal(t, id, getResp["id"])
}

// TestRouterIntegration verifies router with dependencies.
func TestRouterIntegration(t *testing.T) {
	cfg := &config.Config{TradingMode: "dry_run"}
	registry := strategies.NewRegistry()
	mockProvider := new(MockDataProvider)

	router := NewRouter(cfg, registry, mockProvider)
	assert.NotNil(t, router)

	// Test health endpoint
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestWriteJSON tests helper
func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, 200, map[string]string{"foo": "bar"})
	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"foo":"bar"}`, rec.Body.String())
}
