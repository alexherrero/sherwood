package indicators

import (
	"math"
	"testing"
)

func TestSMA(t *testing.T) {
	data := []float64{10, 20, 30, 40, 50}
	period := 3
	expected := []float64{math.NaN(), math.NaN(), 20, 30, 40}

	result := SMA(data, period)

	if len(result) != len(expected) {
		t.Fatalf("Expected length %d, got %d", len(expected), len(result))
	}

	for i := 0; i < len(result); i++ {
		if math.IsNaN(expected[i]) {
			if !math.IsNaN(result[i]) {
				t.Errorf("Index %d: expected NaN, got %f", i, result[i])
			}
		} else {
			if math.Abs(result[i]-expected[i]) > 0.001 {
				t.Errorf("Index %d: expected %f, got %f", i, expected[i], result[i])
			}
		}
	}
}

func TestEMA(t *testing.T) {
	// Simple test case
	data := []float64{2, 4, 6, 8, 10}
	period := 3
	// SMA of first 3 is (2+4+6)/3 = 4
	// Multiplier k = 2/(3+1) = 0.5
	// EMA[3] (index 3, value 8) = (8 - 4) * 0.5 + 4 = 4 * 0.5 + 4 = 6
	// EMA[4] (index 4, value 10) = (10 - 6) * 0.5 + 6 = 4 * 0.5 + 6 = 8
	expected := []float64{math.NaN(), math.NaN(), 4, 6, 8}

	result := EMA(data, period)

	for i := 0; i < len(result); i++ {
		if math.IsNaN(expected[i]) {
			if !math.IsNaN(result[i]) {
				t.Errorf("Index %d: expected NaN, got %f", i, result[i])
			}
		} else {
			if math.Abs(result[i]-expected[i]) > 0.001 {
				t.Errorf("Index %d: expected %f, got %f", i, expected[i], result[i])
			}
		}
	}
}

func TestRSI(t *testing.T) {
	// Standard RSI test is hard to do manually, but we can check basic behavior
	// E.g. straight up trend should be 100
	data := []float64{10, 11, 12, 13, 14, 15}
	period := 2

	// Changes: 1, 1, 1, 1, 1
	// Period = 2
	// Index 2 (val 12): AvgGain=1, AvgLoss=0 -> RSI=100
	// Index 3 (val 13): AvgGain=(1*1 + 1)/2 = 1, AvgLoss=0 -> RSI=100

	result := RSI(data, period)
	if result[period] != 100 {
		t.Errorf("Expected RSI 100 for uptrend, got %f", result[period])
	}

	// Down trend
	dataDown := []float64{15, 14, 13, 12, 11}
	resultDown := RSI(dataDown, 2)
	// Changes: -1, -1, -1, -1
	// Index 2 (val 13): AvgGain=0, AvgLoss=1 -> RSI=0
	if resultDown[period] != 0 {
		t.Errorf("Expected RSI 0 for downtrend, got %f", resultDown[period])
	}
}

func TestBollingerBands(t *testing.T) {
	data := []float64{10, 10, 10, 10, 10}
	period := 5
	stdDevMult := 2.0

	// StdDev should be 0
	upper, middle, lower := BollingerBands(data, period, stdDevMult)

	if middle[4] != 10 {
		t.Errorf("Expected Middle Band 10, got %f", middle[4])
	}
	if upper[4] != 10 {
		t.Errorf("Expected Upper Band 10, got %f", upper[4])
	}
	if lower[4] != 10 {
		t.Errorf("Expected Lower Band 10, got %f", lower[4])
	}
}

func TestStdDev(t *testing.T) {
	data := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	period := 3
	result := StdDev(data, period)

	// verify length
	if len(result) != len(data) {
		t.Fatalf("Expected length %d, got %d", len(data), len(result))
	}

	// verify first few are NaN
	if !math.IsNaN(result[0]) || !math.IsNaN(result[1]) {
		t.Error("Expected NaN for first few elements")
	}

	// Index 3 (4th element) uses window [4, 4, 4] -> should be 0
	if result[3] != 0 {
		t.Errorf("Expected 0 stddev at index 3, got %f", result[3])
	}

	// Manual check for index 5 (4, 5, 5) -> mean = 14/3 = 4.666
	// var = ((4-4.66)^2 + (5-4.66)^2 + (5-4.66)^2) / 3
	//     = (0.44 + 0.11 + 0.11) / 3 = 0.66/3 = 0.22 -> sqrt = 0.47
	// approximate check
	if math.Abs(result[5]-0.471) > 0.01 {
		t.Errorf("Expected ~0.471 stddev, got %f", result[5])
	}
}

func TestMACD(t *testing.T) {
	data := []float64{
		10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21,
		20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9,
		10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21,
	}
	fastPeriod := 12
	slowPeriod := 26
	signalPeriod := 9

	macdLine, signalLine, histogram := MACD(data, fastPeriod, slowPeriod, signalPeriod)

	if len(macdLine) != len(data) {
		t.Fatalf("Expected length %d, got %d", len(data), len(macdLine))
	}

	// MACD calculation requires enough data points for the slow EMA period.
	// The MACD line (EMA12 - EMA26) becomes valid from the 26th bar onwards.

	// We check for No Panic and Length consistency mostly, and non-NaN at end.
	lastIndex := len(data) - 1
	if math.IsNaN(macdLine[lastIndex]) {
		t.Error("Expected valid MACD at end")
	}
	if math.IsNaN(signalLine[lastIndex]) {
		t.Error("Expected valid Signal Line at end")
	}
	if math.IsNaN(histogram[lastIndex]) {
		t.Error("Expected valid Histogram at end")
	}
}
