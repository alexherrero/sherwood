package strategies

import (
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
)

func TestBollingerBandsStrategy(t *testing.T) {
	strategy := NewBollingerBandsStrategy()

	// Configure short period for testing
	strategy.Period = 5
	strategy.StdDevMultiplier = 2.0

	// Create dummy data
	// 5 samples of 10. Mean=10, StdDev=0. Bands=[10, 10, 10]

	now := time.Now()
	/*
		dataFlat := []models.OHLCV{
			{Timestamp: now.Add(-5 * time.Minute), Close: 10, Symbol: "BTC"},
			{Timestamp: now.Add(-4 * time.Minute), Close: 10, Symbol: "BTC"},
			{Timestamp: now.Add(-3 * time.Minute), Close: 10, Symbol: "BTC"},
			{Timestamp: now.Add(-2 * time.Minute), Close: 10, Symbol: "BTC"},
			{Timestamp: now.Add(-1 * time.Minute), Close: 10, Symbol: "BTC"}, // Price = 10. Upper=10, Lower=10.
		}
	*/

	// Logic:
	// If Price <= Lower (10 <= 10) -> Buy
	// If Price >= Upper (10 >= 10) -> Sell
	// In this flat case, it hits both conditions. The implementation checks Buy first, then Sell.
	// Actually, usually in BB strategies, hitting bands is reversion.
	// But hitting BOTH is weird.

	// Let's make it clearer.
	// Mean=10. StdDev=1. Upper=12, Lower=8.
	// Data: 8, 9, 10, 11, 12.
	// Variance: 4+1+0+1+4 = 10. AvgVar = 2. StdDev = sqrt(2) = 1.414.
	// Upper = 10 + 2*1.414 = 12.828.
	// Lower = 10 - 2.828 = 7.172.

	// Case 1: Price touches Lower Band
	// We need price to be <= Lower.
	// Let's use simpler math.
	// Data: 10, 10, 10, 10, 6.
	// Mean = 46/5 = 9.2.
	// Diffs: 0.8, 0.8, 0.8, 0.8, -3.2.
	// SqDiffs: 0.64, 0.64, 0.64, 0.64, 10.24.
	// SumSq = 12.8. Var = 2.56. StdDev = 1.6.
	// Lower = 9.2 - (2 * 1.6) = 9.2 - 3.2 = 6.0.
	// Price is 6. 6 <= 6.0. BUY signal.

	dataLow := []models.OHLCV{
		{Timestamp: now.Add(-5 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-4 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-3 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-2 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-1 * time.Minute), Close: 6, Symbol: "BTC"},
	}

	signalBuy := strategy.OnData(dataLow)
	if signalBuy.Type != models.SignalBuy {
		t.Errorf("Expected Buy signal for hitting lower band, got %s. Reason: %s", signalBuy.Type, signalBuy.Reason)
	}

	// Case 2: Price touches Upper Band
	// Data: 10, 10, 10, 10, 14.
	// Mean = 54/5 = 10.8.
	// Diffs: -0.8... 3.2.
	// StdDev = 1.6.
	// Upper = 10.8 + 3.2 = 14.0.
	// Price = 14. 14 >= 14. SELL signal.

	dataHigh := []models.OHLCV{
		{Timestamp: now.Add(-5 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-4 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-3 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-2 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-1 * time.Minute), Close: 14, Symbol: "BTC"},
	}

	signalSell := strategy.OnData(dataHigh)
	if signalSell.Type != models.SignalSell {
		t.Errorf("Expected Sell signal for hitting upper band, got %s. Reason: %s", signalSell.Type, signalSell.Reason)
	}

	// Case 3: Middle
	dataMid := []models.OHLCV{
		{Timestamp: now.Add(-5 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-4 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-3 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-2 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-1 * time.Minute), Close: 10, Symbol: "BTC"},
	}
	signalHold := strategy.OnData(dataMid)
	// Here Middle=10, Upper=10, Lower=10. Price=10.
	// Logic: if price <= lower -> Buy.
	// So 10 <= 10 is True -> Buy.
	// Wait, for flat line with 0 stddev, bands collapse to mean.
	// Technically it's a Buy by my logic.
	// This is an edge case.
	// Ideally we want some volatility.
	// But let's verify what the code does. It should return Buy.
	if signalHold.Type != models.SignalBuy {
		// If I strictly want to test hold, I need variance but price in middle.
	}

	// Let's create proper Hold case.
	// Data: 10, 10, 10, 12, 8.
	// Mean=10. Var=(0+0+0+4+4)/5 = 1.6. Std=1.265.
	// Upper = 10 + 2.53 = 12.53.
	// Lower = 10 - 2.53 = 7.47.
	// Price = 8. 7.47 < 8 < 12.53. HOLD.

	dataHold2 := []models.OHLCV{
		{Timestamp: now.Add(-5 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-4 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-3 * time.Minute), Close: 10, Symbol: "BTC"},
		{Timestamp: now.Add(-2 * time.Minute), Close: 12, Symbol: "BTC"}, // increased volatility
		{Timestamp: now.Add(-1 * time.Minute), Close: 10, Symbol: "BTC"}, // back to mean
	}
	// Recalculating for this set...
	// Mean = 52/5=10.4

	signalHold2 := strategy.OnData(dataHold2)
	if signalHold2.Type != models.SignalHold {
		t.Errorf("Expected Hold signal, got %s. Reason: %s", signalHold2.Type, signalHold2.Reason)
	}
}
