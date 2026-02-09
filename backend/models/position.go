package models

import (
	"time"
)

// Position represents a current holding in a symbol.
type Position struct {
	// Symbol is the ticker symbol.
	Symbol string `json:"symbol" db:"symbol"`
	// Quantity is the number of units held.
	Quantity float64 `json:"quantity" db:"quantity"`
	// AverageCost is the average cost basis per unit.
	AverageCost float64 `json:"average_cost" db:"average_cost"`
	// CurrentPrice is the current market price.
	CurrentPrice float64 `json:"current_price" db:"current_price"`
	// MarketValue is the current market value (Quantity * CurrentPrice).
	MarketValue float64 `json:"market_value" db:"market_value"`
	// UnrealizedPL is the unrealized profit/loss.
	UnrealizedPL float64 `json:"unrealized_pl" db:"unrealized_pl"`
	// UpdatedAt is when the position was last updated.
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// Balance represents account balance information.
type Balance struct {
	// Cash is the available cash balance.
	Cash float64 `json:"cash" db:"cash"`
	// Equity is the total account equity.
	Equity float64 `json:"equity" db:"equity"`
	// BuyingPower is the available buying power.
	BuyingPower float64 `json:"buying_power" db:"buying_power"`
	// PortfolioValue is the total portfolio value.
	PortfolioValue float64 `json:"portfolio_value" db:"portfolio_value"`
	// UpdatedAt is when the balance was last updated.
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}
