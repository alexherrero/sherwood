package backtesting

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/backend/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewReport verifies report creation.
func TestNewReport(t *testing.T) {
	result := &BacktestResult{
		ID:       "bt-001",
		Strategy: "test",
		Metrics:  &Metrics{TotalReturn: 10.0},
	}

	report := NewReport(result)

	assert.NotNil(t, report)
	assert.Equal(t, result, report.Result)
}

// TestReport_Summary verifies summary generation.
func TestReport_Summary(t *testing.T) {
	result := &BacktestResult{
		ID:       "bt-001",
		Strategy: "ma_crossover",
		Config: BacktestConfig{
			Symbol:         "AAPL",
			StartDate:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			EndDate:        time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
			InitialCapital: 10000,
			Commission:     1.0,
		},
		Metrics: &Metrics{
			TotalReturn:    15.5,
			TotalReturnAbs: 1550.0,
			FinalEquity:    11550.0,
			SharpeRatio:    1.25,
			MaxDrawdown:    8.0,
			TotalTrades:    25,
			WinningTrades:  15,
			WinRate:        60.0,
			AverageWin:     150.0,
			AverageLoss:    80.0,
			ProfitFactor:   1.8,
		},
	}

	report := NewReport(result)
	summary := report.Summary()

	// Verify summary contains expected sections
	assert.Contains(t, summary, "BACKTEST REPORT")
	assert.Contains(t, summary, "bt-001")
	assert.Contains(t, summary, "ma_crossover")
	assert.Contains(t, summary, "AAPL")
	assert.Contains(t, summary, "PERFORMANCE METRICS")
	assert.Contains(t, summary, "TRADE STATISTICS")
	assert.Contains(t, summary, "15.5") // Total return
}

// TestReport_Summary_NilResult verifies handling of nil result.
func TestReport_Summary_NilResult(t *testing.T) {
	report := NewReport(nil)
	summary := report.Summary()

	assert.Equal(t, "No backtest results available.", summary)
}

// TestReport_Summary_NilMetrics verifies handling of nil metrics.
func TestReport_Summary_NilMetrics(t *testing.T) {
	result := &BacktestResult{
		ID:      "bt-001",
		Metrics: nil,
	}

	report := NewReport(result)
	summary := report.Summary()

	assert.Equal(t, "No backtest results available.", summary)
}

// TestReport_TradeList verifies trade list generation.
func TestReport_TradeList(t *testing.T) {
	result := &BacktestResult{
		Trades: []SimulatedTrade{
			{
				EntryTime:  time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
				ExitTime:   time.Date(2024, 1, 20, 0, 0, 0, 0, time.UTC),
				Symbol:     "AAPL",
				Side:       models.OrderSideBuy,
				EntryPrice: 150.0,
				ExitPrice:  160.0,
				Quantity:   10,
				PnL:        100.0,
			},
			{
				EntryTime:  time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
				ExitTime:   time.Date(2024, 2, 10, 0, 0, 0, 0, time.UTC),
				Symbol:     "AAPL",
				Side:       models.OrderSideBuy,
				EntryPrice: 165.0,
				ExitPrice:  155.0,
				Quantity:   10,
				PnL:        -100.0,
			},
		},
	}

	report := NewReport(result)
	tradeList := report.TradeList()

	assert.Contains(t, tradeList, "TRADE LIST")
	assert.Contains(t, tradeList, "2024-01-15")
	assert.Contains(t, tradeList, "2024-01-20")
	assert.Contains(t, tradeList, "150.00")
	assert.Contains(t, tradeList, "160.00")
	assert.Contains(t, tradeList, "+$100.00")
	assert.Contains(t, tradeList, "$-100.00")
}

// TestReport_TradeList_NoTrades verifies handling of no trades.
func TestReport_TradeList_NoTrades(t *testing.T) {
	result := &BacktestResult{Trades: []SimulatedTrade{}}
	report := NewReport(result)
	tradeList := report.TradeList()

	assert.Equal(t, "No trades executed.", tradeList)
}

// TestReport_TradeList_NilResult verifies handling of nil result.
func TestReport_TradeList_NilResult(t *testing.T) {
	report := NewReport(nil)
	tradeList := report.TradeList()

	assert.Equal(t, "No trades executed.", tradeList)
}

// TestReport_JSON verifies JSON export.
func TestReport_JSON(t *testing.T) {
	result := &BacktestResult{
		ID:       "bt-001",
		Strategy: "ma_crossover",
		Metrics:  &Metrics{TotalReturn: 10.0},
	}

	report := NewReport(result)
	jsonData, err := report.JSON()

	require.NoError(t, err)
	assert.NotEmpty(t, jsonData)

	// Verify it's valid JSON
	var parsed map[string]interface{}
	err = json.Unmarshal(jsonData, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "bt-001", parsed["ID"])
}

// TestReport_MetricsJSON verifies metrics JSON export.
func TestReport_MetricsJSON(t *testing.T) {
	result := &BacktestResult{
		Metrics: &Metrics{
			TotalReturn: 15.5,
			SharpeRatio: 1.25,
			WinRate:     60.0,
		},
	}

	report := NewReport(result)
	jsonData, err := report.MetricsJSON()

	require.NoError(t, err)

	var metrics map[string]interface{}
	err = json.Unmarshal(jsonData, &metrics)
	require.NoError(t, err)
	assert.Equal(t, 15.5, metrics["total_return"])
	assert.Equal(t, 1.25, metrics["sharpe_ratio"])
}

// TestReport_MetricsJSON_NilResult verifies handling of nil result.
func TestReport_MetricsJSON_NilResult(t *testing.T) {
	report := NewReport(nil)
	jsonData, err := report.MetricsJSON()

	require.NoError(t, err)
	assert.Equal(t, "{}", string(jsonData))
}

// TestReport_MetricsJSON_NilMetrics verifies handling of nil metrics.
func TestReport_MetricsJSON_NilMetrics(t *testing.T) {
	result := &BacktestResult{Metrics: nil}
	report := NewReport(result)
	jsonData, err := report.MetricsJSON()

	require.NoError(t, err)
	assert.Equal(t, "{}", string(jsonData))
}
