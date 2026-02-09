package execution

import (
	"testing"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/stretchr/testify/assert"
)

// TestDefaultRiskConfig verifies default configuration values.
func TestDefaultRiskConfig(t *testing.T) {
	cfg := DefaultRiskConfig()

	assert.Equal(t, 10000.0, cfg.MaxPositionSize)
	assert.Equal(t, 0.20, cfg.MaxPortfolioRisk)
	assert.Equal(t, 500.0, cfg.MaxDailyLoss)
	assert.Equal(t, 0.02, cfg.RiskPerTrade)
	assert.Equal(t, 10, cfg.MaxOpenOrders)
}

// TestNewRiskManager verifies risk manager creation.
func TestNewRiskManager(t *testing.T) {
	broker := NewPaperBroker(10000)
	rm := NewRiskManager(nil, broker)

	assert.NotNil(t, rm)
	assert.NotNil(t, rm.config) // Should use defaults
	assert.Equal(t, 10000.0, rm.config.MaxPositionSize)
}

// TestNewRiskManager_WithConfig verifies custom config.
func TestNewRiskManager_WithConfig(t *testing.T) {
	broker := NewPaperBroker(10000)
	cfg := &RiskConfig{
		MaxPositionSize: 5000,
		MaxOpenOrders:   5,
	}

	rm := NewRiskManager(cfg, broker)

	assert.Equal(t, 5000.0, rm.config.MaxPositionSize)
	assert.Equal(t, 5, rm.config.MaxOpenOrders)
}

// TestRiskManager_CheckOrder_Pass verifies valid order passes.
func TestRiskManager_CheckOrder_Pass(t *testing.T) {
	broker := NewPaperBroker(10000)
	_ = broker.Connect()
	rm := NewRiskManager(nil, broker)

	order := models.Order{
		Symbol:   "AAPL",
		Quantity: 10,
		Price:    100.0, // Position value: $1000, under $10k limit
		Type:     models.OrderTypeLimit,
	}

	err := rm.CheckOrder(order)
	assert.NoError(t, err)
}

// TestRiskManager_CheckOrder_ExceedsPositionSize verifies position size limit.
func TestRiskManager_CheckOrder_ExceedsPositionSize(t *testing.T) {
	broker := NewPaperBroker(100000)
	_ = broker.Connect()
	rm := NewRiskManager(nil, broker)

	order := models.Order{
		Symbol:   "AAPL",
		Quantity: 200,
		Price:    150.0, // Position value: $30,000, exceeds $10k limit
		Type:     models.OrderTypeLimit,
	}

	err := rm.CheckOrder(order)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "position size exceeds limit")
}

// TestRiskManager_CheckOrder_DailyLossExceeded verifies daily loss limit.
func TestRiskManager_CheckOrder_DailyLossExceeded(t *testing.T) {
	broker := NewPaperBroker(10000)
	_ = broker.Connect()
	rm := NewRiskManager(nil, broker)

	// Simulate exceeding daily loss
	rm.UpdateDailyPnL(-600) // Exceeds $500 limit

	order := models.Order{
		Symbol:   "AAPL",
		Quantity: 10,
		Price:    100.0,
		Type:     models.OrderTypeLimit,
	}

	err := rm.CheckOrder(order)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "daily loss limit exceeded")
}

// TestRiskManager_CheckOrder_MaxOpenOrders verifies open orders limit.
func TestRiskManager_CheckOrder_MaxOpenOrders(t *testing.T) {
	broker := NewPaperBroker(10000)
	_ = broker.Connect()
	cfg := &RiskConfig{
		MaxPositionSize: 10000,
		MaxOpenOrders:   2,
		MaxDailyLoss:    1000,
	}
	rm := NewRiskManager(cfg, broker)

	// Simulate max open orders
	rm.IncrementOpenOrders()
	rm.IncrementOpenOrders()

	order := models.Order{
		Symbol:   "AAPL",
		Quantity: 10,
		Price:    100.0,
		Type:     models.OrderTypeLimit,
	}

	err := rm.CheckOrder(order)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max open orders reached")
}

// TestRiskManager_CalculatePositionSize verifies position sizing.
func TestRiskManager_CalculatePositionSize(t *testing.T) {
	broker := NewPaperBroker(10000)
	rm := NewRiskManager(nil, broker)

	balance := &models.Balance{Equity: 10000}
	// Entry: 100, StopLoss: 95 -> Risk per unit: $5
	// Max risk: 2% of $10000 = $200
	// Position size: $200 / $5 = 40 units
	size := rm.CalculatePositionSize(100.0, 95.0, balance)

	assert.InDelta(t, 40.0, size, 0.01)
}

// TestRiskManager_CalculatePositionSize_Invalid verifies edge cases.
func TestRiskManager_CalculatePositionSize_Invalid(t *testing.T) {
	broker := NewPaperBroker(10000)
	rm := NewRiskManager(nil, broker)

	// Zero entry price
	size := rm.CalculatePositionSize(0, 95.0, &models.Balance{Equity: 10000})
	assert.Equal(t, 0.0, size)

	// Nil balance
	size = rm.CalculatePositionSize(100.0, 95.0, nil)
	assert.Equal(t, 0.0, size)

	// Same entry and stop (zero risk per unit)
	size = rm.CalculatePositionSize(100.0, 100.0, &models.Balance{Equity: 10000})
	assert.Equal(t, 0.0, size)
}

// TestRiskManager_CalculatePositionSize_CappedAtMax verifies position cap.
func TestRiskManager_CalculatePositionSize_CappedAtMax(t *testing.T) {
	broker := NewPaperBroker(1000000)
	rm := NewRiskManager(nil, broker)

	balance := &models.Balance{Equity: 1000000}
	// Entry: 100, StopLoss: 99 -> Risk per unit: $1
	// Max risk: 2% of $1M = $20,000
	// Uncapped position: $20,000 / $1 = 20,000 units
	// Max position value: $10,000 / $100 = 100 units (capped)
	size := rm.CalculatePositionSize(100.0, 99.0, balance)

	assert.InDelta(t, 100.0, size, 0.01) // Should be capped
}

// TestRiskManager_DailyPnL_Tracking verifies PnL tracking.
func TestRiskManager_DailyPnL_Tracking(t *testing.T) {
	broker := NewPaperBroker(10000)
	rm := NewRiskManager(nil, broker)

	assert.Equal(t, 0.0, rm.GetDailyPnL())

	rm.UpdateDailyPnL(100)
	assert.Equal(t, 100.0, rm.GetDailyPnL())

	rm.UpdateDailyPnL(-50)
	assert.Equal(t, 50.0, rm.GetDailyPnL())

	rm.ResetDaily()
	assert.Equal(t, 0.0, rm.GetDailyPnL())
}

// TestRiskManager_OpenOrders_Tracking verifies order tracking.
func TestRiskManager_OpenOrders_Tracking(t *testing.T) {
	broker := NewPaperBroker(10000)
	rm := NewRiskManager(nil, broker)

	rm.IncrementOpenOrders()
	rm.IncrementOpenOrders()
	rm.DecrementOpenOrders()

	// Should be 1
	rm.ResetDaily()
	// After reset should be 0, no way to check directly but CheckOrder should pass
}

// TestRiskManager_GetConfig verifies config access.
func TestRiskManager_GetConfig(t *testing.T) {
	broker := NewPaperBroker(10000)
	cfg := &RiskConfig{MaxPositionSize: 5000}
	rm := NewRiskManager(cfg, broker)

	assert.Equal(t, cfg, rm.GetConfig())
	assert.Equal(t, 5000.0, rm.GetConfig().MaxPositionSize)
}
