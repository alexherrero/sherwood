package strategies

import (
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMACrossoverInit verifies MA crossover strategy initialization.
func TestMACrossoverInit(t *testing.T) {
	strategy := NewMACrossover()
	err := strategy.Init(map[string]interface{}{
		"short_period": 5,
		"long_period":  10,
	})
	require.NoError(t, err)
	assert.Equal(t, "ma_crossover", strategy.Name())
}

// TestMACrossoverValidation verifies parameter validation.
func TestMACrossoverValidation(t *testing.T) {
	strategy := NewMACrossover()

	// Invalid: short >= long
	err := strategy.Init(map[string]interface{}{
		"short_period": 20,
		"long_period":  10,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "short_period")
}

// TestMACrossoverSignal verifies signal generation.
func TestMACrossoverSignal(t *testing.T) {
	strategy := NewMACrossover()
	err := strategy.Init(map[string]interface{}{
		"short_period": 3,
		"long_period":  5,
	})
	require.NoError(t, err)

	// Generate test data with upward trend (should generate buy)
	data := generateTestData(10, 100.0, 1.0)
	signal := strategy.OnData(data)

	assert.Equal(t, "AAPL", signal.Symbol)
	assert.NotEmpty(t, signal.Reason)
}

// TestMACrossoverInsufficientData verifies handling of insufficient data.
func TestMACrossoverInsufficientData(t *testing.T) {
	strategy := NewMACrossover()
	err := strategy.Init(map[string]interface{}{
		"short_period": 10,
		"long_period":  20,
	})
	require.NoError(t, err)

	// Only 5 bars - not enough for 20-period MA
	data := generateTestData(5, 100.0, 0.5)
	signal := strategy.OnData(data)

	assert.Equal(t, models.SignalHold, signal.Type)
	assert.Contains(t, signal.Reason, "Need at least")
}

// TestRegistryRegister verifies strategy registration.
func TestRegistryRegister(t *testing.T) {
	registry := NewRegistry()
	strategy := NewMACrossover()

	err := registry.Register(strategy)
	require.NoError(t, err)

	// Duplicate registration should fail
	err = registry.Register(strategy)
	assert.Error(t, err)
}

// TestRegistryGet verifies strategy retrieval.
func TestRegistryGet(t *testing.T) {
	registry := NewRegistry()
	strategy := NewMACrossover()
	registry.Register(strategy)

	found, exists := registry.Get("ma_crossover")
	assert.True(t, exists)
	assert.Equal(t, "ma_crossover", found.Name())

	_, exists = registry.Get("nonexistent")
	assert.False(t, exists)
}

func TestBaseStrategy_Helpers(t *testing.T) {
	s := NewBaseStrategy("base", "desc")
	config := map[string]interface{}{
		"int_val":     float64(10), // JSON often decodes numbers as float64
		"float_val":   20.5,
		"mixed_int":   30,
		"mixed_float": 40, // Int provided where float expected
		"string_val":  "50",
	}
	s.Init(config)

	// GetConfigInt
	assert.Equal(t, 10, s.GetConfigInt("int_val", 0))
	assert.Equal(t, 30, s.GetConfigInt("mixed_int", 0))
	assert.Equal(t, 99, s.GetConfigInt("missing", 99))
	assert.Equal(t, 99, s.GetConfigInt("string_val", 99)) // Invalid type

	// GetConfigFloat
	assert.Equal(t, 20.5, s.GetConfigFloat("float_val", 0.0))
	assert.Equal(t, 40.0, s.GetConfigFloat("mixed_float", 0.0))
	assert.Equal(t, 99.9, s.GetConfigFloat("missing", 99.9))
	assert.Equal(t, 99.9, s.GetConfigFloat("string_val", 99.9)) // Invalid type
}

// generateTestData creates test OHLCV data.
func generateTestData(n int, startPrice, trend float64) []models.OHLCV {
	data := make([]models.OHLCV, n)
	price := startPrice
	baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < n; i++ {
		data[i] = models.OHLCV{
			Timestamp: baseTime.AddDate(0, 0, i),
			Symbol:    "AAPL",
			Open:      price,
			High:      price * 1.02,
			Low:       price * 0.98,
			Close:     price + trend,
			Volume:    1000000,
		}
		price += trend
	}
	return data
}
