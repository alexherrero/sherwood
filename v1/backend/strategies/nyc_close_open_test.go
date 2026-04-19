package strategies

import (
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/stretchr/testify/assert"
)

func TestNYCCloseOpen_OnData(t *testing.T) {
	strategy := NewNYCCloseOpen()
	err := strategy.Init(nil)
	assert.NoError(t, err)

	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal("Failed to load NYC timezone")
	}

	tests := []struct {
		name     string
		time     time.Time
		expected models.SignalType
	}{
		{
			name:     "Buy at Market Close (16:00 ET)",
			time:     time.Date(2023, 10, 2, 16, 0, 0, 0, location), // Monday
			expected: models.SignalBuy,
		},
		{
			name:     "Sell Before Open (08:30 ET)",
			time:     time.Date(2023, 10, 3, 8, 30, 0, 0, location), // Tuesday
			expected: models.SignalSell,
		},
		{
			name:     "No Signal at Random Time",
			time:     time.Date(2023, 10, 2, 12, 0, 0, 0, location),
			expected: models.SignalHold,
		},
		{
			name:     "No buy on weekend",
			time:     time.Date(2023, 10, 7, 16, 0, 0, 0, location), // Saturday
			expected: models.SignalHold,
		},
		{
			name:     "No sell on weekend",
			time:     time.Date(2023, 10, 8, 8, 30, 0, 0, location), // Sunday
			expected: models.SignalHold,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []models.OHLCV{
				{
					Timestamp: tt.time,
					Symbol:    "BTC-USD",
					Open:      50000,
					High:      51000,
					Low:       49000,
					Close:     50500,
					Volume:    100,
				},
			}
			signal := strategy.OnData(data)
			assert.Equal(t, tt.expected, signal.Type)
		})
	}
}
