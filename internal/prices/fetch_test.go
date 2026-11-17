package prices_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/prices/pricestest"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every price below is made up. The trading date is Wednesday 7 January 2026;
// 1 January is a holiday.

var tolerance = config.MustDecimal("0.02")

type harness struct {
	f       *prices.Fetcher
	primary *pricestest.Source
	check   *pricestest.Source
	clock   *clock.Fake
	store   *store.Store
	logs    *loggingtest.Output
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	now := at(t, "2026-01-07", 17, 0)
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	log, out := loggingtest.New(t)
	h := &harness{
		primary: pricestest.New("primary"),
		check:   pricestest.New("cross"),
		clock:   clock.NewFake(now),
		store:   s,
		logs:    out,
	}
	h.f = &prices.Fetcher{Primary: h.primary, Check: h.check, Clock: h.clock, Calendar: clock.NYSE(), Store: s, Log: log}
	return h
}

func at(t *testing.T, date string, hour, minute int) time.Time {
	t.Helper()
	d, err := time.ParseInLocation(clock.DateLayout, date, clock.Eastern)
	if err != nil {
		t.Fatal(err)
	}
	return d.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func (h *harness) both(bars ...prices.Bar) {
	h.primary.Add(bars...)
	h.check.Add(bars...)
}

func (h *harness) usable(t *testing.T, date string) []string {
	t.Helper()
	bars, err := prices.UsableBars(context.Background(), h.store.Reader(), at(t, date, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range bars {
		out = append(out, b.Symbol+" "+b.Close.String())
	}
	return out
}

func checkOf(t *testing.T, d *prices.Day, symbol string) prices.Check {
	t.Helper()
	for _, c := range d.Checks {
		if c.Symbol == symbol {
			return c
		}
	}
	t.Fatalf("no check for %s", symbol)
	return prices.Check{}
}

var (
	aapl = pricestest.Bar("AAPL", "2026-01-07", "100.00", "104.00", "99.00", "103.00", 1000)
	msft = pricestest.Bar("MSFT", "2026-01-07", "200.00", "206.00", "198.00", "205.00", 2000)
	brk  = pricestest.Bar("BRK.B", "2026-01-07", "300.00", "305.00", "295.00", "301.00", 3000)
)

func TestAllAgreeIsUsable(t *testing.T) {
	h := newHarness(t)
	h.both(aapl, msft)
	// The cross-check may differ by up to the tolerance: 2% of 205.00 is 4.10.
	h.check.Add(pricestest.Bar("MSFT", "2026-01-07", "200.00", "210.00", "198.00", "209.10", 2500))
	d, err := h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0),
		prices.Stocks{Held: []string{"MSFT"}, Watched: []string{"AAPL", "MSFT"}}, tolerance)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Usable || d.Halt() || d.Detail != "" {
		t.Fatalf("day = %+v", d)
	}
	if d.Excluded("AAPL") || d.Excluded("MSFT") || !d.Excluded("BRK.B") {
		t.Error("only the checked, agreeing stocks may be included")
	}
	if !checkOf(t, d, "MSFT").Held || checkOf(t, d, "AAPL").Held {
		t.Error("held flags are wrong")
	}
	// The rules get the primary source's bars.
	if got := h.usable(t, "2026-01-07"); !slices.Equal(got, []string{"AAPL 103.00", "MSFT 205.00"}) {
		t.Errorf("usable = %v", got)
	}
	loaded, err := prices.LoadDay(context.Background(), h.store.Reader(), at(t, "2026-01-07", 12, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Usable || loaded.PrimarySource != "primary" || loaded.CheckSource != "cross" || loaded.Tolerance != "0.02" ||
		!slices.Equal(loaded.Checks, d.Checks) {
		t.Errorf("loaded = %+v, want %+v", loaded, d)
	}
}

func TestMissingDayStoresNothingUsable(t *testing.T) {
	h := newHarness(t)
	h.both(aapl)
	h.primary.Add(msft) // the cross-check has no MSFT bar for the day
	d, err := h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0),
		prices.Stocks{Watched: []string{"AAPL", "MSFT"}}, tolerance)
	if err != nil {
		t.Fatal(err)
	}
	if d.Usable || !strings.Contains(d.Detail, "MSFT") {
		t.Fatalf("day = %+v, want not usable naming MSFT", d)
	}
	if c := checkOf(t, d, "MSFT"); c.Outcome != prices.Missing || !strings.Contains(c.Detail, "cross") {
		t.Errorf("MSFT = %+v", c)
	}
	// AAPL agreed, but a missing day gives nobody usable bars.
	if c := checkOf(t, d, "AAPL"); c.Outcome != prices.Agreed {
		t.Errorf("AAPL = %+v", c)
	}
	if !d.Excluded("AAPL") {
		t.Error("AAPL must be excluded on a missing day")
	}
	if got := h.usable(t, "2026-01-07"); len(got) != 0 {
		t.Errorf("usable on a missing day = %v", got)
	}
	// The bars that did arrive are kept for the record.
	hist, err := prices.History(context.Background(), h.store.Reader(), "primary", "AAPL", at(t, "2026-01-01", 0, 0), at(t, "2026-01-07", 0, 0))
	if err != nil || len(hist) != 1 {
		t.Errorf("history = %v, %v", hist, err)
	}
}

func TestNoBarsAtAllIsMissing(t *testing.T) {
	h := newHarness(t)
	// Neither source has published the day yet; both have the day before.
	h.both(pricestest.Bar("AAPL", "2026-01-06", "100.00", "101.00", "99.00", "100.50", 10))
	d, err := h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0), prices.Stocks{Held: []string{"AAPL"}}, tolerance)
	if err != nil {
		t.Fatal(err)
	}
	if d.Usable || d.Halt() || checkOf(t, d, "AAPL").Outcome != prices.Missing {
		t.Errorf("day = %+v", d)
	}
	if got := h.usable(t, "2026-01-07"); len(got) != 0 {
		t.Errorf("usable = %v", got)
	}
}

func TestSourceErrorIsMissing(t *testing.T) {
	h := newHarness(t)
	h.both(aapl, msft)
	h.primary.Fail("MSFT", errors.New("made-up outage"))
	d, err := h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0),
		prices.Stocks{Watched: []string{"AAPL", "MSFT"}}, tolerance)
	if err != nil {
		t.Fatal(err)
	}
	if d.Usable || !strings.Contains(checkOf(t, d, "MSFT").Detail, "made-up outage") {
		t.Errorf("day = %+v", d)
	}
}

func TestBadBarIsMissing(t *testing.T) {
	tests := []struct {
		name string
		bar  prices.Bar
	}{
		{"high below low", pricestest.Bar("AAPL", "2026-01-07", "100.00", "98.00", "99.00", "99.00", 1)},
		{"close above high", pricestest.Bar("AAPL", "2026-01-07", "100.00", "101.00", "99.00", "102.00", 1)},
		{"another stock", pricestest.Bar("MSFT", "2026-01-07", "100.00", "101.00", "99.00", "100.00", 1)},
		{"negative volume", pricestest.Bar("AAPL", "2026-01-07", "100.00", "101.00", "99.00", "100.00", -1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.check.Add(aapl)
			h.primary = pricestest.New("primary")
			h.f.Primary = &renaming{Source: h.primary, symbol: "AAPL"}
			h.primary.Add(tt.bar)
			d, err := h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0), prices.Stocks{Watched: []string{"AAPL"}}, tolerance)
			if err != nil {
				t.Fatal(err)
			}
			if d.Usable || checkOf(t, d, "AAPL").Outcome != prices.Missing {
				t.Errorf("day = %+v", d)
			}
		})
	}
}

// renaming asks its fake for bars of a fixed symbol, so a test can make a
// source answer with a bar for the wrong stock.
type renaming struct {
	*pricestest.Source
	symbol string
}

func (r *renaming) DailyBars(ctx context.Context, _ string, from, to time.Time) ([]prices.Bar, error) {
	bars, err := r.Source.DailyBars(ctx, r.symbol, from, to)
	if err != nil {
		bars, err = r.Source.DailyBars(ctx, "MSFT", from, to)
	}
	return bars, err
}

func TestBarOnClosedDayIsMissing(t *testing.T) {
	h := newHarness(t)
	h.f.HistoryDays = 10
	h.both(aapl)
	// 1 January 2026 is New Year's Day; no source should have a bar for it.
	h.primary.Add(pricestest.Bar("AAPL", "2026-01-01", "100.00", "101.00", "99.00", "100.00", 1))
	d, err := h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0), prices.Stocks{Watched: []string{"AAPL"}}, tolerance)
	if err != nil {
		t.Fatal(err)
	}
	if d.Usable {
		t.Errorf("day = %+v", d)
	}
}

func TestDisagreeingStockExcludedAndHeldFlagged(t *testing.T) {
	h := newHarness(t)
	h.both(aapl, msft, brk)
	// MSFT is held and its cross-check close is 2.5% higher; BRK.B is only
	// watched and its cross-check low is far off.
	h.check.Add(pricestest.Bar("MSFT", "2026-01-07", "200.00", "210.20", "198.00", "210.13", 2000))
	h.check.Add(pricestest.Bar("BRK.B", "2026-01-07", "300.00", "305.00", "250.00", "301.00", 3000))
	d, err := h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0),
		prices.Stocks{Held: []string{"MSFT", "AAPL"}, Watched: []string{"BRK.B"}}, tolerance)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Usable {
		t.Fatalf("a disagreement must not make the day missing: %+v", d)
	}
	ms := checkOf(t, d, "MSFT")
	if ms.Outcome != prices.Disagreed || !ms.Halt || !strings.Contains(ms.Detail, "close primary 205.00, cross 210.13") {
		t.Errorf("MSFT = %+v", ms)
	}
	bk := checkOf(t, d, "BRK.B")
	if bk.Outcome != prices.Disagreed || bk.Halt || !strings.Contains(bk.Detail, "low") {
		t.Errorf("BRK.B = %+v", bk)
	}
	if !d.Halt() || !slices.Equal(d.HaltSymbols(), []string{"MSFT"}) {
		t.Errorf("halt symbols = %v", d.HaltSymbols())
	}
	if !d.Excluded("MSFT") || !d.Excluded("BRK.B") || d.Excluded("AAPL") {
		t.Error("only the disagreeing stocks should be excluded")
	}
	if got := h.usable(t, "2026-01-07"); !slices.Equal(got, []string{"AAPL 103.00"}) {
		t.Errorf("usable = %v", got)
	}
	loaded, err := prices.LoadDay(context.Background(), h.store.Reader(), at(t, "2026-01-07", 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Halt() || !slices.Equal(loaded.HaltSymbols(), []string{"MSFT"}) {
		t.Errorf("stored halt flag lost: %+v", loaded)
	}
	if lines, err := h.logs.Lines(); err != nil || !strings.Contains(h.logs.String(), "price sources disagree") || len(lines) == 0 {
		t.Errorf("log = %s (%v)", h.logs.String(), err)
	}
}

func TestDisagreementIsAShareOfThePrimary(t *testing.T) {
	// 2% of 100.00 is 2.00: 102.00 agrees, 102.01 does not.
	for _, tt := range []struct {
		closing string
		want    prices.Outcome
	}{{"102.00", prices.Agreed}, {"98.00", prices.Agreed}, {"102.01", prices.Disagreed}, {"97.99", prices.Disagreed}} {
		h := newHarness(t)
		h.primary.Add(pricestest.Bar("AAPL", "2026-01-07", "100.00", "110.00", "90.00", "100.00", 1))
		h.check.Add(pricestest.Bar("AAPL", "2026-01-07", "100.00", "110.00", "90.00", tt.closing, 1))
		d, err := h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0), prices.Stocks{Watched: []string{"AAPL"}}, tolerance)
		if err != nil {
			t.Fatal(err)
		}
		if got := checkOf(t, d, "AAPL").Outcome; got != tt.want {
			t.Errorf("close %s: %s, want %s", tt.closing, got, tt.want)
		}
	}
}

func TestRefusesDatesThatAreNotClosedSessions(t *testing.T) {
	h := newHarness(t)
	h.both(aapl)
	stocks := prices.Stocks{Watched: []string{"AAPL"}}
	// Before the close on the day itself.
	h.clock.Set(at(t, "2026-01-07", 15, 59))
	if _, err := h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0), stocks, tolerance); !errors.Is(err, prices.ErrTooEarly) {
		t.Errorf("before the close: err = %v", err)
	}
	h.clock.Set(at(t, "2026-01-07", 17, 0))
	if _, err := h.f.FetchDay(context.Background(), at(t, "2026-01-01", 0, 0), stocks, tolerance); !errors.Is(err, clock.ErrClosed) {
		t.Errorf("holiday: err = %v", err)
	}
	if _, err := h.f.FetchDay(context.Background(), at(t, "2026-01-08", 0, 0), stocks, tolerance); !errors.Is(err, prices.ErrTooEarly) {
		t.Errorf("tomorrow: err = %v", err)
	}
	if _, err := h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0), prices.Stocks{Watched: []string{"aapl"}}, tolerance); err == nil {
		t.Error("a malformed symbol should be refused")
	}
	if _, err := h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0), stocks, config.Decimal{}); err == nil {
		t.Error("an unset tolerance should be refused")
	}
	for _, date := range []string{"2026-01-01", "2026-01-07", "2026-01-08"} {
		if _, err := prices.LoadDay(context.Background(), h.store.Reader(), at(t, date, 0, 0)); !errors.Is(err, prices.ErrNotChecked) {
			t.Errorf("%s recorded after a refusal: %v", date, err)
		}
	}
	if len(h.primary.Calls()) != 0 {
		t.Error("a refused date must not reach a source")
	}
}

func TestRerunReplacesTheDay(t *testing.T) {
	h := newHarness(t)
	h.primary.Add(aapl)
	stocks := prices.Stocks{Held: []string{"AAPL"}}
	d, err := h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0), stocks, tolerance)
	if err != nil || d.Usable {
		t.Fatalf("first run: %+v, %v", d, err)
	}
	h.check.Add(aapl)
	h.clock.Advance(time.Hour)
	d, err = h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0), stocks, tolerance)
	if err != nil || !d.Usable {
		t.Fatalf("second run: %+v, %v", d, err)
	}
	if got := h.usable(t, "2026-01-07"); !slices.Equal(got, []string{"AAPL 103.00"}) {
		t.Errorf("usable = %v", got)
	}
	var n int
	if err := h.store.Reader().QueryRow(`SELECT count(*) FROM price_bars`).Scan(&n); err != nil || n != 2 {
		t.Errorf("price_bars rows = %d, %v; want one per source", n, err)
	}
	if err := h.store.Reader().QueryRow(`SELECT count(*) FROM price_checks`).Scan(&n); err != nil || n != 1 {
		t.Errorf("price_checks rows = %d, %v", n, err)
	}
}

func TestHistoryWindow(t *testing.T) {
	h := newHarness(t)
	h.f.HistoryDays = 7
	h.both(aapl)
	h.primary.Add(pricestest.Bar("AAPL", "2026-01-06", "90.00", "95.00", "89.00", "94.00", 10))
	if _, err := h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0), prices.Stocks{Watched: []string{"AAPL"}}, tolerance); err != nil {
		t.Fatal(err)
	}
	calls := map[string]pricestest.Call{"primary": h.primary.Calls()[0], "cross": h.check.Calls()[0]}
	if c := calls["primary"]; c.From != "2025-12-31" || c.To != "2026-01-07" {
		t.Errorf("primary asked for %+v", c)
	}
	if c := calls["cross"]; c.From != "2026-01-07" || c.To != "2026-01-07" {
		t.Errorf("cross-check asked for %+v", c)
	}
	hist, err := prices.History(context.Background(), h.store.Reader(), "primary", "AAPL", at(t, "2026-01-01", 0, 0), at(t, "2026-01-07", 0, 0))
	if err != nil || len(hist) != 2 || hist[0].DateString() != "2026-01-06" {
		t.Errorf("history = %+v, %v", hist, err)
	}
	// History bars of other dates are never usable on their own date until
	// that date is checked.
	if got := h.usable(t, "2026-01-06"); len(got) != 0 {
		t.Errorf("usable on an unchecked date = %v", got)
	}
}

func TestSameSourceTwiceRefused(t *testing.T) {
	h := newHarness(t)
	h.f.Check = pricestest.New("primary")
	if _, err := h.f.FetchDay(context.Background(), at(t, "2026-01-07", 0, 0), prices.Stocks{Watched: []string{"AAPL"}}, tolerance); err == nil {
		t.Error("the same source twice cannot cross-check itself")
	}
}

func TestCanceledContextRecordsNothing(t *testing.T) {
	h := newHarness(t)
	h.both(aapl)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.f.FetchDay(ctx, at(t, "2026-01-07", 0, 0), prices.Stocks{Watched: []string{"AAPL"}}, tolerance); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	if _, err := prices.LoadDay(context.Background(), h.store.Reader(), at(t, "2026-01-07", 0, 0)); !errors.Is(err, prices.ErrNotChecked) {
		t.Errorf("recorded after cancel: %v", err)
	}
}
