// Package backtesting provides performance metrics calculation.
package backtesting

import (
	"math"
)

// Metrics holds calculated performance metrics for a backtest.
type Metrics struct {
	// TotalReturn is the total percentage return.
	TotalReturn float64 `json:"total_return"`
	// TotalReturnAbs is the absolute return in currency.
	TotalReturnAbs float64 `json:"total_return_abs"`
	// AnnualizedReturn is the annualized return percentage.
	AnnualizedReturn float64 `json:"annualized_return"`
	// SharpeRatio is the risk-adjusted return (assuming 0% risk-free rate).
	SharpeRatio float64 `json:"sharpe_ratio"`
	// MaxDrawdown is the maximum peak-to-trough decline.
	MaxDrawdown float64 `json:"max_drawdown"`
	// MaxDrawdownAbs is the maximum drawdown in currency.
	MaxDrawdownAbs float64 `json:"max_drawdown_abs"`
	// TotalTrades is the number of completed trades.
	TotalTrades int `json:"total_trades"`
	// WinningTrades is the number of profitable trades.
	WinningTrades int `json:"winning_trades"`
	// LosingTrades is the number of losing trades.
	LosingTrades int `json:"losing_trades"`
	// WinRate is the percentage of winning trades.
	WinRate float64 `json:"win_rate"`
	// AverageWin is the average profit on winning trades.
	AverageWin float64 `json:"average_win"`
	// AverageLoss is the average loss on losing trades.
	AverageLoss float64 `json:"average_loss"`
	// ProfitFactor is the ratio of gross profits to gross losses.
	ProfitFactor float64 `json:"profit_factor"`
	// Volatility is the standard deviation of returns.
	Volatility float64 `json:"volatility"`
	// FinalEquity is the ending equity.
	FinalEquity float64 `json:"final_equity"`
}

// CalculateMetrics computes performance metrics from backtest results.
//
// Args:
//   - trades: List of simulated trades
//   - equityCurve: Equity over time
//   - initialCapital: Starting capital
//
// Returns:
//   - *Metrics: Calculated performance metrics
func CalculateMetrics(trades []SimulatedTrade, equityCurve []EquityPoint, initialCapital float64) *Metrics {
	m := &Metrics{
		TotalTrades: len(trades),
	}

	if len(equityCurve) == 0 {
		return m
	}

	// Final equity
	m.FinalEquity = equityCurve[len(equityCurve)-1].Equity

	// Total return
	m.TotalReturnAbs = m.FinalEquity - initialCapital
	if initialCapital > 0 {
		m.TotalReturn = (m.TotalReturnAbs / initialCapital) * 100
	}

	// Calculate max drawdown
	peak := initialCapital
	maxDD := 0.0
	maxDDAbs := 0.0
	for _, ep := range equityCurve {
		if ep.Equity > peak {
			peak = ep.Equity
		}
		dd := (peak - ep.Equity) / peak * 100
		ddAbs := peak - ep.Equity
		if dd > maxDD {
			maxDD = dd
			maxDDAbs = ddAbs
		}
	}
	m.MaxDrawdown = maxDD
	m.MaxDrawdownAbs = maxDDAbs

	// Trade statistics
	var wins, losses float64
	grossProfit := 0.0
	grossLoss := 0.0

	for _, trade := range trades {
		if trade.PnL > 0 {
			m.WinningTrades++
			wins += trade.PnL
			grossProfit += trade.PnL
		} else if trade.PnL < 0 {
			m.LosingTrades++
			losses += math.Abs(trade.PnL)
			grossLoss += math.Abs(trade.PnL)
		}
	}

	if m.TotalTrades > 0 {
		m.WinRate = float64(m.WinningTrades) / float64(m.TotalTrades) * 100
	}
	if m.WinningTrades > 0 {
		m.AverageWin = wins / float64(m.WinningTrades)
	}
	if m.LosingTrades > 0 {
		m.AverageLoss = losses / float64(m.LosingTrades)
	}
	if grossLoss > 0 {
		m.ProfitFactor = grossProfit / grossLoss
	}

	// Calculate daily returns for Sharpe ratio
	if len(equityCurve) > 1 {
		returns := make([]float64, len(equityCurve)-1)
		for i := 1; i < len(equityCurve); i++ {
			if equityCurve[i-1].Equity > 0 {
				returns[i-1] = (equityCurve[i].Equity - equityCurve[i-1].Equity) / equityCurve[i-1].Equity
			}
		}

		// Mean and std dev
		mean := 0.0
		for _, r := range returns {
			mean += r
		}
		mean /= float64(len(returns))

		variance := 0.0
		for _, r := range returns {
			variance += (r - mean) * (r - mean)
		}
		variance /= float64(len(returns))
		stdDev := math.Sqrt(variance)

		m.Volatility = stdDev * 100

		// Sharpe ratio (annualized, assuming 252 trading days)
		if stdDev > 0 {
			m.SharpeRatio = (mean / stdDev) * math.Sqrt(252)
		}

		// Annualized return (assuming 252 trading days)
		tradingDays := len(equityCurve)
		if tradingDays > 0 {
			years := float64(tradingDays) / 252.0
			if years > 0 && m.FinalEquity > 0 && initialCapital > 0 {
				m.AnnualizedReturn = (math.Pow(m.FinalEquity/initialCapital, 1/years) - 1) * 100
			}
		}
	}

	return m
}
