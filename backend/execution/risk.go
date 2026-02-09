// Package execution provides risk management functionality.
package execution

import (
	"fmt"

	"github.com/alexherrero/sherwood/backend/models"
)

// RiskConfig holds risk management configuration.
type RiskConfig struct {
	// MaxPositionSize is the maximum value per position.
	MaxPositionSize float64
	// MaxPortfolioRisk is the maximum portfolio risk percentage.
	MaxPortfolioRisk float64
	// MaxDailyLoss is the maximum daily loss allowed.
	MaxDailyLoss float64
	// RiskPerTrade is the maximum risk per trade (default 2%).
	RiskPerTrade float64
	// MaxOpenOrders is the maximum number of open orders.
	MaxOpenOrders int
}

// DefaultRiskConfig returns default risk configuration.
//
// Returns:
//   - *RiskConfig: Default configuration
func DefaultRiskConfig() *RiskConfig {
	return &RiskConfig{
		MaxPositionSize:  10000.0, // $10,000 max per position
		MaxPortfolioRisk: 0.20,    // 20% max portfolio risk
		MaxDailyLoss:     500.0,   // $500 max daily loss
		RiskPerTrade:     0.02,    // 2% risk per trade
		MaxOpenOrders:    10,      // 10 open orders max
	}
}

// RiskManager enforces trading risk limits.
type RiskManager struct {
	config     *RiskConfig
	broker     Broker
	dailyPnL   float64
	openOrders int
}

// NewRiskManager creates a new risk manager.
//
// Args:
//   - config: Risk configuration
//   - broker: Broker for position/balance queries
//
// Returns:
//   - *RiskManager: The risk manager instance
func NewRiskManager(config *RiskConfig, broker Broker) *RiskManager {
	if config == nil {
		config = DefaultRiskConfig()
	}
	return &RiskManager{
		config:     config,
		broker:     broker,
		dailyPnL:   0,
		openOrders: 0,
	}
}

// CheckOrder evaluates if an order passes risk checks.
//
// Args:
//   - order: The order to evaluate
//
// Returns:
//   - error: Risk violation error, or nil if passed
func (rm *RiskManager) CheckOrder(order models.Order) error {
	// Check daily loss limit
	if rm.dailyPnL < -rm.config.MaxDailyLoss {
		return fmt.Errorf("daily loss limit exceeded: %.2f", rm.dailyPnL)
	}

	// Check max open orders
	if rm.openOrders >= rm.config.MaxOpenOrders {
		return fmt.Errorf("max open orders reached: %d", rm.config.MaxOpenOrders)
	}

	// Check position size
	positionValue := order.Quantity * order.Price
	if order.Type == models.OrderTypeMarket {
		// For market orders, use last known price estimate
		positionValue = order.Quantity * 100 // Conservative estimate
	}

	if positionValue > rm.config.MaxPositionSize {
		return fmt.Errorf("position size exceeds limit: %.2f > %.2f",
			positionValue, rm.config.MaxPositionSize)
	}

	// Check portfolio risk
	balance, err := rm.broker.GetBalance()
	if err == nil && balance != nil {
		riskAmount := positionValue * rm.config.RiskPerTrade
		if riskAmount > balance.Equity*rm.config.MaxPortfolioRisk {
			return fmt.Errorf("order exceeds portfolio risk limit")
		}
	}

	return nil
}

// CalculatePositionSize calculates optimal position size based on risk.
//
// Args:
//   - entryPrice: Planned entry price
//   - stopLoss: Stop loss price
//   - balance: Account balance
//
// Returns:
//   - float64: Recommended position size
func (rm *RiskManager) CalculatePositionSize(entryPrice, stopLoss float64, balance *models.Balance) float64 {
	if entryPrice <= 0 || stopLoss <= 0 || balance == nil {
		return 0
	}

	// Calculate risk per unit
	riskPerUnit := entryPrice - stopLoss
	if riskPerUnit < 0 {
		riskPerUnit = -riskPerUnit
	}
	if riskPerUnit == 0 {
		return 0
	}

	// Calculate max risk amount (2% of equity by default)
	maxRisk := balance.Equity * rm.config.RiskPerTrade

	// Calculate position size
	positionSize := maxRisk / riskPerUnit

	// Cap at max position size
	maxUnits := rm.config.MaxPositionSize / entryPrice
	if positionSize > maxUnits {
		positionSize = maxUnits
	}

	return positionSize
}

// UpdateDailyPnL updates the daily P&L tracking.
//
// Args:
//   - pnl: P&L change to add
func (rm *RiskManager) UpdateDailyPnL(pnl float64) {
	rm.dailyPnL += pnl
}

// ResetDaily resets the daily tracking (call at market open).
func (rm *RiskManager) ResetDaily() {
	rm.dailyPnL = 0
	rm.openOrders = 0
}

// IncrementOpenOrders increments the open order count.
func (rm *RiskManager) IncrementOpenOrders() {
	rm.openOrders++
}

// DecrementOpenOrders decrements the open order count.
func (rm *RiskManager) DecrementOpenOrders() {
	if rm.openOrders > 0 {
		rm.openOrders--
	}
}

// GetDailyPnL returns the current daily P&L.
func (rm *RiskManager) GetDailyPnL() float64 {
	return rm.dailyPnL
}

// GetConfig returns the risk configuration.
func (rm *RiskManager) GetConfig() *RiskConfig {
	return rm.config
}
