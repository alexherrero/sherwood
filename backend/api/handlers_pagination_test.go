package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/config"
	"github.com/alexherrero/sherwood/backend/execution"
	"github.com/alexherrero/sherwood/backend/models"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetOrdersPaginationAndFiltering(t *testing.T) {
	cfg := &config.Config{AllowedOrigins: []string{"http://localhost:3000"}}
	registry := strategies.NewRegistry()
	mockProvider := new(MockDataProvider)
	mockBroker := new(MockBroker)

	// Setup order manager and populate with orders
	orderManager := execution.NewOrderManager(mockBroker, nil, nil)

	// We need to inject orders into OrderManager.
	// Since SubmitOrder calls broker.PlaceOrder, we mock it to return successful orders.
	// We'll create 10 orders.
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("ord-%d", i)
		symbol := "AAPL"
		if i >= 5 {
			symbol = "GOOGL"
		}
		status := models.OrderStatusFilled
		if i%2 == 0 {
			status = models.OrderStatusPending
		}

		mockBroker.On("PlaceOrder", mock.Anything).Return(&models.Order{
			ID:        id,
			Symbol:    symbol,
			Status:    status,
			CreatedAt: time.Now().Add(time.Duration(i) * time.Minute), // Ascending time, but we sort Descending
			Side:      models.OrderSideBuy,
			Type:      models.OrderTypeMarket,
			Quantity:  10,
		}, nil).Once()

		_, err := orderManager.CreateMarketOrder(symbol, models.OrderSideBuy, 10)
		require.NoError(t, err)
	}

	handler := NewHandler(registry, mockProvider, cfg, orderManager, nil)

	t.Run("Pagination_Page1", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/execution/orders?limit=3&page=1", nil)
		rec := httptest.NewRecorder()
		handler.GetOrdersHandler(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		var resp map[string]interface{}
		json.Unmarshal(rec.Body.Bytes(), &resp)

		orders := resp["orders"].([]interface{})
		assert.Len(t, orders, 3)
		assert.Equal(t, float64(10), resp["total"])
		// Should be the latest 3 orders (9, 8, 7)
	})

	t.Run("Pagination_Page2", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/execution/orders?limit=3&page=2", nil)
		rec := httptest.NewRecorder()
		handler.GetOrdersHandler(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		var resp map[string]interface{}
		json.Unmarshal(rec.Body.Bytes(), &resp)

		orders := resp["orders"].([]interface{})
		assert.Len(t, orders, 3)
		// Should be (6, 5, 4)
	})

	t.Run("Filter_Symbol", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/execution/orders?symbol=GOOGL", nil)
		rec := httptest.NewRecorder()
		handler.GetOrdersHandler(rec, req)

		var resp map[string]interface{}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.Equal(t, float64(5), resp["total"]) // 5 GOOGL orders
	})

	t.Run("Filter_Status", func(t *testing.T) {
		// Pending status is when i%2 == 0 (0, 2, 4, 6, 8) -> 5 orders
		req := httptest.NewRequest(http.MethodGet, "/api/v1/execution/orders?status=pending", nil)
		rec := httptest.NewRecorder()
		handler.GetOrdersHandler(rec, req)

		var resp map[string]interface{}
		json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.Equal(t, float64(5), resp["total"])
	})
}

func TestGetOrderHandler(t *testing.T) {
	cfg := &config.Config{AllowedOrigins: []string{"http://localhost:3000"}}
	registry := strategies.NewRegistry()
	mockProvider := new(MockDataProvider)
	mockBroker := new(MockBroker)
	orderManager := execution.NewOrderManager(mockBroker, nil, nil)

	// Create one order
	mockBroker.On("PlaceOrder", mock.Anything).Return(&models.Order{
		ID: "test-id-1", Symbol: "AAPL",
	}, nil).Once()
	orderManager.CreateMarketOrder("AAPL", models.OrderSideBuy, 1)

	// Since NewRouter creates its own handler, we test Handler method directly or use Router
	// Let's use Router to test URL param parsing
	router := NewRouter(cfg, registry, mockProvider, orderManager, nil)

	t.Run("Approves_Valid_ID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/execution/orders/test-id-1", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("Returns_404_Invalid_ID", func(t *testing.T) {
		// MockBroker.GetOrder will be called for non-cached orders
		mockBroker.On("GetOrder", "missing-id").Return(nil, fmt.Errorf("not found")).Once()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/execution/orders/missing-id", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestPerformanceSummary(t *testing.T) {
	cfg := &config.Config{AllowedOrigins: []string{"http://localhost:3000"}}
	registry := strategies.NewRegistry()
	mockProvider := new(MockDataProvider)
	mockBroker := new(MockBroker)
	orderManager := execution.NewOrderManager(mockBroker, nil, nil)
	handler := NewHandler(registry, mockProvider, cfg, orderManager, nil)

	mockBroker.On("GetBalance").Return(&models.Balance{Cash: 50000, Equity: 60000}, nil)
	mockBroker.On("GetPositions").Return([]models.Position{
		{Symbol: "AAPL", UnrealizedPL: 5000},
		{Symbol: "MSFT", UnrealizedPL: 5000},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/portfolio/summary", nil)
	rec := httptest.NewRecorder()
	handler.GetPortfolioSummaryHandler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)

	assert.Equal(t, float64(10000), resp["total_unrealized_pl"])
	assert.Equal(t, float64(2), resp["open_positions"])
}
