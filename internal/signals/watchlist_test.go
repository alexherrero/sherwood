package signals_test

import (
	"context"
	"strings"
	"testing"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/signals"
)

// Every symbol and price here is made up.

// watchStrategy has two strategies with the same breakout rules over DDD:
// "watching" opts in to the watchlist and "own-stocks" does not.
func watchStrategy(t *testing.T) *config.Strategy {
	t.Helper()
	head, _, _ := strings.Cut(strategyText(t, breakoutRules), "\nstrategies:\n")
	head = strings.Replace(head, "\nsectors:\n", "\nsectors:\n  EEE: sector-b\n", 1)
	doc := head + `
strategies:
  - name: watching
    portfolio_share: 0.30
    stocks: [DDD]
    include_watchlist: true
    rules:
` + breakoutRules + `  - name: own-stocks
    portfolio_share: 0.30
    stocks: [DDD]
    rules:
` + breakoutRules
	s, err := config.ParseStrategy([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestOnlyAStrategyThatOptsInSignalsOnAnAddedStock adds EEE from the phone
// app and runs the rules the way the after-close job does: the watchlist in
// force, applied to the strategy file. EEE breaks out on the date.
func TestOnlyAStrategyThatOptsInSignalsOnAnAddedStock(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.closes("DDD", "10.00", "10.00", "10.00", "10.00", "10.00")
	h.closes("EEE", "10.00", "10.00", "10.00", "10.00", "11.00")
	file := watchStrategy(t)

	// EEE's prices are checked either way, so only the watchlist decides
	// whether a strategy's rules look at it.
	f := &prices.Fetcher{Primary: h.primary, Check: h.check, Clock: h.clock, Store: h.store,
		Log: h.runner.Log, HistoryDays: 10, Calendar: clock.NYSE()}
	if _, err := f.FetchDay(ctx, date(t, tradingDate), prices.Stocks{Watched: []string{"DDD", "EEE"}},
		config.MustDecimal("0.02")); err != nil {
		t.Fatal(err)
	}
	run := func() []signals.Signal {
		t.Helper()
		watched, err := limits.Watchlist(ctx, h.store.Reader(), file)
		if err != nil {
			t.Fatal(err)
		}
		res, err := h.runner.Run(ctx, date(t, tradingDate), file.WithWatchlist(watched), nil)
		if err != nil {
			t.Fatal(err)
		}
		return res.Signals
	}

	// Before the tap, EEE is on no strategy's list and nothing signals.
	if got := run(); len(got) != 0 {
		t.Fatalf("before the tap: signals %+v, want none", got)
	}
	if _, err := limits.Watch(ctx, h.store, file, h.clock.Now(), "EEE"); err != nil {
		t.Fatal(err)
	}
	got := run()
	if len(got) != 1 {
		t.Fatalf("after the tap: signals %+v, want one buy of EEE", got)
	}
	if s := got[0]; s.Strategy != "watching" || s.Symbol != "EEE" || s.Kind != signals.KindBuy ||
		s.StrategyHash != file.Hash {
		t.Errorf("signal = %+v, want a buy of EEE from the strategy that opts in, under the file's hash", s)
	}
	for _, s := range got {
		if s.Strategy == "own-stocks" {
			t.Errorf("the strategy that does not opt in made a signal: %+v", s)
		}
	}
	if !strings.Contains(file.Strategies[0].Stocks[0], "DDD") || len(file.Strategies[0].Stocks) != 1 {
		t.Errorf("the strategy file's own stocks changed: %v", file.Strategies[0].Stocks)
	}
}
