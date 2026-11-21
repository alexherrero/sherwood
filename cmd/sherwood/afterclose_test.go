package main

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/broker/paper"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/execution"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
	"github.com/alexherrero/sherwood/internal/prices/pricestest"
	"github.com/alexherrero/sherwood/internal/risk"
	"github.com/alexherrero/sherwood/internal/scheduler"
	"github.com/alexherrero/sherwood/internal/secret"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every symbol, price and holding below is made up. The dates are in late
// November 2026: Thursday 26 November is Thanksgiving, when the exchange is
// closed, and Friday 27 November is a half day.

// acStrategy is the example file's limits with a made-up watchlist and one
// made-up strategy: a two-day breakout, a 10% fixed stop and fixed sizing.
func acStrategy(t *testing.T) *config.Strategy {
	t.Helper()
	return acStrategyWith(t, "")
}

// acStrategyWith is acStrategy with extra written into the strategy's
// entry, after its stocks, and a sector for the made-up FFF.
func acStrategyWith(t *testing.T, extra string) *config.Strategy {
	t.Helper()
	head, _, ok := strings.Cut(readRepoFile(t, "strategy.example.yaml"), "\nwatchlist:\n")
	if !ok {
		t.Fatal("the example file has no watchlist block")
	}
	s, err := config.ParseStrategy([]byte(head + `
watchlist:
  - AAA
  - EEE
sectors:
  AAA: sector-a
  BBB: sector-b
  EEE: sector-a
  FFF: sector-b
strategies:
  - name: test-breakout
    portfolio_share: 0.50
    stocks: [AAA, BBB]
` + extra + `    rules:
      - type: breakout
        settings:
          days: 2
          exit_days: 2
      - type: fixed_stop
        settings:
          distance: 0.10
      - type: size_fixed_amount
        settings:
          amount: "100.00"
`))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var acSymbols = []string{"AAA", "BBB", "EEE"}

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation(clock.DateLayout, s, clock.Eastern)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// at is hh:mm US Eastern on the date s.
func at(t *testing.T, s string, hour, minute int) time.Time {
	return day(t, s).Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

type acHarness struct {
	store   *store.Store
	clock   *clock.Fake
	primary *pricestest.Source
	check   *pricestest.Source
	sched   *scheduler.Scheduler
	logs    *loggingtest.Output
	alerts  *notifytest.Fake
	halts   *halt.Switch
	paper   *paper.Broker
	// strategy is the strategy file the job runs with.
	strategy *config.Strategy
}

// newAC builds a store, two fake sources and a scheduler with the
// after-close job registered. Both sources hold a flat 10.00 bar for every
// stock on every trading date of November 2026, except that AAA closes at
// 11.00 from 24 November on: a breakout, so a buy, on the 24th only.
//
// missing names bars ("SYMBOL YYYY-MM-DD") the cross-check source lacks.
func newAC(t *testing.T, now time.Time, catchUpDays int, missing ...string) *acHarness {
	t.Helper()
	return newACWith(t, now, catchUpDays, acStrategy(t), missing...)
}

// newACWith is newAC with the given strategy file.
func newACWith(t *testing.T, now time.Time, catchUpDays int, strategy *config.Strategy, missing ...string) *acHarness {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sherwood.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	log, out := loggingtest.New(t)
	h := &acHarness{store: s, clock: clock.NewFake(now), primary: pricestest.New("primary"),
		check: pricestest.New("cross"), logs: out, alerts: &notifytest.Fake{}}
	h.halts, err = halt.New(halt.Config{Store: s, Clock: h.clock, Notifier: h.alerts, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	for d := day(t, "2026-11-02"); d.Month() == time.November; d = d.AddDate(0, 0, 1) {
		if open, err := clock.NYSE().IsTradingDay(d); err != nil || !open {
			continue
		}
		date := d.Format(clock.DateLayout)
		for _, sym := range acSymbols {
			c := "10.00"
			if sym == "AAA" && date >= "2026-11-24" {
				c = "11.00"
			}
			b := pricestest.Bar(sym, date, c, c, c, c, 1000)
			h.primary.Add(b)
			if !slices.Contains(missing, sym+" "+date) {
				h.check.Add(b)
			}
		}
	}
	h.sched, err = scheduler.New(scheduler.Config{Store: s, Clock: h.clock, Calendar: clock.NYSE(), Logger: log,
		CatchUpDays: catchUpDays})
	if err != nil {
		t.Fatal(err)
	}
	h.paper, err = paper.New(paper.Config{Store: s, Clock: h.clock, Calendar: clock.NYSE(), Policy: money.WholeShares})
	if err != nil {
		t.Fatal(err)
	}
	ex, err := execution.New(execution.Config{Store: s, Clock: h.clock, Calendar: clock.NYSE(), Adapter: h.paper,
		Checker: risk.Standard{}, Switch: h.halts, Strategy: strategy, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	h.strategy = strategy
	a := &afterClose{store: s, clock: h.clock, calendar: clock.NYSE(), log: log, strategy: strategy,
		halts: h.halts, alerts: h.alerts, paper: h.paper, exec: ex, primary: h.primary, check: h.check}
	if err := a.register(h.sched); err != nil {
		t.Fatal(err)
	}
	return h
}

// runAt sets the clock and runs whatever is due.
func (h *acHarness) runAt(t *testing.T, now time.Time) {
	t.Helper()
	h.clock.Set(now)
	if err := h.sched.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type jobRun struct {
	job, date, outcome, detail, hash string
}

// runs lists job_runs in the order the runs were claimed.
func (h *acHarness) runs(t *testing.T) []jobRun {
	t.Helper()
	rows, err := h.store.Reader().Query(
		`SELECT job_name, trading_date, outcome, detail, input_hash FROM job_runs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []jobRun
	for rows.Next() {
		var r jobRun
		if err := rows.Scan(&r.job, &r.date, &r.outcome, &r.detail, &r.hash); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// summary is "job date outcome" per run.
func summary(runs []jobRun) []string {
	var out []string
	for _, r := range runs {
		out = append(out, r.job+" "+r.date+" "+r.outcome)
	}
	return out
}

func (h *acHarness) strings(t *testing.T, query string, args ...any) []string {
	t.Helper()
	rows, err := h.store.Reader().Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// signalText is "date strategy symbol kind" per recorded signal.
func (h *acHarness) signalText(t *testing.T) []string {
	return h.strings(t, `SELECT trading_date || ' ' || strategy || ' ' || symbol || ' ' || kind FROM signals ORDER BY id`)
}

// fetchedDates lists the end date of each request to the primary source.
func (h *acHarness) fetchedDates() []string {
	var out []string
	for _, c := range h.primary.Calls() {
		out = append(out, c.To)
	}
	return out
}

func threeEach(dates ...string) []string {
	var out []string
	for _, d := range dates {
		out = append(out, d, d, d)
	}
	return out
}

func TestAfterCloseRunsOncePerTradingDayAndSkipsHolidays(t *testing.T) {
	h := newAC(t, at(t, "2026-11-23", 9, 0), 1)
	for d := day(t, "2026-11-23"); !d.After(day(t, "2026-11-30")); d = d.AddDate(0, 0, 1) {
		date := d.Format(clock.DateLayout)
		before := len(h.runs(t))
		h.runAt(t, at(t, date, 16, 29))
		if got := len(h.runs(t)); got != before {
			t.Fatalf("%s 16:29: %d runs started before 16:30", date, got-before)
		}
		h.runAt(t, at(t, date, 16, 30))
		h.runAt(t, at(t, date, 23, 59))
		h.runAt(t, at(t, date, 23, 59)) // nothing is run twice
	}

	var want []string
	for _, date := range []string{"2026-11-23", "2026-11-24", "2026-11-25", "2026-11-27", "2026-11-30"} {
		want = append(want,
			"after-close "+date+" succeeded",
			"after-close-retry-1 "+date+" skipped",
			"after-close-retry-2 "+date+" skipped")
	}
	if got := summary(h.runs(t)); !reflect.DeepEqual(got, want) {
		t.Errorf("job runs:\n got %q\nwant %q", got, want)
	}
	for _, r := range h.runs(t) {
		if r.job != "after-close" && !strings.Contains(r.detail, "an earlier attempt already recorded") {
			t.Errorf("%s %s skipped for %q", r.job, r.date, r.detail)
		}
	}
	checked := h.strings(t, `SELECT trading_date FROM price_days ORDER BY trading_date`)
	if want := []string{"2026-11-23", "2026-11-24", "2026-11-25", "2026-11-27", "2026-11-30"}; !reflect.DeepEqual(checked, want) {
		t.Errorf("checked dates %q, want %q", checked, want)
	}
	if got, want := h.fetchedDates(), threeEach("2026-11-23", "2026-11-24", "2026-11-25", "2026-11-27", "2026-11-30"); !reflect.DeepEqual(got, want) {
		t.Errorf("primary requests ended on %q, want %q", got, want)
	}
	if got, want := h.signalText(t), []string{"2026-11-24 test-breakout AAA buy"}; !reflect.DeepEqual(got, want) {
		t.Errorf("signals %q, want %q", got, want)
	}
}

func TestAfterCloseCatchesUpWithEachDatesOwnBarsAndHoldings(t *testing.T) {
	h := newAC(t, at(t, "2026-11-30", 8, 0), 0)
	ctx := context.Background()
	held := holdings.Holding{Symbol: "BBB", Quantity: money.MustParseQuantity("3"),
		AverageCost: money.MustParseMoney("9.50"), Opened: day(t, "2026-11-20")}
	// BBB was held at 16:30 on the 24th and sold at 17:00 that evening.
	if _, _, err := holdings.Record(ctx, h.store, holdings.Snapshot{Source: holdings.SourceImport,
		AsOf: at(t, "2026-11-24", 10, 0), Holdings: []holdings.Holding{held}, Cash: money.MustParseMoney("100.00")}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := holdings.Record(ctx, h.store, holdings.Snapshot{Source: holdings.SourceFills,
		AsOf: at(t, "2026-11-24", 17, 0), Cash: money.MustParseMoney("130.00")}); err != nil {
		t.Fatal(err)
	}

	// Down since the morning of the 23rd: the catch-up window is the last
	// five trading dates, and today's 16:30 has not come yet.
	h.runAt(t, at(t, "2026-11-30", 8, 0))

	var want []string
	for _, date := range []string{"2026-11-23", "2026-11-24", "2026-11-25", "2026-11-27"} {
		want = append(want,
			"after-close "+date+" succeeded",
			"after-close-retry-1 "+date+" skipped",
			"after-close-retry-2 "+date+" skipped")
	}
	runs := h.runs(t)
	if got := summary(runs); !reflect.DeepEqual(got, want) {
		t.Fatalf("job runs:\n got %q\nwant %q", got, want)
	}
	if got, want := h.fetchedDates(), threeEach("2026-11-23", "2026-11-24", "2026-11-25", "2026-11-27"); !reflect.DeepEqual(got, want) {
		t.Errorf("primary requests ended on %q, want %q", got, want)
	}
	// The breakout is on the 24th's own bars; a run reading later bars would
	// miss it or put it on another date.
	if got, want := h.signalText(t), []string{"2026-11-24 test-breakout AAA buy"}; !reflect.DeepEqual(got, want) {
		t.Errorf("signals %q, want %q", got, want)
	}

	// Each date reads the holdings as they were at its own 16:30.
	heldOn := h.strings(t, `SELECT trading_date FROM price_checks WHERE held = 1 ORDER BY trading_date`)
	if want := []string{"2026-11-24"}; !reflect.DeepEqual(heldOn, want) {
		t.Errorf("BBB checked as held on %q, want %q", heldOn, want)
	}
	sugg := h.strings(t, `SELECT trading_date || ' ' || symbol || ' ' || suggestion FROM holding_suggestions ORDER BY id`)
	if want := []string{"2026-11-24 BBB hold"}; !reflect.DeepEqual(sugg, want) {
		t.Errorf("holding suggestions %q, want %q", sugg, want)
	}

	values := map[string]string{}
	lines, err := h.logs.Lines()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		switch l["msg"] {
		case "portfolio valued":
			values[fmt.Sprint(l["trading_date"])] = fmt.Sprint(l["amount"])
			if l["input_hash"] == nil {
				t.Errorf("valuation logged without the run's input hash: %v", l)
			}
		case "portfolio not valued":
			values[fmt.Sprint(l["trading_date"])] = "none: " + fmt.Sprint(l["reason"])
		}
	}
	wantValues := map[string]string{
		"2026-11-23": "none: no holdings or cash have been imported yet",
		"2026-11-24": "130.00", // 3 × 10.00 + 100.00
		"2026-11-25": "130.00", // sold: cash only
		"2026-11-27": "130.00",
	}
	if !reflect.DeepEqual(values, wantValues) {
		t.Errorf("valuations %v, want %v", values, wantValues)
	}

	// The input hash is the date plus what the run read: every attempt on a
	// date reads the same, and the dates differ.
	hashes := map[string]string{}
	for _, r := range runs {
		if have, ok := hashes[r.date]; ok && have != r.hash {
			t.Errorf("%s %s input hash %s, earlier attempt %s", r.job, r.date, r.hash, have)
		}
		hashes[r.date] = r.hash
	}
	if len(hashes) != 4 || hashes["2026-11-25"] == hashes["2026-11-27"] {
		t.Errorf("input hashes per date %v", hashes)
	}
}

func TestAfterCloseHaltsWhenTheSourcesDisagreeOnAHeldStock(t *testing.T) {
	h := newAC(t, at(t, "2026-11-24", 9, 0), 1)
	ctx := context.Background()
	held := holdings.Holding{Symbol: "BBB", Quantity: money.MustParseQuantity("3"),
		AverageCost: money.MustParseMoney("9.50"), Opened: day(t, "2026-11-20")}
	if _, _, err := holdings.Record(ctx, h.store, holdings.Snapshot{Source: holdings.SourceImport,
		AsOf: at(t, "2026-11-24", 10, 0), Holdings: []holdings.Holding{held}, Cash: money.MustParseMoney("100.00")}); err != nil {
		t.Fatal(err)
	}
	// The cross-check source has BBB 20% higher on the 24th.
	h.check.Add(pricestest.Bar("BBB", "2026-11-24", "12.00", "12.00", "12.00", "12.00", 1000))
	h.runAt(t, at(t, "2026-11-24", 16, 30))

	st, err := halt.Active(ctx, h.store.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if !st.On() || len(st.Active) != 1 || st.Active[0].Cause != halt.PriceDisagreement {
		t.Fatalf("switch = %+v, want one price halt", st)
	}
	ev := st.Active[0].Evidence
	if !strings.Contains(ev, "Price check for 2026-11-24 (primary against cross)") || !strings.Contains(ev, " BBB: ") ||
		strings.Contains(ev, "AAA") {
		t.Errorf("evidence %q", ev)
	}
	if sent := h.alerts.Sent(); len(sent) != 1 || sent[0] != notify.TradingHalted() {
		t.Errorf("alerts %v, want one TradingHalted", sent)
	}
	// While halted, the rules still run and record their signals.
	if got, want := h.signalText(t), []string{"2026-11-24 test-breakout AAA buy"}; !reflect.DeepEqual(got, want) {
		t.Errorf("signals %q, want %q", got, want)
	}
	if runs := h.runs(t); runs[len(runs)-1].outcome != "succeeded" {
		t.Errorf("the attempt %s", runs[len(runs)-1].outcome)
	}

	// The next day agrees, which does not reset the switch: only you can.
	h.runAt(t, at(t, "2026-11-25", 16, 30))
	if st, err := halt.Active(ctx, h.store.Reader()); err != nil || len(st.Active) != 1 {
		t.Errorf("after a clean day: %+v, %v", st, err)
	}
	if len(h.alerts.Sent()) != 1 {
		t.Errorf("a clean day notified")
	}
}

func TestAfterCloseMissingDayMakesNoSignals(t *testing.T) {
	// The cross-check source has not published BBB's bar for the 24th, the
	// day AAA breaks out.
	start := func(t *testing.T) *acHarness {
		t.Helper()
		h := newAC(t, at(t, "2026-11-24", 9, 0), 1, "BBB 2026-11-24")
		h.runAt(t, at(t, "2026-11-24", 16, 30))
		h.runAt(t, at(t, "2026-11-24", 18, 0))
		runs := h.runs(t)
		want := []string{"after-close 2026-11-24 skipped", "after-close-retry-1 2026-11-24 skipped"}
		if got := summary(runs); !reflect.DeepEqual(got, want) {
			t.Fatalf("job runs %q, want %q", got, want)
		}
		for i, next := range []string{"18:00", "21:00"} {
			if !strings.Contains(runs[i].detail, "prices not usable yet, so no signals; trying again at "+next) ||
				!strings.Contains(runs[i].detail, "BBB") {
				t.Errorf("%s skipped for %q", runs[i].job, runs[i].detail)
			}
		}
		if got := h.signalText(t); len(got) != 0 {
			t.Errorf("signals on a missing day: %q", got)
		}
		if n := h.strings(t, `SELECT count(*) FROM holding_suggestions`); n[0] != "0" {
			t.Errorf("%s holding suggestions on a missing day", n[0])
		}
		if got := h.strings(t, `SELECT usable FROM price_days WHERE trading_date = '2026-11-24'`); !reflect.DeepEqual(got, []string{"0"}) {
			t.Errorf("price_days usable = %q, want 0", got)
		}
		return h
	}

	t.Run("a later attempt finds the bar", func(t *testing.T) {
		h := start(t)
		h.check.Add(pricestest.Bar("BBB", "2026-11-24", "10.00", "10.00", "10.00", "10.00", 1000))
		h.runAt(t, at(t, "2026-11-24", 21, 0))
		runs := h.runs(t)
		if got := runs[len(runs)-1]; got.job != "after-close-retry-2" || got.outcome != "succeeded" {
			t.Fatalf("last run %+v, want after-close-retry-2 succeeded", got)
		}
		if got, want := h.signalText(t), []string{"2026-11-24 test-breakout AAA buy"}; !reflect.DeepEqual(got, want) {
			t.Errorf("signals %q, want %q", got, want)
		}
		if runs[0].hash != runs[1].hash || runs[1].hash != runs[2].hash {
			t.Errorf("attempts on one date read different inputs: %q", []string{runs[0].hash, runs[1].hash, runs[2].hash})
		}
	})

	t.Run("it never appears", func(t *testing.T) {
		h := start(t)
		h.runAt(t, at(t, "2026-11-24", 21, 0))
		runs := h.runs(t)
		last := runs[len(runs)-1]
		if last.job != "after-close-retry-2" || last.outcome != "skipped" ||
			!strings.Contains(last.detail, "prices not usable, so this date has no signals") {
			t.Fatalf("last run %+v", last)
		}
		// The next day is a day like any other, and the 24th stays empty.
		h.runAt(t, at(t, "2026-11-25", 16, 30))
		if got := h.strings(t, `SELECT outcome FROM job_runs WHERE job_name = 'after-close' AND trading_date = '2026-11-25'`); !reflect.DeepEqual(got, []string{"succeeded"}) {
			t.Errorf("the 25th's run: %q", got)
		}
		if got := h.signalText(t); len(got) != 0 {
			t.Errorf("signals %q, want none", got)
		}
	})
}

// orderText is "date symbol kind state" per order, oldest first.
func (h *acHarness) orderText(t *testing.T) []string {
	return h.strings(t, `SELECT trading_date || ' ' || symbol || ' ' || kind || ' ' || state FROM orders ORDER BY id`)
}

// recordCash records made-up cash and no holdings as of 10:00 on date.
func (h *acHarness) recordCash(t *testing.T, date, amount string) {
	t.Helper()
	if _, _, err := holdings.Record(context.Background(), h.store, holdings.Snapshot{Source: holdings.SourceImport,
		AsOf: at(t, date, 10, 0), Cash: money.MustParseMoney(amount)}); err != nil {
		t.Fatal(err)
	}
}

func TestAfterCloseProposesOrdersAndExpiresThemNextClose(t *testing.T) {
	h := newAC(t, at(t, "2026-11-24", 9, 0), 1)
	h.recordCash(t, "2026-11-24", "1000.00")

	// The 24th's breakout becomes a buy for the 25th, waiting for approval,
	// and one alert says so. The later attempts skip and propose nothing.
	for _, hm := range [][2]int{{16, 30}, {18, 0}, {21, 0}} {
		h.runAt(t, at(t, "2026-11-24", hm[0], hm[1]))
	}
	if got, want := h.orderText(t), []string{"2026-11-25 AAA buy awaiting_approval"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("orders %q, want %q", got, want)
	}
	if got := h.alerts.Sent(); !reflect.DeepEqual(got, []notify.Alert{notify.BuysWaiting(1)}) {
		t.Errorf("alerts %v, want one buys-waiting alert for 1", got)
	}

	// Nobody approves it, so the 25th's close expires it.
	h.runAt(t, at(t, "2026-11-25", 16, 30))
	if got, want := h.orderText(t), []string{"2026-11-25 AAA buy declined"}; !reflect.DeepEqual(got, want) {
		t.Errorf("orders %q, want %q", got, want)
	}
	if got := h.strings(t, `SELECT actor FROM order_history ORDER BY id DESC LIMIT 1`); got[0] != "expiry" {
		t.Errorf("last move by %q, want expiry", got[0])
	}
}

func TestAfterCloseCatchUpProposesNoBuy(t *testing.T) {
	// Down from before the 24th's close until the next morning, before the
	// 25th's session opens.
	h := newAC(t, at(t, "2026-11-25", 8, 0), 2)
	h.recordCash(t, "2026-11-24", "1000.00")
	h.runAt(t, at(t, "2026-11-25", 8, 0))
	if got, want := h.signalText(t), []string{"2026-11-24 test-breakout AAA buy"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("signals %q, want %q", got, want)
	}
	if got := h.orderText(t); len(got) != 0 {
		t.Errorf("orders %q, want none", got)
	}
	if !strings.Contains(h.logs.String(), "this run is catching up a date whose day has gone") {
		t.Errorf("log:\n%s", h.logs.String())
	}
	if len(h.alerts.Sent()) != 0 {
		t.Errorf("alerts %v, want none", h.alerts.Sent())
	}
}

func TestAfterCloseIsOffWithoutBothKeys(t *testing.T) {
	ctx := context.Background()
	now := at(t, "2026-11-24", 23, 0)
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sherwood.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	log, out := loggingtest.New(t)
	clk := clock.NewFake(now)
	sched, err := scheduler.New(scheduler.Config{Store: s, Clock: clk, Calendar: clock.NYSE(), Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	keys := config.PriceKeys{TiingoToken: secret.New("made-up-token")}
	if err := registerAfterClose(env{clock: clk}, sched, keys, acStrategy(t), s, nil, notify.Nowhere, trading{}, log); err != nil {
		t.Fatal(err)
	}
	if err := sched.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if n := (&acHarness{store: s}).strings(t, `SELECT count(*) FROM job_runs`); n[0] != "0" {
		t.Errorf("%s job runs with the job off", n[0])
	}
	if !strings.Contains(out.String(), "after-close job is off") || strings.Contains(out.String(), "made-up-token") {
		t.Errorf("log:\n%s", out.String())
	}

	p, c := newPriceSources(config.PriceKeys{TiingoToken: secret.New("a"), StooqAPIKey: secret.New("b")})
	if p == nil || c == nil || p.Name() != "tiingo" || c.Name() != "stooq" {
		t.Errorf("newPriceSources with both keys = %v, %v", p, c)
	}
	for _, k := range []config.PriceKeys{{}, {TiingoToken: secret.New("a")}, {StooqAPIKey: secret.New("b")}} {
		if p, c := newPriceSources(k); p != nil || c != nil {
			t.Errorf("newPriceSources without both keys gave sources")
		}
	}
}

func TestHistoryDaysReachesTheOldestHolding(t *testing.T) {
	d := day(t, "2026-11-24")
	if got := historyDays(d, nil); got != signals.DefaultHistoryDays {
		t.Errorf("no holdings: %d", got)
	}
	old := holdings.Holding{Opened: d.AddDate(-2, 0, 0)}
	if got, want := historyDays(d, []holdings.Holding{old}), 730; got != want {
		t.Errorf("a holding from two years back: %d, want %d", got, want)
	}
}

// TestAfterCloseRunsAnOptedInStrategyOverAStockAddedFromTheApp adds FFF to
// the watchlist from the phone app. The tap itself makes no order. The
// evening's run fetches FFF's prices, the strategy that opts in to the
// watchlist signals its breakout, and the buy waits for approval after
// passing the risk check, like any other.
func TestAfterCloseRunsAnOptedInStrategyOverAStockAddedFromTheApp(t *testing.T) {
	for _, c := range []struct {
		name   string
		extra  string
		orders []string
	}{
		{"opted in", "    include_watchlist: true\n",
			[]string{"2026-11-25 AAA buy awaiting_approval", "2026-11-25 FFF buy awaiting_approval"}},
		{"not opted in", "", []string{"2026-11-25 AAA buy awaiting_approval"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newACWith(t, at(t, "2026-11-24", 9, 0), 1, acStrategyWith(t, c.extra))
			for d := day(t, "2026-11-02"); !d.After(day(t, "2026-11-24")); d = d.AddDate(0, 0, 1) {
				if open, err := clock.NYSE().IsTradingDay(d); err != nil || !open {
					continue
				}
				date, c := d.Format(clock.DateLayout), "10.00"
				if date == "2026-11-24" {
					c = "12.00"
				}
				b := pricestest.Bar("FFF", date, c, c, c, c, 1000)
				h.primary.Add(b)
				h.check.Add(b)
			}
			h.recordCash(t, "2026-11-24", "1000.00")
			if _, err := limits.Watch(context.Background(), h.store, h.strategy, h.clock.Now(), "FFF"); err != nil {
				t.Fatal(err)
			}
			if got := h.orderText(t); len(got) != 0 {
				t.Fatalf("the tap made orders %q", got)
			}

			h.runAt(t, at(t, "2026-11-24", 16, 30))
			var fetched bool
			for _, call := range h.primary.Calls() {
				fetched = fetched || call.Symbol == "FFF"
			}
			if !fetched {
				t.Error("the after-close did not fetch the added stock's prices")
			}
			if got := h.orderText(t); !reflect.DeepEqual(got, c.orders) {
				t.Errorf("orders %q, want %q", got, c.orders)
			}
		})
	}
}
