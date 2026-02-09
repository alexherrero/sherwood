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

func TestTradingEngine_RunLoop(t *testing.T) {
	// Setup Mocks
	mockProvider := new(MockProvider)
	mockStrategy := new(MockStrategy)
	mockBroker := new(MockBroker)

	// Setup Strategy Registry
	registry := strategies.NewRegistry()
	registry.Register(mockStrategy)

	// Setup Order Manager
	orderManager := execution.NewOrderManager(mockBroker, nil, nil)

	// Setup Engine
	engine := NewTradingEngine(
		mockProvider,
		registry,
		orderManager,
		[]string{"AAPL"},
		10*time.Millisecond,
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
	mockStrategy.AssertExpectations(t)
	mockBroker.AssertExpectations(t)
}
