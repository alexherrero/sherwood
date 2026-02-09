package models

import (
	"time"
)

// OrderSide represents the direction of an order.
type OrderSide string

const (
	// OrderSideBuy represents a buy order.
	OrderSideBuy OrderSide = "buy"
	// OrderSideSell represents a sell order.
	OrderSideSell OrderSide = "sell"
)

// OrderType represents the type of order.
type OrderType string

const (
	// OrderTypeMarket is a market order executed at current price.
	OrderTypeMarket OrderType = "market"
	// OrderTypeLimit is a limit order executed at a specified price or better.
	OrderTypeLimit OrderType = "limit"
	// OrderTypeStop is a stop order triggered at a specified price.
	OrderTypeStop OrderType = "stop"
	// OrderTypeStopLimit is a stop-limit order.
	OrderTypeStopLimit OrderType = "stop_limit"
)

// OrderStatus represents the current state of an order.
type OrderStatus string

const (
	// OrderStatusPending indicates the order is pending submission.
	OrderStatusPending OrderStatus = "pending"
	// OrderStatusSubmitted indicates the order has been submitted.
	OrderStatusSubmitted OrderStatus = "submitted"
	// OrderStatusFilled indicates the order has been fully filled.
	OrderStatusFilled OrderStatus = "filled"
	// OrderStatusPartiallyFilled indicates the order is partially filled.
	OrderStatusPartiallyFilled OrderStatus = "partially_filled"
	// OrderStatusCancelled indicates the order has been cancelled.
	OrderStatusCancelled OrderStatus = "cancelled"
	// OrderStatusRejected indicates the order was rejected.
	OrderStatusRejected OrderStatus = "rejected"
)

// Order represents a trading order.
type Order struct {
	// ID is the unique identifier for the order.
	ID string `json:"id" db:"id"`
	// Symbol is the ticker symbol.
	Symbol string `json:"symbol" db:"symbol"`
	// Side is the order direction (buy/sell).
	Side OrderSide `json:"side" db:"side"`
	// Type is the order type.
	Type OrderType `json:"type" db:"type"`
	// Quantity is the number of units to trade.
	Quantity float64 `json:"quantity" db:"quantity"`
	// Price is the limit/stop price (0 for market orders).
	Price float64 `json:"price" db:"price"`
	// Status is the current order status.
	Status OrderStatus `json:"status" db:"status"`
	// FilledQuantity is the quantity that has been filled.
	FilledQuantity float64 `json:"filled_quantity" db:"filled_quantity"`
	// AveragePrice is the average fill price.
	AveragePrice float64 `json:"average_price" db:"average_price"`
	// CreatedAt is when the order was created.
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	// UpdatedAt is when the order was last updated.
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// Trade represents a completed trade (filled order).
type Trade struct {
	// ID is the unique identifier for the trade.
	ID string `json:"id" db:"id"`
	// OrderID is the associated order ID.
	OrderID string `json:"order_id" db:"order_id"`
	// Symbol is the ticker symbol.
	Symbol string `json:"symbol" db:"symbol"`
	// Side is the trade direction.
	Side OrderSide `json:"side" db:"side"`
	// Quantity is the traded quantity.
	Quantity float64 `json:"quantity" db:"quantity"`
	// Price is the execution price.
	Price float64 `json:"price" db:"price"`
	// ExecutedAt is when the trade was executed.
	ExecutedAt time.Time `json:"executed_at" db:"executed_at"`
}
