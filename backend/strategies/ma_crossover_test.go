package strategies

import (
	"testing"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMACrossover_NewMACrossover verifies default construction.
func TestMACrossover_NewMACrossover(t *testing.T) {
	s := NewMACrossover()
	assert.Equal(t, "ma_crossover", s.Name())
	assert.Equal(t, 10, s.shortPeriod)
	assert.Equal(t, 20, s.longPeriod)
}

// TestMACrossover_Init verifies configuration initialization.
func TestMACrossover_Init(t *testing.T) {
	tests := []struct {
		name        string
		config      map[string]interface{}
		wantShort   int
		wantLong    int
		wantErr     bool
		errContains string
	}{
		{
			name:      "default config",
			config:    map[string]interface{}{},
			wantShort: 10,
			wantLong:  20,
			wantErr:   false,
		},
		{
			name: "custom config",
			config: map[string]interface{}{
				"short_period": 5,
				"long_period":  15,
			},
			wantShort: 5,
			wantLong:  15,
			wantErr:   false,
		},
		{
			name: "invalid short >= long",
			config: map[string]interface{}{
				"short_period": 20,
				"long_period":  10,
			},
			wantErr:     true,
			errContains: "must be less than",
		},
		{
			name: "zero short period",
			config: map[string]interface{}{
				"short_period": 0,
				"long_period":  20,
			},
			wantErr:     true,
			errContains: "must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewMACrossover()
			err := s.Init(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantShort, s.shortPeriod)
			assert.Equal(t, tt.wantLong, s.longPeriod)
		})
	}
}

// TestMACrossover_GetParameters verifies parameter definitions.
func TestMACrossover_GetParameters(t *testing.T) {
	s := NewMACrossover()
	params := s.GetParameters()

	assert.Contains(t, params, "short_period")
	assert.Contains(t, params, "long_period")

	shortParam := params["short_period"]
	assert.Equal(t, "int", shortParam.Type)
	assert.Equal(t, 10, shortParam.Default)

	longParam := params["long_period"]
	assert.Equal(t, "int", longParam.Type)
	assert.Equal(t, 20, longParam.Default)
}

// TestMACrossover_OnData_InsufficientData verifies behavior with insufficient data.
func TestMACrossover_OnData_InsufficientData(t *testing.T) {
	s := NewMACrossover()
	_ = s.Init(map[string]interface{}{})

	// Only 10 data points, but need 21 (longPeriod + 1)
	data := generateOHLCVData(10, 100.0, "TEST")
	signal := s.OnData(data)

	assert.Equal(t, models.SignalHold, signal.Type)
	assert.Contains(t, signal.Reason, "Need at least")
}

// TestMACrossover_OnData_BullishCrossover verifies buy signal on bullish crossover.
func TestMACrossover_OnData_BullishCrossover(t *testing.T) {
	s := NewMACrossover()
	_ = s.Init(map[string]interface{}{
		"short_period": 2,
		"long_period":  4,
	})

	// Create data where short MA crosses above long MA
	// Need at least long_period + 1 = 5 data points
	// Bar 0-3: Low prices so long MA is low
	// Bar 4: Sharp rise so short MA crosses above
	data := []models.OHLCV{
		{Symbol: "TEST", Close: 100},
		{Symbol: "TEST", Close: 100},
		{Symbol: "TEST", Close: 100},
		{Symbol: "TEST", Close: 100},
		{Symbol: "TEST", Close: 120}, // Jump causes short MA to cross above long MA
	}

	signal := s.OnData(data)
	// Short MA (2 periods) = (100+120)/2 = 110
	// Long MA (4 periods) = (100+100+100+120)/4 = 105
	// Previous Short MA = (100+100)/2 = 100
	// Previous Long MA = (100+100+100+100)/4 = 100
	// prevShort <= prevLong (100 <= 100) AND currentShort > currentLong (110 > 105) = BULLISH
	assert.Equal(t, models.SignalBuy, signal.Type)
	assert.Contains(t, signal.Reason, "Bullish crossover")
}

// TestMACrossover_OnData_BearishCrossover verifies sell signal on bearish crossover.
func TestMACrossover_OnData_BearishCrossover(t *testing.T) {
	s := NewMACrossover()
	_ = s.Init(map[string]interface{}{
		"short_period": 2,
		"long_period":  4,
	})

	// Create data where short MA crosses below long MA
	// Bar 0-3: High prices so long MA is high
	// Bar 4: Sharp drop so short MA crosses below
	data := []models.OHLCV{
		{Symbol: "TEST", Close: 120},
		{Symbol: "TEST", Close: 120},
		{Symbol: "TEST", Close: 120},
		{Symbol: "TEST", Close: 120},
		{Symbol: "TEST", Close: 100}, // Drop causes short MA to cross below long MA
	}

	signal := s.OnData(data)
	// Short MA (2 periods) = (120+100)/2 = 110
	// Long MA (4 periods) = (120+120+120+100)/4 = 115
	// Previous Short MA = (120+120)/2 = 120
	// Previous Long MA = (120+120+120+120)/4 = 120
	// prevShort >= prevLong (120 >= 120) AND currentShort < currentLong (110 < 115) = BEARISH
	assert.Equal(t, models.SignalSell, signal.Type)
	assert.Contains(t, signal.Reason, "Bearish crossover")
}

// TestMACrossover_OnData_NoCrossover verifies hold signal when no crossover.
func TestMACrossover_OnData_NoCrossover(t *testing.T) {
	s := NewMACrossover()
	_ = s.Init(map[string]interface{}{
		"short_period": 2,
		"long_period":  4,
	})

	// Create flat data - no crossover
	data := []models.OHLCV{
		{Symbol: "TEST", Close: 100},
		{Symbol: "TEST", Close: 100},
		{Symbol: "TEST", Close: 100},
		{Symbol: "TEST", Close: 100},
		{Symbol: "TEST", Close: 100},
	}

	signal := s.OnData(data)
	assert.Equal(t, models.SignalHold, signal.Type)
	assert.Contains(t, signal.Reason, "No crossover")
}

// generateOHLCVData creates test OHLCV data with flat prices.
func generateOHLCVData(count int, basePrice float64, symbol string) []models.OHLCV {
	data := make([]models.OHLCV, count)
	for i := 0; i < count; i++ {
		data[i] = models.OHLCV{
			Symbol: symbol,
			Open:   basePrice,
			High:   basePrice + 1,
			Low:    basePrice - 1,
			Close:  basePrice,
			Volume: 1000,
		}
	}
	return data
}
