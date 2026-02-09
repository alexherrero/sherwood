package backtesting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestCalculateMetrics_EmptyEquityCurve verifies handling of empty equity curve.
func TestCalculateMetrics_EmptyEquityCurve(t *testing.T) {
	trades := []SimulatedTrade{}
	equityCurve := []EquityPoint{}

	m := CalculateMetrics(trades, equityCurve, 10000)

	assert.NotNil(t, m)
	assert.Equal(t, 0, m.TotalTrades)
}

// TestCalculateMetrics_TotalReturn verifies total return calculation.
func TestCalculateMetrics_TotalReturn(t *testing.T) {
	equityCurve := []EquityPoint{
		{Timestamp: time.Now(), Equity: 10000},
		{Timestamp: time.Now(), Equity: 11000}, // +10%
	}

	m := CalculateMetrics([]SimulatedTrade{}, equityCurve, 10000)

	assert.Equal(t, 11000.0, m.FinalEquity)
	assert.Equal(t, 1000.0, m.TotalReturnAbs)
	assert.InDelta(t, 10.0, m.TotalReturn, 0.01)
}

// TestCalculateMetrics_MaxDrawdown verifies max drawdown calculation.
func TestCalculateMetrics_MaxDrawdown(t *testing.T) {
	equityCurve := []EquityPoint{
		{Equity: 10000},
		{Equity: 12000}, // New peak
		{Equity: 9000},  // -25% drawdown from peak
		{Equity: 11000}, // Recovery
	}

	m := CalculateMetrics([]SimulatedTrade{}, equityCurve, 10000)

	// Max drawdown = (12000 - 9000) / 12000 = 25%
	assert.InDelta(t, 25.0, m.MaxDrawdown, 0.01)
	assert.Equal(t, 3000.0, m.MaxDrawdownAbs)
}

// TestCalculateMetrics_TradeStatistics verifies trade stats calculation.
func TestCalculateMetrics_TradeStatistics(t *testing.T) {
	trades := []SimulatedTrade{
		{PnL: 100},  // Win
		{PnL: 200},  // Win
		{PnL: -50},  // Loss
		{PnL: 150},  // Win
		{PnL: -100}, // Loss
	}
	equityCurve := []EquityPoint{{Equity: 10300}}

	m := CalculateMetrics(trades, equityCurve, 10000)

	assert.Equal(t, 5, m.TotalTrades)
	assert.Equal(t, 3, m.WinningTrades)
	assert.Equal(t, 2, m.LosingTrades)
	assert.InDelta(t, 60.0, m.WinRate, 0.01)     // 3/5 = 60%
	assert.InDelta(t, 150.0, m.AverageWin, 0.01) // (100+200+150)/3
	assert.InDelta(t, 75.0, m.AverageLoss, 0.01) // (50+100)/2
}

// TestCalculateMetrics_ProfitFactor verifies profit factor calculation.
func TestCalculateMetrics_ProfitFactor(t *testing.T) {
	trades := []SimulatedTrade{
		{PnL: 300},  // Gross profit: 300
		{PnL: -100}, // Gross loss: 100
	}
	equityCurve := []EquityPoint{{Equity: 10200}}

	m := CalculateMetrics(trades, equityCurve, 10000)

	// Profit factor = 300 / 100 = 3.0
	assert.InDelta(t, 3.0, m.ProfitFactor, 0.01)
}

// TestCalculateMetrics_Volatility verifies volatility calculation.
func TestCalculateMetrics_Volatility(t *testing.T) {
	equityCurve := []EquityPoint{
		{Equity: 10000},
		{Equity: 10100}, // +1%
		{Equity: 10000}, // -0.99%
		{Equity: 10200}, // +2%
	}

	m := CalculateMetrics([]SimulatedTrade{}, equityCurve, 10000)

	// Should have some volatility since returns vary
	assert.True(t, m.Volatility > 0)
}

// TestCalculateMetrics_SharpeRatio verifies Sharpe ratio calculation.
func TestCalculateMetrics_SharpeRatio(t *testing.T) {
	// Create equity curve with consistent positive returns
	equityCurve := make([]EquityPoint, 100)
	baseTime := time.Now()
	for i := 0; i < 100; i++ {
		equityCurve[i] = EquityPoint{
			Timestamp: baseTime.AddDate(0, 0, i),
			Equity:    10000 + float64(i)*10, // Steady growth
		}
	}

	m := CalculateMetrics([]SimulatedTrade{}, equityCurve, 10000)

	// Should have positive Sharpe ratio for consistent gains
	assert.True(t, m.SharpeRatio > 0)
}

// TestCalculateMetrics_AllWinners verifies metrics with all winning trades.
func TestCalculateMetrics_AllWinners(t *testing.T) {
	trades := []SimulatedTrade{
		{PnL: 100},
		{PnL: 200},
		{PnL: 150},
	}
	equityCurve := []EquityPoint{{Equity: 10450}}

	m := CalculateMetrics(trades, equityCurve, 10000)

	assert.Equal(t, 3, m.WinningTrades)
	assert.Equal(t, 0, m.LosingTrades)
	assert.InDelta(t, 100.0, m.WinRate, 0.01)
	assert.Equal(t, 0.0, m.AverageLoss)
	assert.Equal(t, 0.0, m.ProfitFactor) // No losses, division by zero handled
}

// TestCalculateMetrics_ZeroInitialCapital verifies handling of zero capital.
func TestCalculateMetrics_ZeroInitialCapital(t *testing.T) {
	equityCurve := []EquityPoint{{Equity: 100}}

	m := CalculateMetrics([]SimulatedTrade{}, equityCurve, 0)

	// Should not panic, return safe values
	assert.Equal(t, 0.0, m.TotalReturn)
}

// TestMetricsStruct_Fields verifies Metrics struct has expected fields.
func TestMetricsStruct_Fields(t *testing.T) {
	m := Metrics{
		TotalReturn:  10.5,
		SharpeRatio:  1.5,
		MaxDrawdown:  5.0,
		TotalTrades:  20,
		WinRate:      60.0,
		ProfitFactor: 2.0,
		FinalEquity:  11000,
	}

	assert.Equal(t, 10.5, m.TotalReturn)
	assert.Equal(t, 1.5, m.SharpeRatio)
	assert.Equal(t, 5.0, m.MaxDrawdown)
	assert.Equal(t, 20, m.TotalTrades)
}
