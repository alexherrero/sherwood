package indicators

import (
	"math"
)

// SMA calculates the Simple Moving Average.
func SMA(data []float64, period int) []float64 {
	if len(data) < period || period <= 0 {
		return nil
	}
	sma := make([]float64, len(data))
	for i := 0; i < len(data); i++ {
		if i < period-1 {
			sma[i] = math.NaN()
			continue
		}
		var sum float64
		for j := 0; j < period; j++ {
			sum += data[i-j]
		}
		sma[i] = sum / float64(period)
	}
	return sma
}

// EMA calculates the Exponential Moving Average.
func EMA(data []float64, period int) []float64 {
	if len(data) < period || period <= 0 {
		return nil
	}
	ema := make([]float64, len(data))
	k := 2.0 / float64(period+1)

	// First EMA is SMA
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += data[i]
	}
	ema[period-1] = sum / float64(period)

	// Fill initial NaN
	for i := 0; i < period-1; i++ {
		ema[i] = math.NaN()
	}

	for i := period; i < len(data); i++ {
		ema[i] = (data[i]-ema[i-1])*k + ema[i-1]
	}
	return ema
}

// StdDev calculates the rolling Standard Deviation.
func StdDev(data []float64, period int) []float64 {
	if len(data) < period || period <= 0 {
		return nil
	}
	stdDev := make([]float64, len(data))
	sma := SMA(data, period)

	for i := 0; i < len(data); i++ {
		if i < period-1 {
			stdDev[i] = math.NaN()
			continue
		}
		var varianceSum float64
		for j := 0; j < period; j++ {
			diff := data[i-j] - sma[i]
			varianceSum += diff * diff
		}
		stdDev[i] = math.Sqrt(varianceSum / float64(period))
	}
	return stdDev
}

// RSI calculates the Relative Strength Index.
func RSI(data []float64, period int) []float64 {
	if len(data) < period+1 || period <= 0 {
		return nil
	}
	rsi := make([]float64, len(data))

	gains := make([]float64, len(data))
	losses := make([]float64, len(data))

	for i := 1; i < len(data); i++ {
		change := data[i] - data[i-1]
		if change > 0 {
			gains[i] = change
			losses[i] = 0
		} else {
			gains[i] = 0
			losses[i] = -change
		}
	}

	// Calculate initial average gain/loss
	var avgGain, avgLoss float64
	for i := 1; i <= period; i++ {
		avgGain += gains[i]
		avgLoss += losses[i]
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)

	// Fill initial values with NaN
	for i := 0; i < period; i++ {
		rsi[i] = math.NaN()
	}

	// First RSI
	if avgLoss == 0 {
		rsi[period] = 100
	} else {
		rs := avgGain / avgLoss
		rsi[period] = 100 - (100 / (1 + rs))
	}

	// Subsequent RSI values using smoothed averages
	for i := period + 1; i < len(data); i++ {
		avgGain = (avgGain*float64(period-1) + gains[i]) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + losses[i]) / float64(period)

		if avgLoss == 0 {
			rsi[i] = 100
		} else {
			rs := avgGain / avgLoss
			rsi[i] = 100 - (100 / (1 + rs))
		}
	}

	return rsi
}

// BollingerBands calculates the Bollinger Bands (Upper, Middle, Lower).
func BollingerBands(data []float64, period int, stdDevMultiplier float64) ([]float64, []float64, []float64) {
	middle := SMA(data, period)
	std := StdDev(data, period)
	upper := make([]float64, len(data))
	lower := make([]float64, len(data))

	for i := 0; i < len(data); i++ {
		if math.IsNaN(middle[i]) {
			upper[i] = math.NaN()
			lower[i] = math.NaN()
			continue
		}
		upper[i] = middle[i] + (std[i] * stdDevMultiplier)
		lower[i] = middle[i] - (std[i] * stdDevMultiplier)
	}
	return upper, middle, lower
}

// MACD calculates the Moving Average Convergence Divergence.
func MACD(data []float64, fastPeriod, slowPeriod, signalPeriod int) ([]float64, []float64, []float64) {
	fastEMA := EMA(data, fastPeriod)
	slowEMA := EMA(data, slowPeriod)
	macdLine := make([]float64, len(data))

	// Calculate MACD Line
	for i := 0; i < len(data); i++ {
		if math.IsNaN(fastEMA[i]) || math.IsNaN(slowEMA[i]) {
			macdLine[i] = math.NaN()
		} else {
			macdLine[i] = fastEMA[i] - slowEMA[i]
		}
	}

	// We need to calculate Signal Line based on valid MACD values
	// But since EMA function expects a strict array, we need to handle the initial NaNs carefully
	// A simple approach is to pass the macdLine to EMA, which handles initial NaNs by propagating them
	// However, the "start" of valid data for signal line is later than for macd line.

	// Let's create a slice that starts from where MACD becomes valid to calculate Signal properly?
	// Actually, our EMA implementation handles incoming NaNs by setting output to NaN if input has NaNs locally?
	// Wait, my EMA implementation assumes clean data for the first `period` elements to calculate initial SMA.
	// If input has NaNs at start, `EMA` needs adaptation or we handle it.

	// Improved EMA for MACD Signal Line:
	// We can't just pass `macdLine` with NaNs to `EMA` as written because `EMA` takes strict sums.

	signalLine := make([]float64, len(data))
	histogram := make([]float64, len(data))

	// Find first valid MACD index
	firstValidIdx := -1
	for i := 0; i < len(data); i++ {
		if !math.IsNaN(macdLine[i]) {
			firstValidIdx = i
			break
		}
		signalLine[i] = math.NaN()
		histogram[i] = math.NaN()
	}

	if firstValidIdx == -1 || len(data)-firstValidIdx < signalPeriod {
		// Not enough data
		return macdLine, signalLine, histogram // all NaNs or incomplete
	}

	// Extract valid MACD part
	validMACD := macdLine[firstValidIdx:]
	// Calculate Signal on valid part
	validSignal := EMA(validMACD, signalPeriod)

	// Map back
	for i := 0; i < len(validSignal); i++ {
		signalLine[firstValidIdx+i] = validSignal[i]
		if !math.IsNaN(validSignal[i]) {
			histogram[firstValidIdx+i] = macdLine[firstValidIdx+i] - validSignal[i]
		} else {
			histogram[firstValidIdx+i] = math.NaN()
		}
	}

	return macdLine, signalLine, histogram
}
