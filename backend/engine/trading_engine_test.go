package engine

import (
	"context"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/execution"
	"github.com/alexherrero/sherwood/backend/models"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/stretchr/testify/mock"
)

// MockProvider
type MockProvider struct {
	mock.Mock
}

func (m *MockProvider) Name() string { return "Mock" }

func (m *MockProvider) GetLatestPrice(symbol string) (float64, error) {
	args := m.Called(symbol)
	return args.Get(0).(float64), args.Error(1)
}

func (m *MockProvider) GetTicker(symbol string) (*models.Ticker, error) {
	args := m.Called(symbol)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Ticker), args.Error(1)
}

func (m *MockProvider) GetHistoricalData(symbol string, start, end time.Time, interval string) ([]models.OHLCV, error) {
	args := m.Called(symbol, start, end, interval)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.OHLCV), args.Error(1)
}

// MockStrategy
type MockStrategy struct {
	mock.Mock
	strategies.BaseStrategy
}

func (m *MockStrategy) Name() string        { return "MockStrategy" }
func (m *MockStrategy) Description() string { return "Mock Strategy for Testing" }
func (m *MockStrategy) OnData(data []models.OHLCV) models.Signal {
	args := m.Called(data)
	return args.Get(0).(models.Signal)
}

// Implement other required methods with dummy implementations
func (m *MockStrategy) Init(config map[string]interface{}) error       { return nil }
func (m *MockStrategy) Validate() error                                { return nil }
func (m *MockStrategy) GetParameters() map[string]strategies.Parameter { return nil }

// MockBroker
type MockBroker struct {
	mock.Mock
}

func (m *MockBroker) Name() string      { return "MockBroker" }
func (m *MockBroker) Connect() error    { return nil }
func (m *MockBroker) Disconnect() error { return nil }
func (m *MockBroker) IsConnected() bool { return true }
func (m *MockBroker) PlaceOrder(order models.Order) (*models.Order, error) {
	args := m.Called(order)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Order), args.Error(1)
}

// Implement other methods
func (m *MockBroker) CancelOrder(id string) error                    { return nil }
func (m *MockBroker) GetOrder(id string) (*models.Order, error)      { return nil, nil }
func (m *MockBroker) GetPositions() ([]models.Position, error)       { return nil, nil }
func (m *MockBroker) GetPosition(s string) (*models.Position, error) { return nil, nil }
func (m *MockBroker) GetBalance() (*models.Balance, error)           { return nil, nil }
func (m *MockBroker) GetTrades() ([]models.Trade, error)             { return nil, nil }
func (m *MockBroker) ModifyOrder(id string, p, q float64) (*models.Order, error) {
	return nil, nil
}

func TestTradingEngine_RunLoop(t *testing.T) {
	// Setup Mocks
	mockProvider := new(MockProvider)
	mockStrategy := new(MockStrategy)
	mockBroker := new(MockBroker)

	// Setup Strategy Registry
	registry := strategies.NewRegistry()
	registry.Register(mockStrategy)

	// Setup Order Manager
	orderManager := execution.NewOrderManager(mockBroker, nil, nil, nil)

	// Setup Engine
	engine := NewTradingEngine(
		mockProvider,
		registry,
		orderManager,
		nil,
		[]string{"AAPL"},
		10*time.Millisecond,
		24*time.Hour,
	)

	// Expectation: GetHistoricalData called
	mockProvider.On("GetHistoricalData", "AAPL", mock.Anything, mock.Anything, "1d").
		Return([]models.OHLCV{{Close: 150.0}}, nil)

	// Expectation: Strategy OnData called -> Returns Buy Signal
	mockStrategy.On("OnData", mock.Anything).Return(models.Signal{
		Type:         models.SignalBuy,
		Symbol:       "AAPL",
		Quantity:     10,
		StrategyName: "MockStrategy",
	})

	// Expectation: Broker PlaceOrder called
	mockBroker.On("PlaceOrder", mock.MatchedBy(func(o models.Order) bool {
		return o.Symbol == "AAPL" && o.Side == models.OrderSideBuy && o.Quantity == 10
	})).Return(&models.Order{ID: "order-1", Status: models.OrderStatusSubmitted}, nil)

	// Run Engine
	ctx, cancel := context.WithCancel(context.Background())
	engine.Start(ctx)

	// Let it tick once or twice
	time.Sleep(50 * time.Millisecond)

	// Stop
	cancel()
	engine.Stop()

	// Verify
	mockProvider.AssertExpectations(t)
	mockBroker.AssertExpectations(t)
}

func TestTradingEngine_StopIdempotency(t *testing.T) {
	mockProvider := new(MockProvider)
	mockBroker := new(MockBroker)
	registry := strategies.NewRegistry()
	orderManager := execution.NewOrderManager(mockBroker, nil, nil, nil)

	engine := NewTradingEngine(
		mockProvider,
		registry,
		orderManager,
		nil,
		[]string{"AAPL"},
		10*time.Millisecond,
		24*time.Hour,
	)

	// Expectation: Provider might be called
	mockProvider.On("GetHistoricalData", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]models.OHLCV{}, nil).Maybe()

	// Start engine
	ctx, cancel := context.WithCancel(context.Background())
	engine.Start(ctx)

	// Stop twice
	time.Sleep(10 * time.Millisecond)
	cancel()
	engine.Stop()
	engine.Stop() // Should not panic or error

	// Start again (should handle gracefully if logic allows, or just log)
	// Current impl: Start creates new goroutine.
}

func TestTradingEngine_ProviderError(t *testing.T) {
	mockProvider := new(MockProvider)
	mockBroker := new(MockBroker)
	registry := strategies.NewRegistry()
	mockStrategy := new(MockStrategy)
	registry.Register(mockStrategy)
	orderManager := execution.NewOrderManager(mockBroker, nil, nil, nil)

	engine := NewTradingEngine(
		mockProvider,
		registry,
		orderManager,
		nil,
		[]string{"AAPL"},
		10*time.Millisecond,
		24*time.Hour,
	)

	// Expectation: Provider returns error
	// Use Maybe() or allow multiple calls because ticker might fire multiple times
	mockProvider.On("GetHistoricalData", "AAPL", mock.Anything, mock.Anything, "1d").
		Return(nil, context.DeadlineExceeded)

	// Start engine
	ctx, cancel := context.WithCancel(context.Background())
	engine.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	cancel()
	engine.Stop()

	mockProvider.AssertExpectations(t)
	// Ensure strategy was NOT called due to provider error
	mockStrategy.AssertNotCalled(t, "OnData")
}

func TestTradingEngine_LimitOrder(t *testing.T) {
	// Setup Mocks
	mockProvider := new(MockProvider)
	mockStrategy := new(MockStrategy)
	mockBroker := new(MockBroker)

	// Setup Strategy Registry
	registry := strategies.NewRegistry()
	registry.Register(mockStrategy)

	// Setup Order Manager
	orderManager := execution.NewOrderManager(mockBroker, nil, nil, nil)

	// Setup Engine
	engine := NewTradingEngine(
		mockProvider,
		registry,
		orderManager,
		nil,
		[]string{"MSFT"},
		10*time.Millisecond,
		24*time.Hour,
	)

	// Expectation: GetHistoricalData called
	mockProvider.On("GetHistoricalData", "MSFT", mock.Anything, mock.Anything, "1d").
		Return([]models.OHLCV{{Close: 300.0}}, nil)

	// Expectation: Strategy OnData called -> Returns Buy Signal with Price (Limit Order)
	mockStrategy.On("OnData", mock.Anything).Return(models.Signal{
		Type:         models.SignalBuy,
		Symbol:       "MSFT",
		Quantity:     5,
		Price:        295.0, // Limit Price
		StrategyName: "MockStrategy",
	})

	// Expectation: Broker PlaceOrder called for LIMIT order
	mockBroker.On("PlaceOrder", mock.MatchedBy(func(o models.Order) bool {
		return o.Symbol == "MSFT" &&
			o.Side == models.OrderSideBuy &&
			o.Quantity == 5 &&
			o.Type == models.OrderTypeLimit &&
			o.Price == 295.0
	})).Return(&models.Order{ID: "order-2", Status: models.OrderStatusSubmitted}, nil)

	// Run Engine
	ctx, cancel := context.WithCancel(context.Background())
	engine.Start(ctx)

	// Let it tick
	time.Sleep(50 * time.Millisecond)

	// Stop
	cancel()
	engine.Stop()

	// Verify
	mockProvider.AssertExpectations(t)
	mockBroker.AssertExpectations(t)
}
