package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOrderConstants verifies order constants.
func TestOrderConstants(t *testing.T) {
	assert.Equal(t, OrderSide("buy"), OrderSideBuy)
	assert.Equal(t, OrderSide("sell"), OrderSideSell)

	assert.Equal(t, OrderType("market"), OrderTypeMarket)
	assert.Equal(t, OrderType("limit"), OrderTypeLimit)

	assert.Equal(t, OrderStatus("filled"), OrderStatusFilled)
	assert.Equal(t, OrderStatus("pending"), OrderStatusPending)
}

// TestOrder_JSON verifies JSON marshaling of Order.
func TestOrder_JSON(t *testing.T) {
	now := time.Now().Truncate(time.Second) // Truncate for JSON comparison
	order := Order{
		ID:        "123",
		Symbol:    "AAPL",
		Side:      OrderSideBuy,
		Type:      OrderTypeLimit,
		Quantity:  10.5,
		Price:     150.0,
		Status:    OrderStatusSubmitted,
		CreatedAt: now,
	}

	data, err := json.Marshal(order)
	require.NoError(t, err)

	var parsed Order
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, order.ID, parsed.ID)
	assert.Equal(t, order.Symbol, parsed.Symbol)
	assert.Equal(t, order.Side, parsed.Side)
	assert.Equal(t, order.Type, parsed.Type)
	assert.Equal(t, order.Quantity, parsed.Quantity)
	assert.Equal(t, order.Price, parsed.Price)
	assert.Equal(t, order.Status, parsed.Status)
	assert.True(t, order.CreatedAt.Equal(parsed.CreatedAt))
}

// TestTrade_JSON verifies JSON marshaling of Trade.
func TestTrade_JSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	trade := Trade{
		ID:         "t1",
		OrderID:    "o1",
		Symbol:     "AAPL",
		Side:       OrderSideSell,
		Quantity:   5.0,
		Price:      155.0,
		ExecutedAt: now,
	}

	data, err := json.Marshal(trade)
	require.NoError(t, err)

	var parsed Trade
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, trade.ID, parsed.ID)
	assert.Equal(t, trade.Symbol, parsed.Symbol)
	assert.Equal(t, trade.Quantity, parsed.Quantity)
	assert.Equal(t, trade.Price, parsed.Price)
}
