package analysis

import (
	"math"
	"sort"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
)

// PerformanceMetrics holds aggregate performance statistics.
type PerformanceMetrics struct {
	TotalTrades     int     `json:"total_trades"`
	WinningTrades   int     `json:"winning_trades"`
	LosingTrades    int     `json:"losing_trades"`
	WinRate         float64 `json:"win_rate"`
	TotalPnL        float64 `json:"total_pnl"`
	AveragePnL      float64 `json:"average_pnl"`
	BestTrade       float64 `json:"best_trade"`
	WorstTrade      float64 `json:"worst_trade"`
	SharpeRatio     float64 `json:"sharpe_ratio"`
	MaxDrawdown     float64 `json:"max_drawdown"`
	ProfitFactor    float64 `json:"profit_factor"`
	AverageHoldTime string  `json:"average_hold_time"` // Human readable duration
	AvgHoldTimeSecs float64 `json:"avg_hold_time_secs"`
}

// CalculateMetrics computes performance metrics from a list of orders.
// It uses a Weighted Average Cost (WAC) approach to calculate Realized PnL.
// This simplifies inventory tracking compared to FIFO/LIFO, treating all units as fungible.
func CalculateMetrics(orders []models.Order, initialBalance float64) PerformanceMetrics {
	// Filter for filled orders and sort by execution time
	var filled []models.Order
	for _, o := range orders {
		if o.Status == models.OrderStatusFilled {
			filled = append(filled, o)
		}
	}
	sort.Slice(filled, func(i, j int) bool {
		return filled[i].UpdatedAt.Before(filled[j].UpdatedAt)
	})

	metrics := PerformanceMetrics{}

	// Track positions: Symbol -> Cost Basis (Avg Price), Quantity, OpenTime (for holding period estimate)
	type position struct {
		avgPrice float64
		quantity float64
		openTime time.Time
	}
	positions := make(map[string]position)

	var realizedPnLs []float64
	var equityCurve []float64
	currentEquity := initialBalance
	equityCurve = append(equityCurve, currentEquity)

	var totalHoldDuration time.Duration
	var closedTradeCount int

	grossProfit := 0.0
	grossLoss := 0.0

	for _, order := range filled {
		symbol := order.Symbol
		pos := positions[symbol]

		if order.Side == models.OrderSideBuy {
			// Increase Long Position or Decrease Short Position?
			// Assumption: Long-only logic for MVP simplicity, consistent with most basic strategies.
			// Or handle symmetrical?

			// Simple Weighted Average Cost Basis for Longs
			if pos.quantity >= 0 {
				totalCost := (pos.quantity * pos.avgPrice) + (order.Quantity * order.AveragePrice)
				totalQty := pos.quantity + order.Quantity
				if totalQty > 0 {
					pos.avgPrice = totalCost / totalQty
				} else {
					pos.avgPrice = 0
				}
				pos.quantity = totalQty
				if pos.quantity > 0 && pos.openTime.IsZero() {
					pos.openTime = order.UpdatedAt
				}
			} else {
				// Buying to cover Short (Not implemented in MVP fully, treating as closing)
				// For now, treat Buy as opening/adding to long.
				// If we support shorts, we'd realize PnL here.
				// Let's stick to Long-Only MVP: Buy adds to position.
			}
			positions[symbol] = pos

		} else if order.Side == models.OrderSideSell {
			// Selling: Realize PnL on the quantity sold
			// If we have inventory
			if pos.quantity > 0 {
				sellQty := math.Min(order.Quantity, pos.quantity)

				// Calculate PnL
				pnl := (order.AveragePrice - pos.avgPrice) * sellQty

				// Update Metrics
				realizedPnLs = append(realizedPnLs, pnl)
				currentEquity += pnl
				equityCurve = append(equityCurve, currentEquity)

				if pnl > 0 {
					metrics.WinningTrades++
					grossProfit += pnl
				} else {
					metrics.LosingTrades++
					grossLoss += math.Abs(pnl)
				}
				metrics.TotalPnL += pnl
				closedTradeCount++

				if pnl > metrics.BestTrade {
					metrics.BestTrade = pnl
				}
				if pnl < metrics.WorstTrade {
					metrics.WorstTrade = pnl
				}

				// Holding time approximation: Time since position opened (any part)
				// This is a simplification. FIFO tracks exact lot time.
				if !pos.openTime.IsZero() {
					totalHoldDuration += order.UpdatedAt.Sub(pos.openTime)
				}

				// Reduce position
				pos.quantity -= sellQty
				if pos.quantity <= 0.00000001 {
					pos.quantity = 0
					pos.avgPrice = 0
					pos.openTime = time.Time{} // Reset
				}
				positions[symbol] = pos
			} else {
				// Selling short? MVP ignores short selling for now or treats as error?
				// Just ignore or track separately?
				// Ignoring for MVP simplicity to avoid negative inventory complexity.
			}
		}
	}

	metrics.TotalTrades = closedTradeCount

	if closedTradeCount > 0 {
		metrics.WinRate = float64(metrics.WinningTrades) / float64(closedTradeCount)
		metrics.AveragePnL = metrics.TotalPnL / float64(closedTradeCount)
		metrics.AvgHoldTimeSecs = totalHoldDuration.Seconds() / float64(closedTradeCount)
		metrics.AverageHoldTime = (time.Duration(metrics.AvgHoldTimeSecs) * time.Second).String()
	}

	if grossLoss > 0 {
		metrics.ProfitFactor = grossProfit / grossLoss
	} else if grossProfit > 0 {
		metrics.ProfitFactor = 0.0 // Handle infinite/undefined
	}

	metrics.MaxDrawdown = calculateMaxDrawdown(equityCurve)
	metrics.SharpeRatio = calculateSharpeRatio(realizedPnLs)

	return metrics
}

func calculateMaxDrawdown(equityCurve []float64) float64 {
	maxPeak := -math.MaxFloat64
	maxDrawdown := 0.0

	for _, equity := range equityCurve {
		if equity > maxPeak {
			maxPeak = equity
		}
		drawdown := (maxPeak - equity) / maxPeak
		if drawdown > maxDrawdown {
			maxDrawdown = drawdown
		}
	}
	return maxDrawdown
}

func calculateSharpeRatio(returns []float64) float64 {
	if len(returns) < 2 {
		return 0.0
	}

	sum := 0.0
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))

	variance := 0.0
	for _, r := range returns {
		variance += math.Pow(r-mean, 2)
	}
	stdDev := math.Sqrt(variance / float64(len(returns)-1))

	if stdDev == 0 {
		return 0.0
	}

	return mean / stdDev
}
