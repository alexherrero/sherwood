package execution

import (
	"testing"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPaperBroker_NewPaperBroker verifies broker creation.
func TestPaperBroker_NewPaperBroker(t *testing.T) {
	broker := NewPaperBroker(10000.0)
	assert.NotNil(t, broker)
	assert.Equal(t, "paper", broker.Name())
	assert.False(t, broker.IsConnected())

	balance, err := broker.GetBalance()
	require.NoError(t, err)
	assert.Equal(t, 10000.0, balance.Cash)
	assert.Equal(t, 10000.0, balance.BuyingPower)
}

// TestPaperBroker_Connect verifies connection lifecycle.
func TestPaperBroker_Connect(t *testing.T) {
	broker := NewPaperBroker(10000.0)

	assert.False(t, broker.IsConnected())

	err := broker.Connect()
	require.NoError(t, err)
	assert.True(t, broker.IsConnected())

	err = broker.Disconnect()
	require.NoError(t, err)
	assert.False(t, broker.IsConnected())
}

// TestPaperBroker_PlaceOrder_NotConnected verifies error when not connected.
func TestPaperBroker_PlaceOrder_NotConnected(t *testing.T) {
	broker := NewPaperBroker(10000.0)

	order := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeMarket,
		Quantity: 10,
	}

	_, err := broker.PlaceOrder(order)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not connected")
}

// TestPaperBroker_PlaceOrder_MarketBuy verifies market buy order execution.
func TestPaperBroker_PlaceOrder_MarketBuy(t *testing.T) {
	broker := NewPaperBroker(10000.0)
	require.NoError(t, broker.Connect())

	// Set price for market order
	broker.SetPrice("AAPL", 150.0)

	order := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeMarket,
		Quantity: 10,
	}

	result, err := broker.PlaceOrder(order)
	require.NoError(t, err)
	assert.Equal(t, models.OrderStatusFilled, result.Status)
	assert.Equal(t, 150.0, result.AveragePrice)
	assert.Equal(t, 10.0, result.FilledQuantity)

	// Check balance updated
	balance, _ := broker.GetBalance()
	assert.Equal(t, 10000.0-1500.0, balance.Cash)

	// Check position created
	pos, err := broker.GetPosition("AAPL")
	require.NoError(t, err)
	assert.Equal(t, 10.0, pos.Quantity)
	assert.Equal(t, 150.0, pos.AverageCost)
}

// TestPaperBroker_PlaceOrder_InsufficientFunds verifies rejection on insufficient funds.
func TestPaperBroker_PlaceOrder_InsufficientFunds(t *testing.T) {
	broker := NewPaperBroker(100.0) // Low balance
	require.NoError(t, broker.Connect())
	broker.SetPrice("AAPL", 150.0)

	order := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeMarket,
		Quantity: 10, // Cost: $1500, but only have $100
	}

	result, err := broker.PlaceOrder(order)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient buying power")
	assert.Equal(t, models.OrderStatusRejected, result.Status)
}

// TestPaperBroker_PlaceOrder_LimitBuy verifies limit buy order execution.
func TestPaperBroker_PlaceOrder_LimitBuy(t *testing.T) {
	broker := NewPaperBroker(10000.0)
	require.NoError(t, broker.Connect())

	// 1. Place limit order without current price -> Pending
	order := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeLimit,
		Quantity: 10,
		Price:    145.0, // Limit price
	}

	result, err := broker.PlaceOrder(order)
	require.NoError(t, err)
	assert.Equal(t, models.OrderStatusPending, result.Status)

	// 2. Set price that triggers fill (price <= limit)
	broker.SetPrice("AAPL", 140.0)

	// Logic to trigger fill in broker needs to be called.
	// Broker currently only checks fill on PlaceOrder submission.
	// We need a way to trigger check or just simulate re-submission behavior?
	// Or we just submit a new Limit order that fills immediately now that price is set.

	order2 := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeLimit,
		Quantity: 10,
		Price:    145.0,
	}
	result2, err := broker.PlaceOrder(order2)
	require.NoError(t, err)
	assert.Equal(t, models.OrderStatusFilled, result2.Status)
	assert.Equal(t, 145.0, result2.AveragePrice) // Fills at limit price per our impl
}

func TestPaperBroker_CancelOrder_Pending(t *testing.T) {
	broker := NewPaperBroker(10000.0)
	require.NoError(t, broker.Connect())

	// Place pending limit order (no price set)
	order := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeLimit,
		Quantity: 10,
		Price:    145.0,
	}
	result, _ := broker.PlaceOrder(order)
	assert.Equal(t, models.OrderStatusPending, result.Status)

	// Cancel it
	err := broker.CancelOrder(result.ID)
	require.NoError(t, err)

	// Verify cancelled status
	retrieved, _ := broker.GetOrder(result.ID)
	assert.Equal(t, models.OrderStatusCancelled, retrieved.Status)
}

// TestPaperBroker_PlaceOrder_Sell verifies sell order execution.
func TestPaperBroker_PlaceOrder_Sell(t *testing.T) {
	broker := NewPaperBroker(10000.0)
	require.NoError(t, broker.Connect())
	broker.SetPrice("AAPL", 150.0)

	// First buy some shares
	buyOrder := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeMarket,
		Quantity: 10,
	}
	_, err := broker.PlaceOrder(buyOrder)
	require.NoError(t, err)

	// Now sell
	broker.SetPrice("AAPL", 160.0) // Price went up
	sellOrder := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideSell,
		Type:     models.OrderTypeMarket,
		Quantity: 5,
	}

	result, err := broker.PlaceOrder(sellOrder)
	require.NoError(t, err)
	assert.Equal(t, models.OrderStatusFilled, result.Status)

	// Check position reduced
	pos, err := broker.GetPosition("AAPL")
	require.NoError(t, err)
	assert.Equal(t, 5.0, pos.Quantity)

	// Check balance increased
	balance, _ := broker.GetBalance()
	// Started: 10000, bought 10 @ 150 = -1500, sold 5 @ 160 = +800
	// Final: 10000 - 1500 + 800 = 9300
	assert.Equal(t, 9300.0, balance.Cash)
}

// TestPaperBroker_PlaceOrder_SellAll verifies position is removed when fully sold.
func TestPaperBroker_PlaceOrder_SellAll(t *testing.T) {
	broker := NewPaperBroker(10000.0)
	require.NoError(t, broker.Connect())
	broker.SetPrice("AAPL", 100.0)

	// Buy
	buyOrder := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeMarket,
		Quantity: 10,
	}
	_, _ = broker.PlaceOrder(buyOrder)

	// Sell all
	sellOrder := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideSell,
		Type:     models.OrderTypeMarket,
		Quantity: 10,
	}
	_, err := broker.PlaceOrder(sellOrder)
	require.NoError(t, err)

	// Position should be gone
	_, err = broker.GetPosition("AAPL")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no position")
}

// TestPaperBroker_GetOrder verifies order retrieval.
func TestPaperBroker_GetOrder(t *testing.T) {
	broker := NewPaperBroker(10000.0)
	require.NoError(t, broker.Connect())
	broker.SetPrice("AAPL", 100.0)

	order := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeMarket,
		Quantity: 1,
	}

	result, _ := broker.PlaceOrder(order)

	retrieved, err := broker.GetOrder(result.ID)
	require.NoError(t, err)
	assert.Equal(t, result.ID, retrieved.ID)

	// Non-existent order
	_, err = broker.GetOrder("invalid-id")
	assert.Error(t, err)
}

// TestPaperBroker_CancelOrder verifies order cancellation.
func TestPaperBroker_CancelOrder(t *testing.T) {
	broker := NewPaperBroker(10000.0)
	require.NoError(t, broker.Connect())
	broker.SetPrice("AAPL", 100.0)

	// In paper broker, orders are filled instantly, so cancellation fails
	order := models.Order{
		Symbol:   "AAPL",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeMarket,
		Quantity: 1,
	}

	result, _ := broker.PlaceOrder(order)

	err := broker.CancelOrder(result.ID)
	assert.Error(t, err) // Already filled
	assert.Contains(t, err.Error(), "cannot cancel filled order")
}

// TestPaperBroker_GetPositions verifies retrieving all positions.
func TestPaperBroker_GetPositions(t *testing.T) {
	broker := NewPaperBroker(10000.0)
	require.NoError(t, broker.Connect())
	broker.SetPrice("AAPL", 100.0)
	broker.SetPrice("GOOGL", 150.0)

	// Buy multiple symbols
	_, _ = broker.PlaceOrder(models.Order{
		Symbol: "AAPL", Side: models.OrderSideBuy, Type: models.OrderTypeMarket, Quantity: 5,
	})
	_, _ = broker.PlaceOrder(models.Order{
		Symbol: "GOOGL", Side: models.OrderSideBuy, Type: models.OrderTypeMarket, Quantity: 3,
	})

	positions, err := broker.GetPositions()
	require.NoError(t, err)
	assert.Len(t, positions, 2)
}

func TestPaperBroker_GetTrades(t *testing.T) {
	broker := NewPaperBroker(10000.0)
	require.NoError(t, broker.Connect())
	broker.SetPrice("AAPL", 100.0)

	// Create two trades
	_, _ = broker.PlaceOrder(models.Order{
		Symbol: "AAPL", Side: models.OrderSideBuy, Type: models.OrderTypeMarket, Quantity: 5,
	})
	_, _ = broker.PlaceOrder(models.Order{
		Symbol: "AAPL", Side: models.OrderSideBuy, Type: models.OrderTypeMarket, Quantity: 2,
	})

	trades, err := broker.GetTrades()
	require.NoError(t, err)
	assert.Len(t, trades, 2)
}

// TestPaperBroker_MarketOrder_NoPrice verifies error when no price available.
func TestPaperBroker_MarketOrder_NoPrice(t *testing.T) {
	broker := NewPaperBroker(10000.0)
	require.NoError(t, broker.Connect())
	// Note: NOT setting price for symbol

	order := models.Order{
		Symbol:   "UNKNOWN",
		Side:     models.OrderSideBuy,
		Type:     models.OrderTypeMarket,
		Quantity: 10,
	}

	_, err := broker.PlaceOrder(order)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no price available")
}
