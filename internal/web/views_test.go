package web

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every symbol, amount and job name below is made up.

// page fetches a view and returns its body, failing on anything but 200.
func (f *fixture) page(path string) string {
	f.t.Helper()
	w := f.do(req{method: http.MethodGet, path: path})
	if w.Code != http.StatusOK {
		f.t.Fatalf("GET %s = %d: %s", path, w.Code, w.Body.String())
	}
	return w.Body.String()
}

func wantIn(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("the page is missing %q", w)
		}
	}
}

func wantNotIn(t *testing.T, body string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(body, w) {
			t.Errorf("the page has %q", w)
		}
	}
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// recordJob writes a job_runs row as the scheduler would. An empty finished
// leaves the run running.
func (f *fixture) recordJob(name, date, outcome, started, finished, detail string) {
	f.t.Helper()
	var fin any
	if finished != "" {
		fin = store.FormatTime(at(f.t, finished))
	}
	if _, err := f.store.Writer().ExecContext(context.Background(),
		`INSERT INTO job_runs (job_name, trading_date, input_hash, started_at, finished_at, outcome, detail)
		 VALUES (?, ?, 'hash', ?, ?, ?, ?)`,
		name, date, store.FormatTime(at(f.t, started)), fin, outcome, detail); err != nil {
		f.t.Fatal(err)
	}
}

// priceCheck is one stock in a recorded price check.
type priceCheck struct {
	symbol  string
	held    bool
	outcome string // agreed, disagreed or missing
	close   string // the primary source's close; "" stores no bar
}

// recordPrices writes a trading date's bars and cross-check the way the
// price fetcher stores them. The day is usable unless a stock is missing.
func (f *fixture) recordPrices(date string, checks ...priceCheck) {
	f.t.Helper()
	ctx := context.Background()
	tx, err := f.store.Writer().BeginTx(ctx, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	usable, detail := 1, ""
	for _, c := range checks {
		if c.outcome == "missing" {
			usable, detail = 0, "a source gave no bar"
		}
	}
	now := store.FormatTime(at(f.t, startOfTest))
	exec := func(q string, args ...any) {
		f.t.Helper()
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			f.t.Fatal(err)
		}
	}
	exec(`INSERT INTO price_days (trading_date, primary_source, check_source, tolerance, usable, detail, checked_at)
	      VALUES (?, 'primary', 'check', '0.01', ?, ?, ?)`, date, usable, detail, now)
	for _, c := range checks {
		if c.close != "" {
			for _, src := range []string{"primary", "check"} {
				exec(`INSERT INTO price_bars (symbol, trading_date, source, open, high, low, close, volume, fetched_at)
				      VALUES (?, ?, ?, ?, ?, ?, ?, 1000, ?)`, c.symbol, date, src, c.close, c.close, c.close, c.close, now)
			}
		}
		halt, d := 0, ""
		if c.outcome != "agreed" {
			d = "the sources differ by 5%"
		}
		if c.held && c.outcome == "disagreed" {
			halt = 1
		}
		held := 0
		if c.held {
			held = 1
		}
		exec(`INSERT INTO price_checks (trading_date, symbol, held, outcome, halt, detail) VALUES (?, ?, ?, ?, ?, ?)`,
			date, c.symbol, held, c.outcome, halt, d)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
}

func holding(t *testing.T, symbol, quantity, averageCost string) holdings.Holding {
	t.Helper()
	q, err := money.ParseQuantity(quantity)
	if err != nil {
		t.Fatal(err)
	}
	c, err := money.ParseMoney(averageCost)
	if err != nil {
		t.Fatal(err)
	}
	return holdings.Holding{Symbol: symbol, Quantity: q, AverageCost: c,
		Opened: time.Date(2026, 3, 2, 0, 0, 0, 0, clock.Eastern)}
}

// recordHoldings writes an imported set and its cash.
func (f *fixture) recordHoldings(asOf, cash string, hs ...holdings.Holding) {
	f.t.Helper()
	if _, _, err := holdings.Record(context.Background(), f.store, holdings.Snapshot{
		Source: holdings.SourceImport, AsOf: at(f.t, asOf), Holdings: hs, Cash: money.MustParseMoney(cash),
	}); err != nil {
		f.t.Fatal(err)
	}
}

// --- Today ---

func TestTodayBannerSaysTheModeInWords(t *testing.T) {
	paper := newFixtureMode(t, config.ModePaper).page("/")
	wantIn(t, paper,
		`<section class="mode-banner mode-banner--paper" aria-label="Trading mode">`,
		`<span class="mode-tag">Paper</span>`,
		"Paper trading: no real money moves.")
	wantNotIn(t, paper, "Live trading", "mode-banner--live")

	live := newFixtureMode(t, config.ModeLive).page("/")
	wantIn(t, live,
		`<section class="mode-banner mode-banner--live" aria-label="Trading mode">`,
		`<span class="mode-tag">Live</span>`,
		"Live trading: orders you approve are placed with the broker and use real money.")
	wantNotIn(t, live, "Paper trading", "mode-banner--paper")
}

func TestTodayWithNothingRecorded(t *testing.T) {
	body := newFixture(t).page("/")
	wantIn(t, body,
		"Nothing is halted.",
		"Prices have not been checked yet.",
		`<h2 class="card-title" id="stop-switch">Stop switch</h2>`,
		`<span class="badge badge--passed">Off</span>`,
		`<form class="card-body stack" method="post" action="/stop-switch/on">`,
		`<button type="submit" class="button button--destructive button--full">Stop all trading</button>`,
		"No job has run yet.")
	wantNotIn(t, body, "notice--halt", "/stop-switch/reset", "Last reset", `role="status"`)
}

func TestTodayShowsTheLastRunOfEachJob(t *testing.T) {
	f := newFixture(t)
	f.recordJob("close-job", "2026-11-12", "succeeded", "2026-11-12T21:30:00Z", "2026-11-12T21:31:00Z", "an older run")
	f.recordJob("close-job", "2026-11-13", "failed", "2026-11-13T21:30:00Z", "2026-11-13T21:30:05Z", "the price source timed out")
	f.recordJob("morning-job", "2026-11-16", "running", "2026-11-16T14:00:00Z", "", "")
	f.recordJob("weekly-job", "2026-11-16", "skipped", "2026-11-16T14:05:00Z", "2026-11-16T14:05:00Z", "the market is closed")
	body := f.page("/")
	wantIn(t, body,
		// close-job's newest run, with its outcome in words and the times in
		// Eastern.
		"close-job<span class=\"row-meta\">For Fri 13 Nov 2026. Started Fri 13 Nov 2026, 4:30 pm ET, finished Fri 13 Nov 2026, 4:30 pm ET.</span>",
		"the price source timed out",
		`<span class="badge badge--blocked">Failed</span>`,
		// A run still going has no finish.
		"morning-job<span class=\"row-meta\">For Mon 16 Nov 2026. Started Mon 16 Nov 2026, 9:00 am ET.</span>",
		`<span class="badge badge--hold">Running now</span>`,
		`<span class="badge badge--hold">Skipped</span>`,
		"the market is closed")
	wantNotIn(t, body, "an older run", "Thu 12 Nov", "No job has run yet.")
	if i, j := strings.Index(body, "close-job"), strings.Index(body, "weekly-job"); i < 0 || j < i {
		t.Error("the jobs are not in name order")
	}
}

func TestTodayShowsAHaltAndItsCause(t *testing.T) {
	f := newFixture(t)
	// A disagreement in the price record is not itself the halt: the
	// after-close job throws the switch for it, with the evidence.
	f.recordPrices("2026-11-13",
		priceCheck{symbol: "AAAA", held: true, outcome: "agreed", close: "10.00"},
		priceCheck{symbol: "BBBB", held: true, outcome: "disagreed", close: "20.00"})
	body := f.page("/")
	wantIn(t, body, "Nothing is halted.", "Prices were last checked for Fri 13 Nov 2026.")
	wantNotIn(t, body, "notice--halt", "BBBB")

	if _, _, err := f.stop.Throw(context.Background(), halt.PriceDisagreement,
		"Price check for 2026-11-13: the sources disagree on BBBB, a stock you hold (the sources differ by 5%)."); err != nil {
		t.Fatal(err)
	}
	body = f.page("/")
	wantIn(t, body,
		`<section class="notice notice--halt" aria-label="Halt">`,
		"Trading is halted.",
		"Rules still run and record their signals",
		`<span class="row-label">The price sources disagree on a stock you hold<span class="row-meta">Mon 16 Nov 2026, 10:00 am ET</span>`,
		"the sources disagree on BBBB, a stock you hold (the sources differ by 5%).",
		`<span class="badge badge--blocked">On: trading halted</span>`,
		`<form class="card-body stack" method="post" action="/stop-switch/reset">`,
		`<input type="hidden" name="through" value="1">`,
		`<textarea class="field-input" id="reset-reason" name="reason" rows="3" maxlength="500" required></textarea>`)
	wantNotIn(t, body, "Nothing is halted.", "/stop-switch/on", "Stop all trading</button>")
}

// --- Portfolio ---

func TestPortfolioBeforeAnyImport(t *testing.T) {
	body := newFixture(t).page("/portfolio")
	wantIn(t, body, "Holdings not imported yet.", "Nothing has been recorded so far.")
	wantNotIn(t, body, "Summary", "Over the limit")
}

func TestPortfolioValuesHoldingsFromStoredCloses(t *testing.T) {
	f := newFixture(t)
	f.recordHoldings("2026-11-16T14:00:00Z", "2500.00",
		holding(t, "AAAA", "10", "12.50"),
		holding(t, "BBBB", "0.333333", "300.03"),
		holding(t, "CCCC", "1234", "1000.005"))
	f.recordPrices("2026-11-12",
		priceCheck{symbol: "AAAA", held: true, outcome: "agreed", close: "14.00"},
		priceCheck{symbol: "BBBB", held: true, outcome: "agreed", close: "300.03"},
		priceCheck{symbol: "CCCC", held: true, outcome: "agreed", close: "900.00"})
	f.recordPrices("2026-11-13",
		priceCheck{symbol: "AAAA", held: true, outcome: "agreed", close: "15.00"},
		priceCheck{symbol: "BBBB", held: true, outcome: "agreed", close: "300.03"},
		priceCheck{symbol: "CCCC", held: true, outcome: "agreed", close: "900.00"})
	body := f.page("/portfolio")
	wantIn(t, body,
		`<span class="badge">3 holdings</span>`,
		"As of Mon 16 Nov 2026, 9:00 am ET, imported from the broker.",
		// Totals: exact sums, rounded to the cent only for display.
		`<span class="row-label">Invested</span><span class="row-value">$1,234,231.18</span>`,
		`<span class="row-label">Value at the latest close</span><span class="row-value">$1,110,850.01</span>`,
		`<span class="delta delta--down">−$123,381.17, down 10.0%</span>`,
		`<span class="row-value">$2,500.00</span>`,
		`<span class="row-label">Total with cash</span><span class="row-value">$1,113,350.01</span>`,
		// AAAA uses the newest close, not the older one.
		`<span class="delta delta--up">&#43;$25.00, up 20.0%</span>`,
		`Close of Fri 13 Nov 2026.</span></span><span class="row-value">$15.00</span>`,
		`<span class="row-label">Value</span><span class="row-value">$150.00</span>`,
		// A fractional quantity, valued exactly: 0.333333 × 300.03.
		`<span class="row-label">Shares</span><span class="row-value">0.333333</span>`,
		`<span class="row-label">Value</span><span class="row-value">$100.01</span>`,
		`<span class="delta delta--flat">$0.00, no change</span>`,
		// An average cost with more places than cents is shown as stored.
		`<span class="row-label">Average cost</span><span class="row-value">$1,000.005</span>`,
		`<span class="row-label">Invested</span><span class="row-value">$1,234,006.17</span>`,
		`<span class="delta delta--down">−$123,406.17, down 10.0%</span>`,
		`<span class="row-label">Held since</span><span class="row-value">Mon 2 Mar 2026</span>`,
		// Room for the later parts' columns.
		`<span class="row-label">Suggestion</span><span class="row-value">Not yet available</span>`,
		`<span class="row-label">AI note</span><span class="row-value">Not yet available</span>`)
	wantNotIn(t, body, "$14.00", "Not shown", "Over the limit", "Holdings not imported yet.")
	if i, j := strings.Index(body, ">AAAA<"), strings.Index(body, ">CCCC<"); i < 0 || j < i {
		t.Error("the holdings are not in symbol order")
	}
}

func TestPortfolioSaysWhenNoCheckedCloseIsStored(t *testing.T) {
	f := newFixture(t)
	f.recordHoldings("2026-11-16T14:00:00Z", "100.00",
		holding(t, "AAAA", "10", "12.50"),
		holding(t, "DDDD", "5", "40.00"),
		holding(t, "EEEE", "2", "50.00"))
	f.recordPrices("2026-11-12",
		priceCheck{symbol: "AAAA", held: true, outcome: "agreed", close: "14.00"},
		priceCheck{symbol: "EEEE", held: true, outcome: "agreed", close: "55.00"})
	// On the next day EEEE's sources disagree, so its stored bar is not
	// usable, and a day with a missing stock is not usable at all.
	f.recordPrices("2026-11-13",
		priceCheck{symbol: "AAAA", held: true, outcome: "agreed", close: "15.00"},
		priceCheck{symbol: "EEEE", held: true, outcome: "disagreed", close: "99.00"})
	f.recordPrices("2026-11-16",
		priceCheck{symbol: "AAAA", held: true, outcome: "agreed", close: "77.00"},
		priceCheck{symbol: "DDDD", held: true, outcome: "missing"})
	body := f.page("/portfolio")
	wantIn(t, body,
		// DDDD has never had a usable close.
		"No checked closing price is stored for DDDD yet.",
		`Not shown, because 1 holding has no checked closing price stored yet and a total would be wrong.`,
		// AAAA's newest usable close is the 13th; the 16th was incomplete.
		`Close of Fri 13 Nov 2026.</span></span><span class="row-value">$15.00</span>`,
		// EEEE keeps its last agreed close, with its date.
		`Close of Thu 12 Nov 2026.</span></span><span class="row-value">$55.00</span>`,
		`<span class="delta delta--up">&#43;$10.00, up 10.0%</span>`,
		`<span class="row-value">$100.00</span>`)
	wantNotIn(t, body, "$77.00", "$99.00", "Total with cash", "Value at the latest close")
}

func TestPortfolioWithoutACashReading(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tx, err := f.store.Writer().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holdings.Replace(ctx, tx, holdings.SourceFills, at(t, "2026-11-16T14:00:00Z"),
		[]holdings.Holding{holding(t, "AAAA", "1", "10.00")}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.recordPrices("2026-11-13", priceCheck{symbol: "AAAA", held: true, outcome: "agreed", close: "11.00"})
	body := f.page("/portfolio")
	wantIn(t, body,
		`<span class="badge">1 holding</span>`,
		"worked out from sherwood&#39;s own fills",
		"No cash reading has been recorded yet.",
		`<span class="row-label">Value at the latest close</span><span class="row-value">$11.00</span>`)
	wantNotIn(t, body, "Total with cash")
}

func TestPortfolioWithAnEmptyRecordedSet(t *testing.T) {
	f := newFixture(t)
	f.recordHoldings("2026-11-16T14:00:00Z", "-5.25")
	body := f.page("/portfolio")
	wantIn(t, body, "No stocks held.", `<span class="badge">0 holdings</span>`,
		`<span class="row-value">−$5.25</span>`, `<span class="row-value">−$5.25</span></li>`)
	wantNotIn(t, body, "Holdings not imported yet.")
}

func TestPortfolioWarnsInWordsWhenOverTheLimit(t *testing.T) {
	f := newFixture(t)
	var hs []holdings.Holding
	for i := range holdings.Limit + 1 {
		hs = append(hs, holding(t, fmt.Sprintf("S%02d", i), "1", "1.00"))
	}
	f.recordHoldings("2026-11-16T14:00:00Z", "0.00", hs...)
	body := f.page("/portfolio")
	wantIn(t, body,
		`<section class="notice notice--warning" aria-label="Over the limit">`,
		"Over the limit of 20 holdings.",
		"The current holdings list 21 stocks, more than the limit of 20. All 21 are shown below.")
	for _, h := range hs {
		wantIn(t, body, `<h2 class="card-title">`+h.Symbol+`</h2>`)
	}

	// At the limit there is no warning.
	g := newFixture(t)
	g.recordHoldings("2026-11-16T14:00:00Z", "0.00", hs[:holdings.Limit]...)
	wantNotIn(t, g.page("/portfolio"), "Over the limit")
}

// --- Formatting ---

func TestMoneyFormatting(t *testing.T) {
	for in, want := range map[string]string{
		"0":           "$0.00",
		"5":           "$5.00",
		"999.99":      "$999.99",
		"1000":        "$1,000.00",
		"-1234567.5":  "−$1,234,567.50",
		"12345.6789":  "$12,345.6789",
		"-0.004":      "−$0.004",
		"100000000.1": "$100,000,000.10",
	} {
		if got := dollars(money.MustParseMoney(in)); got != want {
			t.Errorf("dollars(%s) = %q, want %q", in, got, want)
		}
	}
	if got := cents(money.MustParseMoney("12345.675")); got != "$12,345.68" {
		t.Errorf("cents rounds to %q", got)
	}
	if got := cents(money.MustParseMoney("-0.004")); got != "$0.00" {
		t.Errorf("a negative amount that rounds to zero shows as %q", got)
	}
	for _, c := range []struct{ value, cost, text, class string }{
		{"110", "100", "+$10.00, up 10.0%", "delta--up"},
		{"90", "100", "−$10.00, down 10.0%", "delta--down"},
		{"100.004", "100", "$0.00, no change", "delta--flat"},
		{"5", "0", "+$5.00, up", "delta--up"},
		{"100.1", "300", "−$199.90, down 66.6%", "delta--down"},
	} {
		got := gainOf(money.MustParseMoney(c.value), money.MustParseMoney(c.cost))
		if got.Text != c.text || got.Class != c.class {
			t.Errorf("gainOf(%s, %s) = %+v, want %q %q", c.value, c.cost, got, c.text, c.class)
		}
	}
}

// TestTheViewsUseNoFloats checks that nothing between the store and the page
// converts money to a float: no float types, and no float conversions.
func TestTheViewsUseNoFloats(t *testing.T) {
	banned := map[string]bool{
		"float32": true, "float64": true,
		"ParseFloat": true, "FormatFloat": true, "AppendFloat": true,
		"Float64": true, "InexactFloat64": true, "NewFromFloat": true, "NewFromFloat32": true,
	}
	fset := token.NewFileSet()
	for _, name := range []string{"format.go", "today.go", "portfolio.go"} {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && banned[id.Name] {
				t.Errorf("%s uses %s", fset.Position(id.Pos()), id.Name)
			}
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.FLOAT {
				t.Errorf("%s has a float literal %s", fset.Position(lit.Pos()), lit.Value)
			}
			return true
		})
	}
}
