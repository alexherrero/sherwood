package backtest

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/prices"
)

// Every price, strategy and amount here is made up for the tests.

// start is a made-up first trading date. The back-tester does not read the
// exchange calendar, so the tests use consecutive dates.
var start = time.Date(2025, time.March, 3, 0, 0, 0, 0, clock.Eastern)

func dateN(i int) time.Time { return start.AddDate(0, 0, i) }

// bars builds one stock's bars from rows of open, high, low, close.
func bars(t *testing.T, sym string, rows ...[4]string) []prices.Bar {
	t.Helper()
	out := make([]prices.Bar, len(rows))
	for i, r := range rows {
		out[i] = prices.Bar{Symbol: sym, Date: dateN(i),
			Open: money.MustParsePrice(r[0]), High: money.MustParsePrice(r[1]),
			Low: money.MustParsePrice(r[2]), Close: money.MustParsePrice(r[3])}
		if err := out[i].Check(); err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
	}
	return out
}

// strategyFile is a made-up strategy file with one or more strategies.
func strategyFile(t *testing.T, maxHoldings int, strategies string) *config.Strategy {
	t.Helper()
	text := fmt.Sprintf(`version: 1
limits:
  max_stock_share: 0.50
  max_order_value: "1000.00"
  max_orders_per_day: 5
  daily_loss_limit: "100.00"
  max_holdings: %d
  max_limit_distance: 0.05
  buy_limit_margin: 0.01
  fractional_shares: false
  price_cross_check_tolerance: 0.02
  min_paper_weeks: 4
watchlist: []
strategies:
%s`, maxHoldings, strategies)
	s, err := config.ParseStrategy([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func breakoutStrategy(stocks, extra string) string {
	return `  - name: test-breakout
    portfolio_share: 0.50
    stocks: [` + stocks + `]
    rules:
      - type: breakout
        settings:
          days: 2
          exit_days: 2
` + extra
}

const fixedAmount = `      - type: size_fixed_amount
        settings:
          amount: "100.00"
`

// path is a made-up stock that breaks out on day 4 and breaks down on day 7.
var path = [][4]string{
	{"10.00", "10.00", "10.00", "10.00"},
	{"10.00", "10.00", "10.00", "10.00"},
	{"10.00", "10.00", "10.00", "10.00"},
	{"10.00", "10.00", "10.00", "10.00"},
	{"10.00", "10.60", "10.00", "10.50"}, // 4: closes above its 2-day high: buy, limit 10.61
	{"10.40", "10.80", "10.30", "10.70"}, // 5: opens at 10.40, under the limit: 9 shares fill at 10.40
	{"10.70", "10.75", "10.20", "10.25"}, // 6
	{"10.20", "10.30", "9.80", "9.90"},   // 7: closes below its 2-day low: sell
	{"9.95", "10.00", "9.90", "9.95"},    // 8: sold at the open, 9.95
}

func run(t *testing.T, cfg Config) *Result {
	t.Helper()
	rep, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 1 {
		t.Fatalf("%d results, want 1", len(rep.Results))
	}
	return &rep.Results[0]
}

func wantTrades(t *testing.T, r *Result, want ...string) {
	t.Helper()
	var got []string
	for _, tr := range r.Trades {
		got = append(got, fmt.Sprintf("%s %s %s %s at %s", date(tr.Date), tr.Side, tr.Symbol, tr.Quantity, tr.Price))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("trades:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSignalsFillOnTheNextDay(t *testing.T) {
	r := run(t, Config{
		Strategy: strategyFile(t, 20, breakoutStrategy("AAA", fixedAmount)),
		Bars:     map[string][]prices.Bar{"AAA": bars(t, "AAA", path...)},
		Capital:  money.MustParseMoney("1000.00"),
	})
	wantTrades(t, r,
		"2025-03-08 buy AAA 9 at 10.40",
		"2025-03-11 sell AAA 9 at 9.95",
	)
	for name, got := range map[string]string{
		"capital":      r.Capital.String(),
		"final":        r.Final.String(),
		"return":       percent(r.Return),
		"max drawdown": percent(r.MaxDrawdown),
		"win rate":     percent(r.WinRate),
		"turnover":     r.Turnover.StringFixed(2),
		"average":      r.AverageValue.RoundCents().String(),
		"profit":       r.Trades[1].Profit.String(),
	} {
		want := map[string]string{
			"capital":      "500.00",
			"final":        "495.95",
			"return":       "-0.81%",
			"max drawdown": "1.43%", // 502.70 on day 5 down to 495.50 on day 7
			"win rate":     "0.00%",
			"turnover":     "0.18", // (93.60 + 89.55) / 2 / 499.20
			"average":      "499.20",
			"profit":       "-4.05",
		}[name]
		if got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
	if r.Days != len(path) || r.Buys != 1 || r.Sells != 1 || r.Wins != 0 || r.HeldAtEnd != 0 {
		t.Errorf("days %d buys %d sells %d wins %d held %d", r.Days, r.Buys, r.Sells, r.Wins, r.HeldAtEnd)
	}
}

func TestRestingStop(t *testing.T) {
	stop := `      - type: trailing_stop
        settings:
          distance: 0.05
` + fixedAmount
	for name, tc := range map[string]struct {
		day6  [4]string
		trade string
	}{
		// After day 5's close the stop is 10.70 × 0.95 = 10.165, so 10.17.
		"hit intraday": {[4]string{"10.70", "10.75", "10.10", "10.25"}, "2025-03-09 sell AAA 9 at 10.17"},
		"gap below":    {[4]string{"10.00", "10.30", "9.95", "10.25"}, "2025-03-09 sell AAA 9 at 10.00"},
	} {
		t.Run(name, func(t *testing.T) {
			p := append([][4]string(nil), path[:7]...)
			p[6] = tc.day6
			r := run(t, Config{
				Strategy: strategyFile(t, 20, breakoutStrategy("AAA", stop)),
				Bars:     map[string][]prices.Bar{"AAA": bars(t, "AAA", p...)},
				Capital:  money.MustParseMoney("1000.00"),
			})
			wantTrades(t, r, "2025-03-08 buy AAA 9 at 10.40", tc.trade)
			if r.StopSells != 1 {
				t.Errorf("stop sells = %d, want 1", r.StopSells)
			}
		})
	}
}

func TestBuyNotReachedExpires(t *testing.T) {
	p := append([][4]string(nil), path[:6]...)
	p[5] = [4]string{"10.80", "11.00", "10.61", "10.90"} // the low touches the 10.61 limit only
	r := run(t, Config{
		Strategy: strategyFile(t, 20, breakoutStrategy("AAA", fixedAmount)),
		Bars:     map[string][]prices.Bar{"AAA": bars(t, "AAA", p...)},
		Capital:  money.MustParseMoney("1000.00"),
	})
	wantTrades(t, r)
	if r.Unfilled != 1 {
		t.Errorf("unfilled = %d, want 1", r.Unfilled)
	}
}

func TestCashOnly(t *testing.T) {
	big := strings.Replace(fixedAmount, `"100.00"`, `"600.00"`, 1) // more than the 500.00 capital
	r := run(t, Config{
		Strategy: strategyFile(t, 20, breakoutStrategy("AAA", big)),
		Bars:     map[string][]prices.Bar{"AAA": bars(t, "AAA", path...)},
		Capital:  money.MustParseMoney("1000.00"),
	})
	wantTrades(t, r)
	if r.BlockedCash != 1 {
		t.Errorf("blocked for cash = %d, want 1", r.BlockedCash)
	}
}

func TestMaxHoldings(t *testing.T) {
	r := run(t, Config{
		Strategy: strategyFile(t, 1, breakoutStrategy("BBB, AAA", fixedAmount)),
		Bars:     map[string][]prices.Bar{"AAA": bars(t, "AAA", path...), "BBB": bars(t, "BBB", path...)},
		Capital:  money.MustParseMoney("1000.00"),
	})
	// Buys are taken in symbol order, so AAA is bought and BBB blocked.
	wantTrades(t, r, "2025-03-08 buy AAA 9 at 10.40", "2025-03-11 sell AAA 9 at 9.95")
	if r.BlockedHoldings != 1 {
		t.Errorf("blocked for holdings = %d, want 1", r.BlockedHoldings)
	}
}

func TestNoSizingRuleMakesNoBuys(t *testing.T) {
	r := run(t, Config{
		Strategy: strategyFile(t, 20, breakoutStrategy("AAA", "")),
		Bars:     map[string][]prices.Bar{"AAA": bars(t, "AAA", path...)},
		Capital:  money.MustParseMoney("1000.00"),
	})
	wantTrades(t, r)
	if r.NoSizing != 1 {
		t.Errorf("no sizing = %d, want 1", r.NoSizing)
	}
}

func TestRefuses(t *testing.T) {
	s := strategyFile(t, 20, breakoutStrategy("AAA", fixedAmount))
	good := map[string][]prices.Bar{"AAA": bars(t, "AAA", path...)}
	for name, cfg := range map[string]Config{
		"no capital":   {Strategy: s, Bars: good},
		"no strategy":  {Bars: good, Capital: money.MustParseMoney("1.00")},
		"no bars":      {Strategy: s, Bars: map[string][]prices.Bar{}, Capital: money.MustParseMoney("1.00")},
		"empty range":  {Strategy: s, Bars: good, Capital: money.MustParseMoney("1.00"), From: dateN(100)},
		"out of order": {Strategy: s, Bars: map[string][]prices.Bar{"AAA": {good["AAA"][1], good["AAA"][0]}}, Capital: money.MustParseMoney("1.00")},
	} {
		if _, err := Run(cfg); !errors.Is(err, ErrConfig) {
			t.Errorf("%s: err = %v, want ErrConfig", name, err)
		}
	}
}

// synthetic makes n made-up bars for a stock from a simple integer sequence,
// so a long test needs no data file.
func synthetic(sym string, seed, n int) []prices.Bar {
	out := make([]prices.Bar, n)
	c := int64(5000 + seed*700)
	x := int64(seed*7919 + 17)
	for i := range n {
		x = (x*1103515245 + 12345) % 2147483648
		step := x%201 - 96 // a slight upward drift, in cents
		open := c + (x/201)%41 - 20
		c += step
		if c < 500 {
			c = 500
		}
		hi, lo := max(open, c)+(x/8241)%30, min(open, c)-(x/24723)%30
		p := func(v int64) money.Price {
			q, err := money.PriceFromCents(v)
			if err != nil {
				panic(err)
			}
			return q
		}
		out[i] = prices.Bar{Symbol: sym, Date: dateN(i), Open: p(open), High: p(hi), Low: p(lo), Close: p(c)}
	}
	return out
}

const mixed = `  - name: test-trend
    portfolio_share: 0.40
    stocks: [AAA, BBB]
    rules:
      - type: moving_average_cross
        settings:
          fast_days: 5
          slow_days: 20
      - type: trailing_stop
        settings:
          distance: 0.08
      - type: size_by_risk
        settings:
          risk_per_trade: 0.02
  - name: test-momentum
    portfolio_share: 0.30
    stocks: [AAA, BBB, CCC]
    rules:
      - type: momentum_ranking
        settings:
          days: 10
          top: 1
          exit_rank: 2
      - type: breakout
        settings:
          days: 15
          exit_days: 10
      - type: fixed_stop
        settings:
          distance: 0.06
      - type: size_fixed_amount
        settings:
          amount: "250.00"
  - name: test-pullback
    portfolio_share: 0.30
    stocks: [CCC]
    rules:
      - type: pullback
        settings:
          trend_days: 30
          high_days: 10
          pullback: 0.03
      - type: size_fixed_amount
        settings:
          amount: "300.00"
`

func mixedBars(n int) map[string][]prices.Bar {
	return map[string][]prices.Bar{"AAA": synthetic("AAA", 1, n), "BBB": synthetic("BBB", 2, n), "CCC": synthetic("CCC", 3, n)}
}

func report(t *testing.T, cfg Config) string {
	t.Helper()
	rep, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := rep.Write(&b, true); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestSameInputsGiveTheSameReport(t *testing.T) {
	s := strategyFile(t, 20, mixed)
	first := report(t, Config{Strategy: s, Bars: mixedBars(250), Capital: money.MustParseMoney("10000.00")})
	for range 3 {
		if again := report(t, Config{Strategy: s, Bars: mixedBars(250), Capital: money.MustParseMoney("10000.00")}); again != first {
			t.Fatalf("a second run differs:\n%s\nfirst:\n%s", again, first)
		}
	}
	// The made-up prices must actually trade, or the test proves little.
	for _, name := range []string{"test-trend", "test-momentum", "test-pullback"} {
		section := first[strings.Index(first, "\n"+name+"\n"):]
		if strings.Contains(section[:strings.Index(section, "Blocked")], "0 buys and") {
			t.Errorf("%s made no trades:\n%s", name, first)
		}
	}
	if strings.Contains(first, "e+") || strings.Contains(first, "e-") {
		t.Errorf("a number is in exponent form:\n%s", first)
	}
}

func TestNoLookAhead(t *testing.T) {
	// Replaying the first 120 days must give the same report whether or not
	// the later bars exist.
	s := strategyFile(t, 20, mixed)
	cut := mixedBars(120)
	later := mixedBars(250)
	a := report(t, Config{Strategy: s, Bars: cut, Capital: money.MustParseMoney("10000.00")})
	b := report(t, Config{Strategy: s, Bars: later, Capital: money.MustParseMoney("10000.00"), To: dateN(119)})
	if a != b {
		t.Fatalf("later bars changed earlier days:\n%s\nwith later bars:\n%s", a, b)
	}
}

func TestFromKeepsEarlierBarsAsHistory(t *testing.T) {
	s := strategyFile(t, 20, mixed)
	r, err := Run(Config{Strategy: s, Bars: mixedBars(250), Capital: money.MustParseMoney("10000.00"), From: dateN(100)})
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range r.Results {
		if !res.First.Equal(dateN(100)) || res.Days != 150 {
			t.Errorf("%s: first %s, %d days", res.Name, date(res.First), res.Days)
		}
		for _, tr := range res.Trades {
			if tr.Date.Before(dateN(101)) {
				t.Errorf("%s: a trade on %s, before the first possible fill", res.Name, date(tr.Date))
			}
		}
	}
}
