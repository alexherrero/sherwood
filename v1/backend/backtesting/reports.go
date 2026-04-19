// Package backtesting provides report generation for backtest results.
package backtesting

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Report generates human-readable reports from backtest results.
type Report struct {
	Result *BacktestResult
}

// NewReport creates a new report generator.
//
// Args:
//   - result: The backtest result to report on
//
// Returns:
//   - *Report: The report generator
func NewReport(result *BacktestResult) *Report {
	return &Report{Result: result}
}

// Summary returns a text summary of the backtest.
//
// Returns:
//   - string: Formatted summary text
func (r *Report) Summary() string {
	if r.Result == nil || r.Result.Metrics == nil {
		return "No backtest results available."
	}

	m := r.Result.Metrics
	c := r.Result.Config

	var sb strings.Builder

	sb.WriteString("═══════════════════════════════════════════════════════════════\n")
	sb.WriteString(fmt.Sprintf("                    BACKTEST REPORT: %s\n", r.Result.ID))
	sb.WriteString("═══════════════════════════════════════════════════════════════\n\n")

	sb.WriteString("CONFIGURATION\n")
	sb.WriteString("───────────────────────────────────────────────────────────────\n")
	sb.WriteString(fmt.Sprintf("  Strategy:        %s\n", r.Result.Strategy))
	sb.WriteString(fmt.Sprintf("  Symbol:          %s\n", c.Symbol))
	sb.WriteString(fmt.Sprintf("  Period:          %s to %s\n",
		c.StartDate.Format("2006-01-02"), c.EndDate.Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("  Initial Capital: $%.2f\n", c.InitialCapital))
	sb.WriteString(fmt.Sprintf("  Commission:      $%.2f per trade\n", c.Commission))
	sb.WriteString("\n")

	sb.WriteString("PERFORMANCE METRICS\n")
	sb.WriteString("───────────────────────────────────────────────────────────────\n")
	sb.WriteString(fmt.Sprintf("  Total Return:      %+.2f%% ($%+.2f)\n", m.TotalReturn, m.TotalReturnAbs))
	sb.WriteString(fmt.Sprintf("  Final Equity:      $%.2f\n", m.FinalEquity))
	sb.WriteString(fmt.Sprintf("  Annualized Return: %+.2f%%\n", m.AnnualizedReturn))
	sb.WriteString(fmt.Sprintf("  Sharpe Ratio:      %.2f\n", m.SharpeRatio))
	sb.WriteString(fmt.Sprintf("  Max Drawdown:      -%.2f%% ($%.2f)\n", m.MaxDrawdown, m.MaxDrawdownAbs))
	sb.WriteString(fmt.Sprintf("  Volatility:        %.2f%%\n", m.Volatility))
	sb.WriteString("\n")

	sb.WriteString("TRADE STATISTICS\n")
	sb.WriteString("───────────────────────────────────────────────────────────────\n")
	sb.WriteString(fmt.Sprintf("  Total Trades:    %d\n", m.TotalTrades))
	sb.WriteString(fmt.Sprintf("  Winning Trades:  %d (%.1f%%)\n", m.WinningTrades, m.WinRate))
	sb.WriteString(fmt.Sprintf("  Losing Trades:   %d\n", m.LosingTrades))
	sb.WriteString(fmt.Sprintf("  Average Win:     $%.2f\n", m.AverageWin))
	sb.WriteString(fmt.Sprintf("  Average Loss:    $%.2f\n", m.AverageLoss))
	sb.WriteString(fmt.Sprintf("  Profit Factor:   %.2f\n", m.ProfitFactor))
	sb.WriteString("\n")

	sb.WriteString("═══════════════════════════════════════════════════════════════\n")
	sb.WriteString(fmt.Sprintf("  Generated: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString("═══════════════════════════════════════════════════════════════\n")

	return sb.String()
}

// TradeList returns a formatted list of all trades.
//
// Returns:
//   - string: Formatted trade list
func (r *Report) TradeList() string {
	if r.Result == nil || len(r.Result.Trades) == 0 {
		return "No trades executed."
	}

	var sb strings.Builder
	sb.WriteString("TRADE LIST\n")
	sb.WriteString("───────────────────────────────────────────────────────────────\n")
	sb.WriteString("  #   Entry Date   Exit Date    Side   Entry    Exit     P&L\n")
	sb.WriteString("───────────────────────────────────────────────────────────────\n")

	for i, t := range r.Result.Trades {
		pnlSign := "+"
		if t.PnL < 0 {
			pnlSign = ""
		}
		sb.WriteString(fmt.Sprintf(" %3d  %s  %s  %-4s  $%7.2f  $%7.2f  %s$%.2f\n",
			i+1,
			t.EntryTime.Format("2006-01-02"),
			t.ExitTime.Format("2006-01-02"),
			t.Side,
			t.EntryPrice,
			t.ExitPrice,
			pnlSign,
			t.PnL,
		))
	}

	return sb.String()
}

// JSON returns the result as JSON.
//
// Returns:
//   - []byte: JSON-encoded result
//   - error: Any encoding error
func (r *Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r.Result, "", "  ")
}

// MetricsJSON returns just the metrics as JSON.
//
// Returns:
//   - []byte: JSON-encoded metrics
//   - error: Any encoding error
func (r *Report) MetricsJSON() ([]byte, error) {
	if r.Result == nil || r.Result.Metrics == nil {
		return []byte("{}"), nil
	}
	return json.MarshalIndent(r.Result.Metrics, "", "  ")
}
