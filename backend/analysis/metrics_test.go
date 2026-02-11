package analysis

import (
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/stretchr/testify/assert"
)

func TestCalculateMetrics(t *testing.T) {
	// Base time for logical ordering
	now := time.Now()

	tests := []struct {
		name           string
		orders         []models.Order
		initialBalance float64
		expected       PerformanceMetrics
	}{
		{
			name:           "Empty Orders",
			orders:         []models.Order{},
			initialBalance: 1000.0,
			expected:       PerformanceMetrics{},
		},
		{
			name: "Single Profitable Trade (Long)",
			orders: []models.Order{
				{
					Symbol:       "AAPL",
					Side:         models.OrderSideBuy,
					Status:       models.OrderStatusFilled,
					Quantity:     10,
					AveragePrice: 100.0,
					UpdatedAt:    now.Add(-2 * time.Hour),
				},
				{
					Symbol:       "AAPL",
					Side:         models.OrderSideSell,
					Status:       models.OrderStatusFilled,
					Quantity:     10,
					AveragePrice: 110.0,
					UpdatedAt:    now,
				},
			},
			initialBalance: 1000.0,
			expected: PerformanceMetrics{
				TotalTrades:   1,
				WinningTrades: 1,
				LosingTrades:  0,
				WinRate:       1.0,
				TotalPnL:      100.0, // (110-100)*10
				AveragePnL:    100.0,
				BestTrade:     100.0,
				WorstTrade:    0.0, // Initialized to 0 if only positive
				MaxDrawdown:   0.0,
			},
		},
		{
			name: "Mixed Trades",
			orders: []models.Order{
				// Trade 1: Win $100
				{Symbol: "AAPL", Side: models.OrderSideBuy, Status: models.OrderStatusFilled, Quantity: 10, AveragePrice: 100.0, UpdatedAt: now.Add(-4 * time.Hour)},
				{Symbol: "AAPL", Side: models.OrderSideSell, Status: models.OrderStatusFilled, Quantity: 10, AveragePrice: 110.0, UpdatedAt: now.Add(-3 * time.Hour)},
				// Trade 2: Loss $50
				{Symbol: "GOOG", Side: models.OrderSideBuy, Status: models.OrderStatusFilled, Quantity: 5, AveragePrice: 200.0, UpdatedAt: now.Add(-2 * time.Hour)},
				{Symbol: "GOOG", Side: models.OrderSideSell, Status: models.OrderStatusFilled, Quantity: 5, AveragePrice: 190.0, UpdatedAt: now.Add(-1 * time.Hour)},
			},
			initialBalance: 1000.0,
			expected: PerformanceMetrics{
				TotalTrades:   2,
				WinningTrades: 1,
				LosingTrades:  1,
				WinRate:       0.5,
				TotalPnL:      50.0, // 100 - 50
				AveragePnL:    25.0,
				BestTrade:     100.0,
				WorstTrade:    -50.0,
				ProfitFactor:  2.0, // 100 / 50
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateMetrics(tt.orders, tt.initialBalance)

			assert.Equal(t, tt.expected.TotalTrades, result.TotalTrades, "TotalTrades mismatch")
			assert.Equal(t, tt.expected.WinningTrades, result.WinningTrades, "WinningTrades mismatch")
			assert.Equal(t, tt.expected.LosingTrades, result.LosingTrades, "LosingTrades mismatch")
			assert.InDelta(t, tt.expected.WinRate, result.WinRate, 0.001, "WinRate mismatch")
			assert.InDelta(t, tt.expected.TotalPnL, result.TotalPnL, 0.001, "TotalPnL mismatch")
			assert.InDelta(t, tt.expected.AveragePnL, result.AveragePnL, 0.001, "AveragePnL mismatch")

			if tt.expected.ProfitFactor != 0 {
				assert.InDelta(t, tt.expected.ProfitFactor, result.ProfitFactor, 0.001, "ProfitFactor mismatch")
			}
		})
	}
}
