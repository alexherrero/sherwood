package backtest

import (
	"bufio"
	"fmt"
	"io"
	"time"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/clock"
)

// percent writes a ratio as a percentage to two places, halves away from zero.
func percent(r decimal.Decimal) string {
	return r.Shift(2).Round(2).StringFixed(2) + "%"
}

func date(t time.Time) string { return t.In(clock.Eastern).Format(clock.DateLayout) }

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Write prints the report as plain text. The same report always prints the
// same bytes. With trades set, each strategy's filled orders follow its
// figures.
func (r *Report) Write(w io.Writer, trades bool) error {
	bw := bufio.NewWriter(w)
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(bw, format+"\n", a...) }
	p("Sherwood back-test")
	p("Strategy file hash: %s", r.StrategyHash)
	p("Starting capital: %s", r.Capital)
	p("Orders from one day's close are filled on the next trading day by the paper fill model.")
	p("This replays past prices. It is not a forecast, and it is not financial advice.")
	for _, res := range r.Results {
		p("")
		p("%s", res.Name)
		p("  Trading days:     %s to %s (%d)", date(res.First), date(res.Last), res.Days)
		p("  Capital:          %s (%s of the starting capital)", res.Capital, res.PortfolioShare)
		p("  Final value:      %s", res.Final.RoundCents())
		p("  Return:           %s", percent(res.Return))
		p("  Max drawdown:     %s", percent(res.MaxDrawdown))
		if res.Sells == 0 {
			p("  Win rate:         no sells to count")
		} else {
			p("  Win rate:         %s (%d of %s made money)", percent(res.WinRate), res.Wins, plural(res.Sells, "sell", "sells"))
		}
		p("  Turnover:         %s (bought %s, sold %s, average value %s)", res.Turnover.Round(2).StringFixed(2),
			res.BoughtValue.RoundCents(), res.SoldValue.RoundCents(), res.AverageValue.RoundCents())
		p("  Orders:           %s and %s filled (%d at a stop); %d buys unfilled",
			plural(res.Buys, "buy", "buys"), plural(res.Sells, "sell", "sells"), res.StopSells, res.Unfilled)
		p("  Blocked buys:     %d for cash, %d for max_holdings, %d with no sizing rule",
			res.BlockedCash, res.BlockedHoldings, res.NoSizing)
		p("  Held at the end:  %s", plural(res.HeldAtEnd, "stock", "stocks"))
		if trades {
			for _, t := range res.Trades {
				line := fmt.Sprintf("    %s %-4s %-6s %s at %s = %s", date(t.Date), t.Side, t.Symbol, t.Quantity, t.Price, t.Value.RoundCents())
				if t.Side == Sold {
					line += fmt.Sprintf(", profit %s", t.Profit.RoundCents())
				}
				p("%s (%s)", line, t.Why)
			}
		}
	}
	return bw.Flush()
}
