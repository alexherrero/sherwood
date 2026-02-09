package api

import (
	"bytes"
	"encoding/json"
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

	handler := NewHandler(registry, mockProvider, cfg, nil, nil)
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

	router := NewRouter(cfg, registry, mockProvider, nil, nil)

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
	router := NewRouter(cfg, registry, mockProvider, nil, nil)

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

	router := NewRouter(cfg, registry, mockProvider, nil, nil)
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

// MockBroker for testing execution endpoints
type MockBroker struct {
	mock.Mock
}

func (m *MockBroker) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockBroker) Connect() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockBroker) Disconnect() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockBroker) IsConnected() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockBroker) PlaceOrder(order models.Order) (*models.Order, error) {
	args := m.Called(order)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Order), args.Error(1)
}

func (m *MockBroker) CancelOrder(orderID string) error {
	args := m.Called(orderID)
	return args.Error(0)
}

func (m *MockBroker) GetOrder(orderID string) (*models.Order, error) {
	args := m.Called(orderID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Order), args.Error(1)
}

func (m *MockBroker) GetPositions() ([]models.Position, error) {
	args := m.Called()
	return args.Get(0).([]models.Position), args.Error(1)
}

func (m *MockBroker) GetPosition(symbol string) (*models.Position, error) {
	args := m.Called(symbol)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Position), args.Error(1)
}

func (m *MockBroker) GetBalance() (*models.Balance, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Balance), args.Error(1)
}

// TestExecutionEndpoints verifies /execution routes
func TestExecutionEndpoints(t *testing.T) {
	cfg := &config.Config{TradingMode: "test"}
	registry := strategies.NewRegistry()
	mockProvider := new(MockDataProvider)
	mockBroker := new(MockBroker)

	// Create OrderManager with MockBroker
	orderManager := execution.NewOrderManager(mockBroker, nil)

	handler := NewHandler(registry, mockProvider, cfg, orderManager, nil)

	// Test GetBalance
	t.Run("GetBalance", func(t *testing.T) {
		expectedBalance := &models.Balance{
			Cash:   100000,
			Equity: 100000,
		}
		mockBroker.On("GetBalance").Return(expectedBalance, nil).Once()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/execution/balance", nil)
		rec := httptest.NewRecorder()

		handler.GetBalanceHandler(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		var balance models.Balance
		err := json.Unmarshal(rec.Body.Bytes(), &balance)
		require.NoError(t, err)
		assert.Equal(t, 100000.0, balance.Cash)
	})

	// Test GetPositions
	t.Run("GetPositions", func(t *testing.T) {
		expectedPositions := []models.Position{
			{Symbol: "AAPL", Quantity: 10, AverageCost: 150},
		}
		mockBroker.On("GetPositions").Return(expectedPositions, nil).Once()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/execution/positions", nil)
		rec := httptest.NewRecorder()

		handler.GetPositionsHandler(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		var positions []models.Position
		err := json.Unmarshal(rec.Body.Bytes(), &positions)
		require.NoError(t, err)
		assert.Len(t, positions, 1)
		assert.Equal(t, "AAPL", positions[0].Symbol)
	})

	// Test GetOrders
	t.Run("GetOrders", func(t *testing.T) {
		// Just verify it calls implementation (OrderManager.GetAllOrders doesn't call broker usually, it returns local)
		// But in our current OrderManager implementation, it only stores submitted orders.
		// Since we haven't submitted any, it should be empty.

		req := httptest.NewRequest(http.MethodGet, "/api/v1/execution/orders", nil)
		rec := httptest.NewRecorder()

		handler.GetOrdersHandler(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		var orders []models.Order
		err := json.Unmarshal(rec.Body.Bytes(), &orders)
		require.NoError(t, err)
		assert.Empty(t, orders)
	})
}

// TestPlaceOrderHandler verifies manual order placement.
func TestPlaceOrderHandler(t *testing.T) {
	cfg := &config.Config{TradingMode: "test"}
	registry := strategies.NewRegistry()
	mockProvider := new(MockDataProvider)
	mockBroker := new(MockBroker)

	// Create OrderManager with MockBroker
	orderManager := execution.NewOrderManager(mockBroker, nil)

	handler := NewHandler(registry, mockProvider, cfg, orderManager, nil)

	t.Run("MarketBuy", func(t *testing.T) {
		// Expectation: broker.PlaceOrder called
		expectedOrder := &models.Order{
			ID:       "test-order-1",
			Symbol:   "AAPL",
			Side:     models.OrderSideBuy,
			Type:     models.OrderTypeMarket,
			Quantity: 10,
			Status:   models.OrderStatusFilled,
		}
		mockBroker.On("PlaceOrder", mock.MatchedBy(func(o models.Order) bool {
			return o.Symbol == "AAPL" && o.Side == models.OrderSideBuy && o.Type == models.OrderTypeMarket && o.Quantity == 10
		})).Return(expectedOrder, nil).Once()

		payload := map[string]interface{}{
			"symbol":   "AAPL",
			"side":     "buy",
			"type":     "market",
			"quantity": 10,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/execution/orders", bytes.NewReader(body))
		rec := httptest.NewRecorder()

		handler.PlaceOrderHandler(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		var order models.Order
		err := json.Unmarshal(rec.Body.Bytes(), &order)
		require.NoError(t, err)
		assert.Equal(t, "test-order-1", order.ID)
	})

	t.Run("InvalidInput", func(t *testing.T) {
		payload := map[string]interface{}{
			"symbol":   "", // Missing symbol
			"side":     "buy",
			"type":     "market",
			"quantity": 10,
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/execution/orders", bytes.NewReader(body))
		rec := httptest.NewRecorder()

		handler.PlaceOrderHandler(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}
