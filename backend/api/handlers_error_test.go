package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/alexherrero/sherwood/backend/execution"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestRunBacktestHandler_Errors tests error scenarios for backtest execution.
func TestRunBacktestHandler_Errors(t *testing.T) {
	cfg := &config.Config{TradingMode: "test"}
	registry := strategies.NewRegistry()
	mockProvider := new(MockDataProvider)
	handler := NewHandler(registry, mockProvider, cfg, nil, nil, nil)

	t.Run("InvalidJSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/backtests", nil) // Empty body
		rec := httptest.NewRecorder()
		handler.RunBacktestHandler(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "Invalid request body")
	})

	t.Run("ValidationFailed", func(t *testing.T) {
		// Missing required fields
		payload := map[string]interface{}{}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/backtests", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.RunBacktestHandler(rec, req)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		assert.Contains(t, rec.Body.String(), "Validation failed")
	})

	t.Run("StrategyNotFound", func(t *testing.T) {
		payload := map[string]interface{}{
			"strategy":        "non_existent",
			"symbol":          "AAPL",
			"start":           time.Now().Add(-24 * time.Hour),
			"end":             time.Now(),
			"initial_capital": 10000,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/backtests", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.RunBacktestHandler(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "Strategy 'non_existent' not found")
	})

	t.Run("ProviderError", func(t *testing.T) {
		_ = registry.Register(strategies.NewMACrossover())
		mockProvider.On("GetHistoricalData", "FAIL", mock.Anything, mock.Anything, "1d").
			Return(nil, fmt.Errorf("network error")).Once()

		payload := map[string]interface{}{
			"strategy":        "ma_crossover",
			"symbol":          "FAIL",
			"start":           time.Now().Add(-24 * time.Hour),
			"end":             time.Now(),
			"initial_capital": 10000,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/backtests", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.RunBacktestHandler(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "Failed to fetch historical data")
	})
}

// TestGetOrderHandler_Errors tests error scenarios for getting a single order.
func TestGetOrderHandler_Errors(t *testing.T) {
	cfg := &config.Config{TradingMode: "test"}
	mockBroker := new(MockBroker)
	orderManager := execution.NewOrderManager(mockBroker, nil, nil, nil)

	// Use router to handle URL parameter parsing
	router := NewRouter(cfg, nil, nil, orderManager, nil, nil)

	t.Run("OrderNotFound", func(t *testing.T) {
		mockBroker.On("GetOrder", "missing").Return(nil, fmt.Errorf("order not found")).Once()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/execution/orders/missing", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}
