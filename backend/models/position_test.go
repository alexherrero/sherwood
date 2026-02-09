package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPosition_Fields verifies fields can be set and accessed.
func TestPosition_Fields(t *testing.T) {
	pos := Position{
		Symbol:       "AAPL",
		Quantity:     100,
		AverageCost:  150.0,
		CurrentPrice: 160.0,
		MarketValue:  16000.0,
		UnrealizedPL: 1000.0,
	}

	assert.Equal(t, "AAPL", pos.Symbol)
	assert.Equal(t, 100.0, pos.Quantity)
	assert.Equal(t, 150.0, pos.AverageCost)
	assert.Equal(t, 1000.0, pos.UnrealizedPL)
}

// TestBalance_Fields verifies fields can be set and accessed.
func TestBalance_Fields(t *testing.T) {
	bal := Balance{
		Cash:           5000.0,
		Equity:         10000.0,
		BuyingPower:    20000.0,
		PortfolioValue: 5000.0,
	}

	assert.Equal(t, 5000.0, bal.Cash)
	assert.Equal(t, 10000.0, bal.Equity)
	assert.Equal(t, 20000.0, bal.BuyingPower)
}
