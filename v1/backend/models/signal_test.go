package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSignalConstants verifies signal constants.
func TestSignalConstants(t *testing.T) {
	assert.Equal(t, SignalType("buy"), SignalBuy)
	assert.Equal(t, SignalType("sell"), SignalSell)
	assert.Equal(t, SignalType("hold"), SignalHold)

	assert.Equal(t, SignalStrength("strong"), SignalStrengthStrong)
	assert.Equal(t, SignalStrength("weak"), SignalStrengthWeak)
}

// TestSignal_JSON verifies JSON marshaling of Signal.
func TestSignal_JSON(t *testing.T) {
	signal := Signal{
		Symbol:       "BTC-USD",
		Type:         SignalBuy,
		Strength:     SignalStrengthStrong,
		Price:        50000.0,
		Quantity:     0.1,
		StopLoss:     49000.0,
		TakeProfit:   55000.0,
		Reason:       "MA Crossover",
		StrategyName: "TestStrategy",
	}

	data, err := json.Marshal(signal)
	require.NoError(t, err)

	var parsed Signal
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, signal.Symbol, parsed.Symbol)
	assert.Equal(t, signal.Type, parsed.Type)
	assert.Equal(t, signal.Strength, parsed.Strength)
	assert.Equal(t, signal.Price, parsed.Price)
}
