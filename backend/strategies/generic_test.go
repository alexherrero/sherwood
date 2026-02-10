package strategies

import (
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllStrategies_GenericContract ensures all registered strategies
// adhere to basic contract behavior.
func TestAllStrategies_GenericContract(t *testing.T) {
	strategyNames := AvailableStrategies()
	require.NotEmpty(t, strategyNames)

	for _, name := range strategyNames {
		t.Run(name, func(t *testing.T) {
			s, err := NewStrategyByName(name)
			require.NoError(t, err)
			require.NotNil(t, s)

			// 1. Verify Metadata
			assert.NotEmpty(t, s.Name(), "Name should not be empty")
			assert.Equal(t, name, s.Name(), "Name should match registry")
			assert.NotEmpty(t, s.Description(), "Description should not be empty")

			// 2. Verify Parameters
			params := s.GetParameters()
			assert.NotEmpty(t, params, "Should expose parameters")
			for key, param := range params {
				assert.NotEmpty(t, param.Type, "Parameter %s needs type", key)
				assert.NotNil(t, param.Default, "Parameter %s needs default", key)
			}

			// 3. Verify Init with empty config (should use defaults or return error if strict)
			// Most strategies use defaults
			err = s.Init(map[string]interface{}{})
			// We don't assert error/no error here because strict strategies might require config
			// But it shouldn't panic.

			// 4. Verify Validate
			// After empty init, validate might fail or pass depending on requirements
			_ = s.Validate()

			// 5. Verify OnData with empty
			signal := s.OnData([]models.OHLCV{})
			assert.Equal(t, models.SignalHold, signal.Type, "Should hold on empty data")

			// 6. Verify OnData with insufficient data
			// Generate 1 candle
			oneCandle := []models.OHLCV{
				{
					Timestamp: time.Now(),
					Symbol:    "TEST",
					Close:     100,
				},
			}
			signal = s.OnData(oneCandle)
			assert.Equal(t, models.SignalHold, signal.Type, "Should hold on insufficient data")
		})
	}
}
