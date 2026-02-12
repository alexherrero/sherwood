package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/alexherrero/sherwood/backend/execution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOrderHistoryHandler(t *testing.T) {
	mockBroker := new(MockBroker)
	orderManager := execution.NewOrderManager(mockBroker, nil, nil, nil)
	handler := NewHandler(nil, nil, &config.Config{}, orderManager, nil, nil)

	t.Run("Success", func(t *testing.T) {
		// Mock GetOrders call (via OrderManager loop/pass-through)
		// Since OrderManager.GetOrders is complex and uses internal storage,
		// mocking it requires mocking the storage or accepting that it returns empty/error if not set up.
		// However, OrderManager uses an interface for persistence but stores active orders in memory.
		// GetOrders logic in OrderManager iterates memory + storage.

		// To truly test this without complex setup, we might need to mock OrderManager itself
		// but OrderManager is a struct, not an interface.
		// We can test the handler's interaction with the request parsing and response writing at least.

		// For now, let's just ensure it calls GetOrdersHandler which we know works if GetOrders works.
		// Actually, GetOrderHistoryHandler just calls GetOrdersHandler.

		req := httptest.NewRequest(http.MethodGet, "/api/v1/orders/history", nil)
		rec := httptest.NewRecorder()

		handler.GetOrderHistoryHandler(rec, req)

		// Since order manager has no orders, it should return empty list
		assert.Equal(t, http.StatusOK, rec.Code)

		var response map[string]interface{}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Empty(t, response["orders"])
	})

	t.Run("ServiceUnavailable", func(t *testing.T) {
		nilHandler := NewHandler(nil, nil, nil, nil, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/orders/history", nil)
		rec := httptest.NewRecorder()

		nilHandler.GetOrderHistoryHandler(rec, req)

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})
}

func TestPlaceOrder_Errors(t *testing.T) {
	mockBroker := new(MockBroker)
	orderManager := execution.NewOrderManager(mockBroker, nil, nil, nil)
	handler := NewHandler(nil, nil, &config.Config{}, orderManager, nil, nil)

	t.Run("InvalidJSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/orders", nil) // Empty body
		rec := httptest.NewRecorder()

		handler.PlaceOrderHandler(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("InvalidSide", func(t *testing.T) {
		payload := map[string]interface{}{
			"symbol":   "AAPL",
			"side":     "invalid",
			"type":     "market",
			"quantity": 1,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewReader(body))
		rec := httptest.NewRecorder()

		handler.PlaceOrderHandler(rec, req)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code) // Validation fails "oneof=buy sell"
	})

	t.Run("LimitOrderNoPrice", func(t *testing.T) {
		payload := map[string]interface{}{
			"symbol":   "AAPL",
			"side":     "buy",
			"type":     "limit",
			"quantity": 1,
			"price":    0, // Invalid for limit
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewReader(body))
		rec := httptest.NewRecorder()

		handler.PlaceOrderHandler(rec, req)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	})
}

func TestModifyOrder_Errors(t *testing.T) {
	mockBroker := new(MockBroker)
	orderManager := execution.NewOrderManager(mockBroker, nil, nil, nil)
	handler := NewHandler(nil, nil, &config.Config{}, orderManager, nil, nil)

	t.Run("InvalidJSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/orders/1", nil)
		rec := httptest.NewRecorder()

		handler.ModifyOrderHandler(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("NoChanges", func(t *testing.T) {
		payload := map[string]interface{}{}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/orders/1", bytes.NewReader(body))
		rec := httptest.NewRecorder()

		handler.ModifyOrderHandler(rec, req)
		// Empty payload validates ok structurally but logic checks for price or qty
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}
