package execution

import (
	"testing"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewOrderManager verifies order manager creation.
func TestNewOrderManager(t *testing.T) {
	broker := NewPaperBroker(10000)
	rm := NewRiskManager(nil, broker)

	om := NewOrderManager(broker, rm)

	assert.NotNil(t, om)
}

// TestOrderManager_SubmitOrder_Success verifies successful order submission.
func TestOrderManager_SubmitOrder_Success(t *testing.T) {
	broker := NewPaperBroker(10000)
	require.NoError(t, broker.Connect())
	broker.SetPrice("AAPL", 150.0)

	rm := NewRiskManager(nil, broker)
	om := NewOrderManager(broker, rm)

	order := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeMarket,
		Quantity: 10,
	}

	result, err := om.SubmitOrder(order)
	require.NoError(t, err)
	assert.NotEmpty(t, result.ID)
	assert.Equal(t, models.OrderStatusFilled, result.Status)
}

// TestOrderManager_SubmitOrder_ValidationFails verifies validation errors.
func TestOrderManager_SubmitOrder_ValidationFails(t *testing.T) {
	broker := NewPaperBroker(10000)
	require.NoError(t, broker.Connect())

	om := NewOrderManager(broker, nil)

	tests := []struct {
		name        string
		order       models.Order
		errContains string
	}{
		{
			name: "empty symbol",
			order: models.Order{
				Symbol:   "",
				Quantity: 10,
			},
			errContains: "symbol is required",
		},
		{
			name: "zero quantity",
			order: models.Order{
				Symbol:   "AAPL",
				Quantity: 0,
			},
			errContains: "quantity must be positive",
		},
		{
			name: "negative quantity",
			order: models.Order{
				Symbol:   "AAPL",
				Quantity: -10,
			},
			errContains: "quantity must be positive",
		},
		{
			name: "limit order without price",
			order: models.Order{
				Symbol:   "AAPL",
				Type:     models.OrderTypeLimit,
				Quantity: 10,
				Price:    0,
			},
			errContains: "limit orders require a positive price",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := om.SubmitOrder(tt.order)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

// TestOrderManager_SubmitOrder_RiskCheckFails verifies risk rejections.
func TestOrderManager_SubmitOrder_RiskCheckFails(t *testing.T) {
	broker := NewPaperBroker(10000)
	require.NoError(t, broker.Connect())

	rm := NewRiskManager(nil, broker)
	rm.UpdateDailyPnL(-600) // Exceed daily loss limit

	om := NewOrderManager(broker, rm)

	order := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeLimit,
		Quantity: 10,
		Price:    100.0,
	}

	_, err := om.SubmitOrder(order)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "risk check failed")
}

// TestOrderManager_CancelOrder verifies order cancellation.
func TestOrderManager_CancelOrder(t *testing.T) {
	broker := NewPaperBroker(10000)
	require.NoError(t, broker.Connect())
	broker.SetPrice("AAPL", 100.0)

	om := NewOrderManager(broker, nil)

	// Place an order first
	order := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeMarket,
		Quantity: 1,
	}
	result, _ := om.SubmitOrder(order)

	// Paper broker fills instantly, so cancel will fail
	err := om.CancelOrder(result.ID)
	assert.Error(t, err)
}

// TestOrderManager_GetOrder verifies order retrieval.
func TestOrderManager_GetOrder(t *testing.T) {
	broker := NewPaperBroker(10000)
	require.NoError(t, broker.Connect())
	broker.SetPrice("AAPL", 100.0)

	om := NewOrderManager(broker, nil)

	order := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeMarket,
		Quantity: 1,
	}
	result, _ := om.SubmitOrder(order)

	retrieved, err := om.GetOrder(result.ID)
	require.NoError(t, err)
	assert.Equal(t, result.ID, retrieved.ID)
}

// TestOrderManager_GetAllOrders verifies getting all orders.
func TestOrderManager_GetAllOrders(t *testing.T) {
	broker := NewPaperBroker(10000)
	require.NoError(t, broker.Connect())
	broker.SetPrice("AAPL", 100.0)
	broker.SetPrice("GOOGL", 150.0)

	om := NewOrderManager(broker, nil)

	_, _ = om.SubmitOrder(models.Order{Symbol: "AAPL", Side: models.OrderSideBuy, Type: models.OrderTypeMarket, Quantity: 1})
	_, _ = om.SubmitOrder(models.Order{Symbol: "GOOGL", Side: models.OrderSideBuy, Type: models.OrderTypeMarket, Quantity: 1})

	orders := om.GetAllOrders()
	assert.Len(t, orders, 2)
}

// TestOrderManager_CreateMarketOrder verifies market order creation.
func TestOrderManager_CreateMarketOrder(t *testing.T) {
	broker := NewPaperBroker(10000)
	require.NoError(t, broker.Connect())
	broker.SetPrice("AAPL", 100.0)

	om := NewOrderManager(broker, nil)

	result, err := om.CreateMarketOrder("AAPL", models.OrderSideBuy, 5)
	require.NoError(t, err)
	assert.Equal(t, "AAPL", result.Symbol)
	assert.Equal(t, models.OrderTypeMarket, result.Type)
	assert.Equal(t, 5.0, result.Quantity)
}

// TestOrderManager_CreateLimitOrder verifies limit order creation.
func TestOrderManager_CreateLimitOrder(t *testing.T) {
	broker := NewPaperBroker(10000)
	require.NoError(t, broker.Connect())

	om := NewOrderManager(broker, nil)

	result, err := om.CreateLimitOrder("AAPL", models.OrderSideBuy, 5, 145.0)
	require.NoError(t, err)
	assert.Equal(t, "AAPL", result.Symbol)
	assert.Equal(t, models.OrderTypeLimit, result.Type)
	assert.Equal(t, 5.0, result.Quantity)
	assert.Equal(t, 145.0, result.AveragePrice) // Paper broker fills at limit price
}

// TestOrderManager_SubmitOrder_NoRiskManager verifies nil risk manager.
func TestOrderManager_SubmitOrder_NoRiskManager(t *testing.T) {
	broker := NewPaperBroker(10000)
	require.NoError(t, broker.Connect())
	broker.SetPrice("AAPL", 100.0)

	om := NewOrderManager(broker, nil) // No risk manager

	order := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeMarket,
		Quantity: 10,
	}

	result, err := om.SubmitOrder(order)
	require.NoError(t, err)
	assert.NotNil(t, result)
}
