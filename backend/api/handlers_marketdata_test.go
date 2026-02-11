package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/alexherrero/sherwood/backend/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestGetHistoricalDataHandler verifies historical data endpoint.
func TestGetHistoricalDataHandler(t *testing.T) {
	cfg := &config.Config{
		TradingMode: "test",
	}
	mockProvider := new(MockDataProvider)

	// Mock successful data retrieval
	expectedData := []models.OHLCV{
		{Timestamp: time.Now(), Close: 150.0, Symbol: "AAPL"},
	}
	mockProvider.On("GetHistoricalData", "AAPL", mock.Anything, mock.Anything, "1d").Return(expectedData, nil)

	handler := NewHandler(nil, mockProvider, cfg, nil, nil, nil)

	t.Run("Success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/market/history?symbol=AAPL&interval=1d", nil)
		rec := httptest.NewRecorder()

		handler.GetHistoricalDataHandler(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var result []models.OHLCV
		err := json.Unmarshal(rec.Body.Bytes(), &result)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "AAPL", result[0].Symbol)
	})

	t.Run("MissingSymbol", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/market/history?interval=1d", nil)
		rec := httptest.NewRecorder()

		handler.GetHistoricalDataHandler(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("ProviderError", func(t *testing.T) {
		mockProvider.On("GetHistoricalData", "FAIL", mock.Anything, mock.Anything, "1d").Return(nil, assert.AnError)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/market/history?symbol=FAIL&interval=1d", nil)
		rec := httptest.NewRecorder()

		handler.GetHistoricalDataHandler(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}
