# Backtesting Framework

The backtesting framework simulates trading strategies against historical data.

## Quick Start

```go
import (
    "github.com/alexherrero/sherwood/backend/backtesting"
    "github.com/alexherrero/sherwood/backend/strategies"
)

// Create strategy
strategy := strategies.NewMACrossover()
strategy.Init(map[string]interface{}{
    "short_period": 12,
    "long_period":  26,
})

// Configure backtest
config := backtesting.BacktestConfig{
    Symbol:         "AAPL",
    StartDate:      time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
    EndDate:        time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC),
    InitialCapital: 100000.0,
    Commission:     1.0,
}

// Run backtest
engine := backtesting.NewEngine()
result, err := engine.Run(strategy, historicalData, config)

// Generate report
report := backtesting.NewReport(result)
fmt.Println(report.Summary())
```

## Performance Metrics

| Metric | Description |
|--------|-------------|
| Total Return | Overall percentage return |
| Sharpe Ratio | Risk-adjusted return (annualized) |
| Max Drawdown | Largest peak-to-trough decline |
| Win Rate | Percentage of profitable trades |
| Profit Factor | Gross profits / gross losses |
| Volatility | Standard deviation of returns |

## Report Formats

### Text Summary

```go
report := backtesting.NewReport(result)
fmt.Println(report.Summary())
```

### JSON Export

```go
jsonData, _ := report.JSON()
fmt.Println(string(jsonData))
```

### Trade List

```go
fmt.Println(report.TradeList())
```

## Configuration Options

| Option | Type | Description |
|--------|------|-------------|
| `Symbol` | string | Ticker symbol to test |
| `StartDate` | time.Time | Backtest start date |
| `EndDate` | time.Time | Backtest end date |
| `InitialCapital` | float64 | Starting capital |
| `PositionSize` | float64 | Fixed position size (0 = use 95% of available cash) |
| `Commission` | float64 | Commission per trade (flat fee) |

## Limitations

- **Long-only**: Focused on spot trading currently.
- **Single symbol**: One asset per backtest run.
- **Slippage**: No execution slippage modeling.
- **Fills**: Simulated at the next bar's Close price.
