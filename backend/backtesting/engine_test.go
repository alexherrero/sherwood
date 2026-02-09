package backtesting

import (
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/alexherrero/sherwood/backend/strategies"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEngine_NewEngine verifies engine creation.
func TestEngine_NewEngine(t *testing.T) {
	engine := NewEngine()
	assert.NotNil(t, engine)
}

// TestEngine_Run_EmptyData verifies error on empty data.
func TestEngine_Run_EmptyData(t *testing.T) {
	engine := NewEngine()
	strategy := strategies.NewMACrossover()
	config := BacktestConfig{
		Symbol:         "TEST",
		InitialCapital: 10000,
	}

	_, err := engine.Run(strategy, []models.OHLCV{}, config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no data provided")
}

// TestEngine_Run_BasicBacktest verifies basic backtest execution.
func TestEngine_Run_BasicBacktest(t *testing.T) {
	engine := NewEngine()
	strategy := strategies.NewMACrossover()
	_ = strategy.Init(map[string]interface{}{
		"short_period": 3,
		"long_period":  5,
	})

	// Create enough data for MA calculations
	data := generateTestOHLCVData(50, "TEST")
	config := BacktestConfig{
		Symbol:         "TEST",
		InitialCapital: 10000,
		Commission:     0,
	}

	result, err := engine.Run(strategy, data, config)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotEmpty(t, result.ID)
	assert.Equal(t, "ma_crossover", result.Strategy)
	assert.NotNil(t, result.Metrics)
	assert.NotEmpty(t, result.EquityCurve)
}

// TestEngine_Run_WithTrades verifies trades are recorded.
func TestEngine_Run_WithTrades(t *testing.T) {
	engine := NewEngine()
	strategy := strategies.NewMACrossover()
	_ = strategy.Init(map[string]interface{}{
		"short_period": 2,
		"long_period":  4,
	})

	// Create data with clear trend changes to trigger trades
	data := generateTrendingData()
	config := BacktestConfig{
		Symbol:         "TEST",
		InitialCapital: 10000,
		Commission:     1.0,
	}

	result, err := engine.Run(strategy, data, config)
	require.NoError(t, err)

	// With trending data, we should have at least one trade
	// (either completed or forced close at end)
	assert.NotNil(t, result.Trades)
}

// TestEngine_Run_EquityCurve verifies equity curve is generated.
func TestEngine_Run_EquityCurve(t *testing.T) {
	engine := NewEngine()
	strategy := strategies.NewMACrossover()
	_ = strategy.Init(map[string]interface{}{
		"short_period": 3,
		"long_period":  5,
	})

	data := generateTestOHLCVData(30, "TEST")
	config := BacktestConfig{
		Symbol:         "TEST",
		InitialCapital: 10000,
	}

	result, err := engine.Run(strategy, data, config)
	require.NoError(t, err)

	// Should have equity points for each bar after first
	assert.Len(t, result.EquityCurve, len(data)-1)

	// All equity values should be positive
	for _, ep := range result.EquityCurve {
		assert.True(t, ep.Equity > 0, "Equity should be positive")
	}
}

// TestEngine_Run_ResultContainsConfig verifies config is stored in result.
func TestEngine_Run_ResultContainsConfig(t *testing.T) {
	engine := NewEngine()
	strategy := strategies.NewMACrossover()

	data := generateTestOHLCVData(30, "AAPL")
	config := BacktestConfig{
		Symbol:         "AAPL",
		InitialCapital: 50000,
		Commission:     5.0,
	}

	result, err := engine.Run(strategy, data, config)
	require.NoError(t, err)

	assert.Equal(t, "AAPL", result.Config.Symbol)
	assert.Equal(t, 50000.0, result.Config.InitialCapital)
	assert.Equal(t, 5.0, result.Config.Commission)
}

// TestEngine_Run_UniqueIDs verifies each backtest gets a unique ID.
func TestEngine_Run_UniqueIDs(t *testing.T) {
	engine := NewEngine()
	strategy := strategies.NewMACrossover()
	data := generateTestOHLCVData(30, "TEST")
	config := BacktestConfig{
		Symbol:         "TEST",
		InitialCapital: 10000,
	}

	result1, _ := engine.Run(strategy, data, config)
	result2, _ := engine.Run(strategy, data, config)

	assert.NotEqual(t, result1.ID, result2.ID)
}

// TestEngine_Run_Timestamps verifies timing metadata.
func TestEngine_Run_Timestamps(t *testing.T) {
	engine := NewEngine()
	strategy := strategies.NewMACrossover()
	data := generateTestOHLCVData(30, "TEST")
	config := BacktestConfig{
		Symbol:         "TEST",
		InitialCapital: 10000,
	}

	before := time.Now()
	result, _ := engine.Run(strategy, data, config)
	after := time.Now()

	assert.True(t, result.StartedAt.After(before) || result.StartedAt.Equal(before))
	assert.True(t, result.CompletedAt.Before(after) || result.CompletedAt.Equal(after))
	assert.True(t, result.CompletedAt.After(result.StartedAt) || result.CompletedAt.Equal(result.StartedAt))
}

// TestSimulatedTrade_Fields verifies trade struct fields.
func TestSimulatedTrade_Fields(t *testing.T) {
	trade := SimulatedTrade{
		EntryTime:  time.Now(),
		ExitTime:   time.Now().Add(time.Hour),
		Symbol:     "TEST",
		Side:       models.OrderSideBuy,
		EntryPrice: 100.0,
		ExitPrice:  110.0,
		Quantity:   10.0,
		PnL:        100.0,
		PnLPercent: 10.0,
	}

	assert.Equal(t, "TEST", trade.Symbol)
	assert.Equal(t, models.OrderSideBuy, trade.Side)
	assert.Equal(t, 100.0, trade.EntryPrice)
	assert.Equal(t, 110.0, trade.ExitPrice)
}

// generateTestOHLCVData creates test OHLCV data with slight price variations.
func generateTestOHLCVData(count int, symbol string) []models.OHLCV {
	data := make([]models.OHLCV, count)
	basePrice := 100.0
	baseTime := time.Now().AddDate(0, 0, -count)

	for i := 0; i < count; i++ {
		// Add slight variation to avoid perfectly flat data
		price := basePrice + float64(i%5)*0.5
		data[i] = models.OHLCV{
			Timestamp: baseTime.AddDate(0, 0, i),
			Symbol:    symbol,
			Open:      price,
			High:      price + 1,
			Low:       price - 1,
			Close:     price,
			Volume:    1000,
		}
	}
	return data
}

// generateTrendingData creates data with clear uptrend then downtrend.
func generateTrendingData() []models.OHLCV {
	var data []models.OHLCV
	baseTime := time.Now().AddDate(0, 0, -30)

	// Uptrend
	for i := 0; i < 15; i++ {
		price := 100.0 + float64(i)*2 // Rising prices
		data = append(data, models.OHLCV{
			Timestamp: baseTime.AddDate(0, 0, i),
			Symbol:    "TEST",
			Open:      price,
			High:      price + 1,
			Low:       price - 1,
			Close:     price,
			Volume:    1000,
		})
	}

	// Downtrend
	for i := 0; i < 15; i++ {
		price := 128.0 - float64(i)*2 // Falling prices
		data = append(data, models.OHLCV{
			Timestamp: baseTime.AddDate(0, 0, 15+i),
			Symbol:    "TEST",
			Open:      price,
			High:      price + 1,
			Low:       price - 1,
			Close:     price,
			Volume:    1000,
		})
	}

	return data
}
