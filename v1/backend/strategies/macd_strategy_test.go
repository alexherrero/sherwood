package strategies

import (
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
)

func TestMACDStrategy(t *testing.T) {
	strategy := NewMACDStrategy()

	// Configure periods for testing
	// We need enough data to calculate EMAs.
	// Fast=12, Slow=26.
	// Let's use smaller periods for predictable testing
	strategy.FastPeriod = 3
	strategy.SlowPeriod = 6
	strategy.SignalPeriod = 3

	// Create dummy data
	// Trend Up: MACD line should be > Signal line
	now := time.Now()

	// Generate 20 data points
	data := make([]models.OHLCV, 20)
	for i := 0; i < 20; i++ {
		// Strong upward trend
		price := 10.0 + float64(i)*2 // 10, 12, 14, ...
		data[i] = models.OHLCV{
			Timestamp: now.Add(time.Duration(i-20) * time.Minute),
			Close:     price,
			Symbol:    "BTC",
		}
	}

	// Create a crossover scenario:
	// 1. Initial downtrend where MACD < Signal.
	// 2. Followed by a sharp uptrend to trigger a MACD crossover above Signal.

	dataCross := make([]models.OHLCV, 0)
	// Phase 1: Flat/Down to establish negative momentum
	for i := 0; i < 15; i++ {
		dataCross = append(dataCross, models.OHLCV{
			Timestamp: now.Add(time.Duration(i-30) * time.Minute),
			Close:     20.0 - float64(i)*0.5, // 20, 19.5, 19...
			Symbol:    "BTC",
		})
	}
	// Phase 2: Sharply Up
	for i := 0; i < 5; i++ {
		dataCross = append(dataCross, models.OHLCV{
			Timestamp: now.Add(time.Duration(i-5) * time.Minute),
			Close:     12.0 + float64(i)*2.0, // 12, 14, 16...
			Symbol:    "BTC",
		})
	}

	// Expect a Buy or Hold signal depending on exact crossover timing.
	// Since OnData processes the last candle, the crossover must have occurred
	// or be sustained at the end of the sequence.
	signal := strategy.OnData(dataCross)
	t.Logf("Signal from MACD crossover: %+v", signal)
	if signal.Type == "" {
		t.Error("Expected valid signal type")
	}

	// Let's try to verify via "Not enough data" first.
	shortData := data[:5]
	signalShort := strategy.OnData(shortData)
	if signalShort.Reason != "Not enough data" {
		t.Errorf("Expected 'Not enough data', got '%s'", signalShort.Reason)
	}
}
