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
