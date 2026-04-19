package strategies

import (
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
)

func TestRSIStrategy(t *testing.T) {
	strategy := NewRSIStrategy()

	// Configure short period for testing
	strategy.Period = 2
	strategy.OverboughtThreshold = 70
	strategy.OversoldThreshold = 30

	// Create dummy data
	// 10, 11, 12: Up trend, RSI should be high
	// 12, 11, 10: Down trend, RSI should be low

	now := time.Now()
	data := []models.OHLCV{
		{Timestamp: now.Add(-5 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-4 * time.Minute), Close: 11, Symbol: "BTC"},
		{Timestamp: now.Add(-3 * time.Minute), Close: 12, Symbol: "BTC"}, // RSI should be 100 here (Up, Up)
	}

	// Test Overbought
	signal := strategy.OnData(data)
	if signal.Type != models.SignalSell {
		t.Errorf("Expected Sell signal for overbought condition, got %s", signal.Type)
	}

	// Test Oversold
	dataOversold := []models.OHLCV{
		{Timestamp: now.Add(-5 * time.Minute), Close: 15, Symbol: "BTC"},
		{Timestamp: now.Add(-4 * time.Minute), Close: 14, Symbol: "BTC"},
		{Timestamp: now.Add(-3 * time.Minute), Close: 13, Symbol: "BTC"}, // RSI should be 0 here (Down, Down)
	}

	signalOversold := strategy.OnData(dataOversold)
	if signalOversold.Type != models.SignalBuy {
		t.Errorf("Expected Buy signal for oversold condition, got %s", signalOversold.Type)
	}

	// Test Hold
	dataHold := []models.OHLCV{
		{Timestamp: now.Add(-5 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-4 * time.Minute), Close: 12, Symbol: "BTC"}, // +2
		{Timestamp: now.Add(-3 * time.Minute), Close: 11, Symbol: "BTC"}, // -1
		// AvgGain = 1, AvgLoss = 0.5. RS = 2. RSI = 100 - (100/3) = 66.6
		// 30 < 66.6 < 70 -> Hold
	}

	signalHold := strategy.OnData(dataHold)
	if signalHold.Type != models.SignalHold {
		t.Errorf("Expected Hold signal for neutral condition, got %s. Reason: %s", signalHold.Type, signalHold.Reason)
	}
}
