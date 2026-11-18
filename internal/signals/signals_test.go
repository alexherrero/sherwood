package signals_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/prices/pricestest"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every symbol, price and holding below is made up. The trading dates are
// Monday 5 to Friday 9 January 2026, and the run is for the Friday.

const tradingDate = "2026-01-09"

var history = []string{"2026-01-05", "2026-01-06", "2026-01-07", "2026-01-08", tradingDate}

// strategyText is the example file's limits and sectors, with sectors for
// the made-up stocks and one made-up strategy:
// a two-day breakout, a 10% fixed stop and a fixed-amount sizing rule.
func strategyText(t *testing.T, rulesText string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "strategy.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	head, _, ok := strings.Cut(string(b), "\nstrategies:\n")
	if !ok {
		t.Fatal("the example file has no strategies block")
	}
	if !strings.Contains(head, "\nsectors:\n") {
		t.Fatal("the example file has no sectors block")
	}
	head = strings.Replace(head, "\nsectors:\n", "\nsectors:\n  AAA: sector-a\n  BBB: sector-a\n  CCC: sector-b\n  DDD: sector-b\n", 1)
	return head + `
strategies:
  - name: test-breakout
    portfolio_share: 0.50
    stocks: [AAA, BBB, CCC, DDD]
    rules:
` + rulesText
}

const breakoutRules = `      - type: breakout
        settings:
          days: 2
          exit_days: 2
      - type: fixed_stop
        settings:
          distance: 0.10
      - type: size_fixed_amount
        settings:
          amount: "100.00"
`

func loadStrategy(t *testing.T, rulesText string) *config.Strategy {
	t.Helper()
	s, err := config.ParseStrategy([]byte(strategyText(t, rulesText)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func date(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation(clock.DateLayout, s, clock.Eastern)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

type harness struct {
	store   *store.Store
	clock   *clock.Fake
	primary *pricestest.Source
	check   *pricestest.Source
	runner  *signals.Runner
	logs    *loggingtest.Output
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	now := date(t, tradingDate).Add(17 * time.Hour)
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	log, out := loggingtest.New(t)
	fake := clock.NewFake(now)
	return &harness{
		store: s, clock: fake, logs: out,
		primary: pricestest.New("primary"),
		check:   pricestest.New("cross"),
		runner:  &signals.Runner{Store: s, Clock: fake, Log: log},
	}
}

// closes adds one bar per history date to both sources, with open, high, low
// and close all equal.
func (h *harness) closes(sym string, cs ...string) {
	for i, c := range cs {
		b := pricestest.Bar(sym, history[i], c, c, c, c, 1000)
		h.primary.Add(b)
		h.check.Add(b)
	}
}

// fetch checks the trading date's prices into the store.
func (h *harness) fetch(t *testing.T, held ...string) *prices.Day {
	t.Helper()
	f := &prices.Fetcher{Primary: h.primary, Check: h.check, Clock: h.clock, Calendar: clock.NYSE(),
		Store: h.store, Log: h.runner.Log, HistoryDays: 10}
	day, err := f.FetchDay(context.Background(), date(t, tradingDate),
		prices.Stocks{Held: held, Watched: []string{"AAA", "BBB", "CCC", "DDD"}}, config.MustDecimal("0.02"))
	if err != nil {
		t.Fatal(err)
	}
	return day
}

func holding(t *testing.T, sym, qty, cost, opened string) holdings.Holding {
	t.Helper()
	return holdings.Holding{Symbol: sym, Quantity: money.MustParseQuantity(qty),
		AverageCost: money.MustParseMoney(cost), Opened: date(t, opened)}
}

// standardDay sets up the trading date used by most tests:
//
//   - AAA, not held, closes above its two-day high: a buy.
//   - BBB, held at 100.00, closes at 89.00, below both its two-day low and
//     its 90.00 stop: a stop sell, which beats the breakout's sell.
//   - CCC, not held, would break out too, but its sources disagree on the
//     date: excluded.
//   - DDD, held at 10.00, does nothing: a hold with a 9.00 stop.
func standardDay(t *testing.T, h *harness) []holdings.Holding {
	t.Helper()
	h.closes("AAA", "10.00", "10.00", "10.00", "10.00", "11.00")
	h.closes("BBB", "100.00", "100.00", "100.00", "100.00", "89.00")
	h.closes("DDD", "10.00", "10.00", "10.00", "10.00", "10.00")
	h.closes("CCC", "10.00", "10.00", "10.00", "10.00")
	h.primary.Add(pricestest.Bar("CCC", tradingDate, "11.00", "11.00", "11.00", "11.00", 1000))
	h.check.Add(pricestest.Bar("CCC", tradingDate, "12.00", "12.00", "12.00", "12.00", 1000))
	h.fetch(t, "BBB", "DDD")
	return []holdings.Holding{
		holding(t, "BBB", "3", "100.00", "2026-01-05"),
		holding(t, "DDD", "5", "10.00", "2026-01-05"),
	}
}

func count(t *testing.T, h *harness, table string) int {
	t.Helper()
	var n int
	if err := h.store.Reader().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTwoRunsGiveTheSameSignalsAndOneRowEach(t *testing.T) {
	h := newHarness(t)
	held := standardDay(t, h)
	strategy := loadStrategy(t, breakoutRules)
	ctx := context.Background()

	first, err := h.runner.Run(ctx, date(t, tradingDate), strategy, held)
	if err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(time.Hour)
	second, err := h.runner.Run(ctx, date(t, tradingDate), strategy, held)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(first.Plan, second.Plan) {
		t.Errorf("the two runs differ:\nfirst:  %+v\nsecond: %+v", first.Plan, second.Plan)
	}
	if first.NewSignals != 2 || first.NewSuggestions != 2 {
		t.Errorf("first run inserted %d signals and %d suggestions, want 2 and 2", first.NewSignals, first.NewSuggestions)
	}
	if second.NewSignals != 0 || second.NewSuggestions != 0 {
		t.Errorf("second run inserted %d signals and %d suggestions, want none", second.NewSignals, second.NewSuggestions)
	}
	if n := count(t, h, "signals"); n != 2 {
		t.Errorf("signals has %d rows, want 2", n)
	}
	if n := count(t, h, "holding_suggestions"); n != 2 {
		t.Errorf("holding_suggestions has %d rows, want 2", n)
	}

	stored, err := signals.Load(ctx, h.store.Reader(), date(t, tradingDate))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, first.Signals) {
		t.Errorf("stored signals differ from the plan:\nstored: %+v\nplan:   %+v", stored, first.Signals)
	}

	want := []struct {
		symbol string
		kind   signals.Kind
		side   signals.Side
		reason string
	}{
		{"AAA", signals.KindBuy, signals.SideBuy, "breakout: closed at 11.00, above its 2-day high of 10.00"},
		{"BBB", signals.KindStopSell, signals.SideSell, "fixed_stop: closed at 89.00, at or below its stop of 90.00"},
	}
	if len(stored) != len(want) {
		t.Fatalf("stored %d signals, want %d: %+v", len(stored), len(want), stored)
	}
	for i, w := range want {
		s := stored[i]
		if s.Symbol != w.symbol || s.Kind != w.kind || s.Side != w.side || s.Reason != w.reason {
			t.Errorf("signal %d = %s %s %s %q, want %s %s %s %q", i, s.Symbol, s.Kind, s.Side, s.Reason,
				w.symbol, w.kind, w.side, w.reason)
		}
		if s.Strategy != "test-breakout" || s.StrategyHash != strategy.Hash || s.Rule != strings.Split(w.reason, ":")[0] {
			t.Errorf("signal %d strategy, hash or rule wrong: %+v", i, s)
		}
		if k := signals.Key("test-breakout", w.symbol, w.side, w.kind, date(t, tradingDate)); s.Key != k {
			t.Errorf("signal %d key = %s, want %s", i, s.Key, k)
		}
	}

	sugs, err := signals.LoadSuggestions(ctx, h.store.Reader(), date(t, tradingDate))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sugs, first.Suggestions) {
		t.Errorf("stored suggestions differ from the plan:\nstored: %+v\nplan:   %+v", sugs, first.Suggestions)
	}
	if len(sugs) != 2 ||
		sugs[0].Symbol != "BBB" || sugs[0].Suggestion != signals.SuggestSell || stopText(sugs[0].Stop) != "90.00" ||
		sugs[1].Symbol != "DDD" || sugs[1].Suggestion != signals.Hold || stopText(sugs[1].Stop) != "9.00" ||
		!strings.HasPrefix(sugs[1].Reason, "breakout: ") {
		t.Errorf("suggestions = %+v, want BBB sell (stop 90.00) and DDD hold (stop 9.00, breakout's reason)", sugs)
	}
	if !strings.Contains(h.logs.String(), `"order_key":"`+stored[0].Key+`"`) {
		t.Errorf("the log does not carry the order key:\n%s", h.logs.String())
	}
}

func TestAnExcludedStockGetsNoSignal(t *testing.T) {
	h := newHarness(t)
	held := standardDay(t, h)
	res, err := h.runner.Run(context.Background(), date(t, tradingDate), loadStrategy(t, breakoutRules), held)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range res.Signals {
		if s.Symbol == "CCC" {
			t.Errorf("CCC got a signal although its sources disagreed: %+v", s)
		}
	}
	if !reflect.DeepEqual(res.Excluded, []string{"CCC"}) {
		t.Errorf("excluded = %v, want [CCC]", res.Excluded)
	}
	if !strings.Contains(h.logs.String(), "no signals for CCC") {
		t.Errorf("the log does not say CCC was left out:\n%s", h.logs.String())
	}

	// The same stock with matching sources would have broken out, so the
	// exclusion is what kept it out.
	h2 := newHarness(t)
	h2.closes("AAA", "10.00", "10.00", "10.00", "10.00", "10.00")
	h2.closes("BBB", "10.00", "10.00", "10.00", "10.00", "10.00")
	h2.closes("CCC", "10.00", "10.00", "10.00", "10.00", "11.00")
	h2.closes("DDD", "10.00", "10.00", "10.00", "10.00", "10.00")
	h2.fetch(t)
	res, err = h2.runner.Run(context.Background(), date(t, tradingDate), loadStrategy(t, breakoutRules), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Signals) != 1 || res.Signals[0].Symbol != "CCC" || res.Signals[0].Kind != signals.KindBuy {
		t.Errorf("signals = %+v, want one buy for CCC", res.Signals)
	}
}

func TestAnExcludedHoldingGetsNoSuggestion(t *testing.T) {
	h := newHarness(t)
	h.closes("AAA", "10.00", "10.00", "10.00", "10.00", "10.00")
	h.closes("BBB", "10.00", "10.00", "10.00", "10.00", "10.00")
	h.closes("DDD", "10.00", "10.00", "10.00", "10.00", "10.00")
	for _, d := range history {
		h.primary.Add(pricestest.Bar("CCC", d, "10.00", "10.00", "10.00", "10.00", 1000))
	}
	h.check.Add(pricestest.Bar("CCC", tradingDate, "10.00", "12.00", "10.00", "12.00", 1000))
	day := h.fetch(t, "CCC")
	if !day.Halt() {
		t.Fatal("a held stock whose sources disagree should halt")
	}
	res, err := h.runner.Run(context.Background(), date(t, tradingDate), loadStrategy(t, breakoutRules),
		[]holdings.Holding{holding(t, "CCC", "1", "10.00", "2026-01-05")})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Signals) != 0 || len(res.Suggestions) != 0 {
		t.Errorf("an excluded holding got signals %+v or suggestions %+v", res.Signals, res.Suggestions)
	}
}

func TestAMissingDayRecordsNothing(t *testing.T) {
	ctx := context.Background()

	// Never checked.
	h := newHarness(t)
	res, err := h.runner.Run(ctx, date(t, tradingDate), loadStrategy(t, breakoutRules), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Missing, "were not checked") || len(res.Signals) != 0 {
		t.Errorf("unchecked date: missing = %q, signals = %+v", res.Missing, res.Signals)
	}

	// Checked, but one stock has no bar from the cross-check source, so the
	// whole day is missing, though AAA alone would have broken out.
	h = newHarness(t)
	h.closes("AAA", "10.00", "10.00", "10.00", "10.00", "11.00")
	h.closes("BBB", "10.00", "10.00", "10.00", "10.00", "10.00")
	h.closes("CCC", "10.00", "10.00", "10.00", "10.00", "10.00")
	h.closes("DDD", "10.00", "10.00", "10.00", "10.00", "10.00")
	h.check.Fail("DDD", prices.ErrNoData)
	if day := h.fetch(t); day.Usable {
		t.Fatal("the day should not be usable")
	}
	res, err = h.runner.Run(ctx, date(t, tradingDate), loadStrategy(t, breakoutRules), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Missing, "are missing") || len(res.Signals) != 0 || len(res.Suggestions) != 0 {
		t.Errorf("missing day: missing = %q, signals = %+v", res.Missing, res.Signals)
	}
	if n := count(t, h, "signals"); n != 0 {
		t.Errorf("a missing day recorded %d signals", n)
	}
	if !strings.Contains(h.logs.String(), "rules run: no signals") {
		t.Errorf("the log does not say the day gave no signals:\n%s", h.logs.String())
	}
}

func TestEvaluateIsDeterministic(t *testing.T) {
	h := newHarness(t)
	held := standardDay(t, h)
	ctx := context.Background()
	day, err := prices.LoadDay(ctx, h.store.Reader(), date(t, tradingDate))
	if err != nil {
		t.Fatal(err)
	}
	bars := map[string][]prices.Bar{}
	for _, sym := range []string{"AAA", "BBB", "CCC", "DDD"} {
		if bars[sym], err = prices.History(ctx, h.store.Reader(), "primary", sym, date(t, "2026-01-01"), date(t, tradingDate)); err != nil {
			t.Fatal(err)
		}
	}
	in := signals.Inputs{Date: date(t, tradingDate).Add(16 * time.Hour), Strategy: loadStrategy(t, breakoutRules),
		Day: day, Bars: bars, Holdings: held}
	a, err := signals.Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Holdings = []holdings.Holding{held[1], held[0]}
	in.Date = date(t, tradingDate)
	b, err := signals.Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("the same inputs gave different plans:\n%+v\n%+v", a, b)
	}

	// A bar after the trading date (a later day already fetched) is ignored.
	bars["AAA"] = append(bars["AAA"], pricestest.Bar("AAA", "2026-01-12", "1.00", "1.00", "1.00", "1.00", 1))
	c, err := signals.Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, c) {
		t.Errorf("a later bar changed the plan:\n%+v\n%+v", a, c)
	}
}

func TestEvaluateRefuses(t *testing.T) {
	h := newHarness(t)
	held := standardDay(t, h)
	day, err := prices.LoadDay(context.Background(), h.store.Reader(), date(t, tradingDate))
	if err != nil {
		t.Fatal(err)
	}
	good := loadStrategy(t, breakoutRules)

	badRules := loadStrategy(t, strings.Replace(breakoutRules, "          exit_days: 2\n", "", 1))
	if _, err := signals.Evaluate(signals.Inputs{Date: day.Date, Strategy: badRules, Day: day}); !errors.Is(err, config.ErrInvalid) ||
		!strings.Contains(err.Error(), "exit_days is missing") {
		t.Errorf("bad rule settings: err = %v, want one naming exit_days", err)
	}

	noHash := *good
	noHash.Hash = ""
	if _, err := signals.Evaluate(signals.Inputs{Date: day.Date, Strategy: &noHash, Day: day}); !errors.Is(err, signals.ErrInput) {
		t.Errorf("no hash: err = %v, want ErrInput", err)
	}

	if _, err := signals.Evaluate(signals.Inputs{Date: date(t, "2026-01-08"), Strategy: good, Day: day}); !errors.Is(err, signals.ErrInput) {
		t.Errorf("a price check for another date: err = %v, want ErrInput", err)
	}

	// AAA's prices agreed, but its bars were not passed in.
	if _, err := signals.Evaluate(signals.Inputs{Date: day.Date, Strategy: good, Day: day, Holdings: held}); !errors.Is(err, signals.ErrInput) {
		t.Errorf("no bars for an agreed stock: err = %v, want ErrInput", err)
	}
}

func TestKey(t *testing.T) {
	d := date(t, tradingDate)
	base := signals.Key("s", "AAA", signals.SideBuy, signals.KindBuy, d)
	// The key's form is pinned: paper-trading's orders reuse it, so changing
	// it would make a re-run miss the orders already made.
	if want := "0c8030ba89154e9e3acead409edc9d7789dc9435440399f0eaaf36a5ec9492ca"; base != want {
		t.Errorf("key = %s, want %s", base, want)
	}
	if signals.Key("s", "AAA", signals.SideBuy, signals.KindBuy, d.Add(20*time.Hour)) != base {
		t.Error("the time of day changed the key")
	}
	for name, k := range map[string]string{
		"strategy": signals.Key("t", "AAA", signals.SideBuy, signals.KindBuy, d),
		"symbol":   signals.Key("s", "AAB", signals.SideBuy, signals.KindBuy, d),
		"side":     signals.Key("s", "AAA", signals.SideSell, signals.KindBuy, d),
		"kind":     signals.Key("s", "AAA", signals.SideBuy, signals.KindAdd, d),
		"date":     signals.Key("s", "AAA", signals.SideBuy, signals.KindBuy, d.AddDate(0, 0, 1)),
		"boundary": signals.Key("s\",\"AAA", "", signals.SideBuy, signals.KindBuy, d),
	} {
		if k == base {
			t.Errorf("changing the %s did not change the key", name)
		}
		if len(k) != 64 {
			t.Errorf("key %q is not 64 hex characters", k)
		}
	}
}

func TestSignalsTablesAreAppendOnlyAndKeyed(t *testing.T) {
	h := newHarness(t)
	held := standardDay(t, h)
	if _, err := h.runner.Run(context.Background(), date(t, tradingDate), loadStrategy(t, breakoutRules), held); err != nil {
		t.Fatal(err)
	}
	w := h.store.Writer()
	for _, stmt := range []string{
		`UPDATE signals SET reason = 'x'`,
		`DELETE FROM signals`,
		`UPDATE holding_suggestions SET reason = 'x'`,
		`DELETE FROM holding_suggestions`,
	} {
		if _, err := w.Exec(stmt); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: err = %v, want append-only", stmt, err)
		}
	}
	hash := strings.Repeat("a", 64)
	at := store.FormatTime(h.clock.Now())
	insert := `INSERT INTO signals (idempotency_key, strategy, symbol, trading_date, side, kind, rule_type, reason,
	    strategy_hash, created_at) VALUES (?, 'test-breakout', 'AAA', ?, ?, ?, 'breakout', 'r', ?, ?)`
	for name, args := range map[string][]any{
		"same five fields, other key": {strings.Repeat("b", 64), tradingDate, "buy", "buy", hash, at},
		"buy side with a sell kind":   {strings.Repeat("c", 64), "2026-01-12", "buy", "sell", hash, at},
		"unknown kind":                {strings.Repeat("d", 64), "2026-01-12", "sell", "exit", hash, at},
		"key not hex":                 {strings.Repeat("Z", 64), "2026-01-12", "buy", "buy", hash, at},
	} {
		if _, err := w.Exec(insert, args...); err == nil {
			t.Errorf("%s: the insert was accepted", name)
		}
	}
}

func stopText(p *money.Price) string {
	if p == nil {
		return "none"
	}
	return p.String()
}
