package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/alexherrero/sherwood/internal/broker/paper"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/execution"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/proposal"
	"github.com/alexherrero/sherwood/internal/risk"
	"github.com/alexherrero/sherwood/internal/scheduler"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// The after-close job runs on every trading date once the session has
// closed. It fetches and cross-checks the date's bars, values the portfolio
// from the holdings and cash recorded by then, and runs every strategy's
// rules, recording prices, signals and holding suggestions for the date.
//
// The free sources may not have published the date's bars by 16:30, so the
// work is tried up to three times a date, as three scheduler jobs. The first
// attempt that finds the prices usable does the work and succeeds; later
// attempts on that date then skip. An attempt that finds them missing records
// the missing day, makes no signals and skips, saying when the next attempt
// is; after the last one the date simply has no signals. Nothing ever acts on
// a missing day.
//
// After downtime the scheduler replays the last few trading dates in order,
// all three attempts of one date before the next date. Each run fetches,
// checks and evaluates its own date's bars, and reads the holdings and cash
// as they were at 16:30 on that date, so a late run reads what an on-time
// run would have read.

// afterCloseAttempt is one scheduled try at a date's after-close work.
type afterCloseAttempt struct {
	name         string // job name; never change it once it has run
	hour, minute int    // US Eastern
}

// afterCloseAttempts are the tries, in order. The first is the 16:30 job; the
// retries leave room for the sources to publish late and stay well inside
// Tiingo's hourly request limit.
var afterCloseAttempts = []afterCloseAttempt{
	{"after-close", 16, 30},
	{"after-close-retry-1", 18, 0},
	{"after-close-retry-2", 21, 0},
}

// afterClose holds what the after-close job needs.
type afterClose struct {
	store    *store.Store
	clock    clock.Clock
	calendar *clock.Calendar
	log      *slog.Logger
	strategy *config.Strategy // loaded and validated at start-up
	halts    *halt.Switch     // thrown when the sources disagree on a held stock
	alerts   notify.Notifier  // says when buys are waiting; notify.Nowhere when off
	paper    *paper.Broker    // settles the day's paper fills; nil in live mode
	primary  prices.Source
	check    prices.Source
}

// newPriceSources builds the two price sources from the settings, or returns
// nils when either key is unset.
func newPriceSources(keys config.PriceKeys) (primary, check prices.Source) {
	if keys.TiingoToken.IsZero() || keys.StooqAPIKey.IsZero() {
		return nil, nil
	}
	return &prices.Tiingo{Token: keys.TiingoToken}, &prices.Stooq{Key: keys.StooqAPIKey}
}

// registerAfterClose registers the after-close job's attempts. Without both
// price keys every date would be missing, so the job is left off and start-up
// says so; once the keys are set, the next start catches up the last few
// dates.
func registerAfterClose(e env, sched *scheduler.Scheduler, keys config.PriceKeys, strategy *config.Strategy,
	db *store.Store, halts *halt.Switch, alerts notify.Notifier, paperBroker *paper.Broker, logger *slog.Logger) error {
	build := e.priceSources
	if build == nil {
		build = newPriceSources
	}
	primary, check := build(keys)
	if primary == nil || check == nil {
		logger.Warn("after-close job is off: no prices or signals will be recorded",
			"reason", "prices.tiingo_token and prices.stooq_api_key must both be set in the settings file")
		return nil
	}
	a := &afterClose{store: db, clock: e.clock, calendar: clock.NYSE(), log: logger, strategy: strategy,
		halts: halts, alerts: alerts, paper: paperBroker, primary: primary, check: check}
	return a.register(sched)
}

func (a *afterClose) register(sched *scheduler.Scheduler) error {
	if a.halts == nil {
		return errors.New("the after-close job needs the stop switch")
	}
	if _, err := a.proposer(a.log); err != nil {
		return err
	}
	for i, at := range afterCloseAttempts {
		err := sched.Register(scheduler.Job{
			Name:   at.name,
			Hour:   at.hour,
			Minute: at.minute,
			Inputs: func(ctx context.Context, r scheduler.Run) ([]byte, error) {
				in, err := a.gather(ctx, r)
				if err != nil {
					return nil, err
				}
				return json.Marshal(in)
			},
			Run: func(ctx context.Context, r scheduler.Run) error { return a.run(ctx, r, i) },
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// afterCloseInputs is everything a run reads besides the prices it fetches.
// Its JSON is what the scheduler hashes into the run's input hash, together
// with the trading date; the prices are recorded per date in price_days.
type afterCloseInputs struct {
	StrategyHash  string `json:"strategy_hash"`
	PrimarySource string `json:"primary_source"`
	CheckSource   string `json:"check_source"`
	Tolerance     string `json:"tolerance"`
	// HoldingsAsOf is when the holdings and cash were read as of.
	HoldingsAsOf string         `json:"holdings_as_of"`
	SnapshotID   int64          `json:"holdings_snapshot"` // 0 when none had been recorded
	Holdings     []holdingInput `json:"holdings"`
	CashID       int64          `json:"cash_reading"` // 0 when none had been recorded
	Cash         *money.Money   `json:"cash"`
	Held         []string       `json:"held"`
	Watched      []string       `json:"watched"`

	// Unexported fields are not encoded.
	set    holdings.Set   // the snapshot read
	cash   *holdings.Cash // the cash reading, or nil
	stocks prices.Stocks  // Held and Watched, for the fetch
}

type holdingInput struct {
	Symbol      string         `json:"symbol"`
	Quantity    money.Quantity `json:"quantity"`
	AverageCost money.Money    `json:"average_cost"`
	Opened      string         `json:"opened"`
}

// holdingsTime is the instant a run on date reads holdings and cash as of:
// the first attempt's time on that date.
func holdingsTime(date time.Time) time.Time {
	y, m, d := date.Date()
	first := afterCloseAttempts[0]
	return time.Date(y, m, d, first.hour, first.minute, 0, 0, clock.Eastern)
}

// gather reads the run's inputs.
func (a *afterClose) gather(ctx context.Context, r scheduler.Run) (*afterCloseInputs, error) {
	at := holdingsTime(r.TradingDate)
	q := a.store.Reader()
	in := &afterCloseInputs{
		StrategyHash:  a.strategy.Hash,
		PrimarySource: a.primary.Name(),
		CheckSource:   a.check.Name(),
		Tolerance:     a.strategy.Limits.PriceCrossCheckTolerance.String(),
		HoldingsAsOf:  store.FormatTime(at),
	}
	set, err := holdings.AsOf(ctx, q, at)
	switch {
	case errors.Is(err, holdings.ErrNone):
	case err != nil:
		return nil, err
	default:
		in.set, in.SnapshotID = set, set.ID
	}
	cash, err := holdings.CashAsOf(ctx, q, at)
	switch {
	case errors.Is(err, holdings.ErrNone):
	case err != nil:
		return nil, err
	default:
		in.cash, in.CashID, in.Cash = &cash, cash.ID, &cash.Amount
	}
	for _, h := range in.set.Holdings {
		in.Holdings = append(in.Holdings, holdingInput{Symbol: h.Symbol, Quantity: h.Quantity,
			AverageCost: h.AverageCost, Opened: store.FormatDate(h.Opened.In(clock.Eastern))})
		in.Held = append(in.Held, h.Symbol)
	}
	watched := slices.Clone(a.strategy.Watchlist)
	for _, def := range a.strategy.Strategies {
		watched = append(watched, def.Stocks...)
	}
	slices.Sort(watched)
	in.Watched = slices.Compact(watched)
	in.stocks = prices.Stocks{Held: in.Held, Watched: in.Watched}
	return in, nil
}

// run is one attempt at a date's work; attempt indexes afterCloseAttempts.
func (a *afterClose) run(ctx context.Context, r scheduler.Run, attempt int) error {
	log := a.log.With("job", r.Job, "trading_date", r.Date(), "input_hash", r.InputHash)
	if attempt > 0 {
		done, err := a.succeeded(ctx, r.Date())
		if err != nil {
			return err
		}
		if done {
			return scheduler.Skip("an earlier attempt already recorded this date's prices and signals")
		}
	}
	prop, err := a.proposer(log)
	if err != nil {
		return err
	}
	// The date's session is over, so its unapproved orders expire, whether
	// or not its prices are in yet.
	if _, err := prop.Expire(ctx, r.TradingDate); err != nil {
		return err
	}
	if _, err := execution.ExpireUnsent(ctx, a.store, a.halts, a.clock, log, r.TradingDate); err != nil {
		return err
	}
	in, err := a.gather(ctx, r)
	if err != nil {
		return err
	}
	if in.set.OverLimit {
		log.Warn("more holdings than the limit allows", "reason",
			fmt.Sprintf("%d holdings, limit %d", len(in.set.Holdings), holdings.Limit))
	}

	f := &prices.Fetcher{Primary: a.primary, Check: a.check, Clock: a.clock, Calendar: a.calendar,
		Store: a.store, Log: log, HistoryDays: historyDays(r.TradingDate, in.set.Holdings)}
	day, err := f.FetchDay(ctx, r.TradingDate, in.stocks, a.strategy.Limits.PriceCrossCheckTolerance)
	if err != nil {
		return err
	}
	if day.Halt() {
		// Trading halts for your review. The rules still run below, without
		// the disagreeing stocks, and record their signals.
		log.Error("held stock's price sources disagree: trading halts for review",
			"halt_cause", string(halt.PriceDisagreement), "symbol", strings.Join(day.HaltSymbols(), ", "))
		if _, _, err := a.halts.Throw(ctx, halt.PriceDisagreement, disagreement(day)); err != nil {
			return err
		}
	}
	if !day.Usable {
		if next := attempt + 1; next < len(afterCloseAttempts) {
			at := afterCloseAttempts[next]
			return scheduler.Skip(fmt.Sprintf("prices not usable yet, so no signals; trying again at %d:%02d: %s",
				at.hour, at.minute, day.Detail))
		}
		return scheduler.Skip("prices not usable, so this date has no signals: " + day.Detail)
	}
	// The day's paper fills settle first, so the valuation and the rules see
	// the holdings after them.
	if err := a.settle(ctx, log, r, in); err != nil {
		return err
	}

	if err := a.value(ctx, log, day, in); err != nil {
		return err
	}
	runner := &signals.Runner{Store: a.store, Clock: a.clock, Log: log}
	res, err := runner.Run(ctx, r.TradingDate, a.strategy, in.set.Holdings)
	if err != nil {
		return err
	}
	if res.Missing != "" {
		return fmt.Errorf("the rules found the prices missing after they were checked: %s", res.Missing)
	}
	_, err = prop.Propose(ctx, proposal.Day{Date: r.TradingDate, CatchUp: r.CatchUp, Holdings: in.set, Cash: in.cash})
	return err
}

// proposer builds the step that turns the date's signals into orders and
// expires the unapproved ones, logging through log.
func (a *afterClose) proposer(log *slog.Logger) (*proposal.Proposer, error) {
	return proposal.New(proposal.Config{Store: a.store, Clock: a.clock, Calendar: a.calendar, Strategy: a.strategy,
		Checker: risk.Standard{}, Switch: a.halts, Notifier: a.alerts, BrokerHealthy: paperBrokerHealthy, Logger: log})
}

// paperBrokerHealthy is the broker's health for the proposal's preview check.
// The paper broker always answers; the execution layer checks the real
// adapter's health again just before it sends.
func paperBrokerHealthy(context.Context) bool { return true }

// disagreement is a price halt's evidence: the date, and each held stock
// whose sources disagreed with what differed.
func disagreement(day *prices.Day) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Price check for %s (%s against %s): the sources disagree on a stock you hold.",
		day.DateString(), day.PrimarySource, day.CheckSource)
	for _, c := range day.Checks {
		if c.Halt {
			fmt.Fprintf(&b, " %s: %s.", c.Symbol, strings.TrimSuffix(c.Detail, "."))
		}
	}
	return b.String()
}

// succeeded reports whether any attempt has already succeeded for date.
func (a *afterClose) succeeded(ctx context.Context, date string) (bool, error) {
	var n int
	err := a.store.Reader().QueryRowContext(ctx,
		`SELECT count(*) FROM job_runs WHERE trading_date = ? AND outcome = 'succeeded' AND job_name IN (?, ?, ?)`,
		date, afterCloseAttempts[0].name, afterCloseAttempts[1].name, afterCloseAttempts[2].name).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("reading earlier attempts: %w", err)
	}
	return n > 0, nil
}

// historyDays is how many calendar days of history to fetch before date:
// enough for the longest rule look-back, and back to the oldest holding's
// opening so a trailing stop sees every close since the buy.
func historyDays(date time.Time, held []holdings.Holding) int {
	days := signals.DefaultHistoryDays
	for _, h := range held {
		since := clock.DateOf(date).Sub(clock.DateOf(h.Opened)).Round(24*time.Hour) / (24 * time.Hour)
		days = max(days, int(since))
	}
	return days
}

// value logs the portfolio's value at the date's close: each holding at its
// checked close, plus the cash reading. A holding without an agreed close
// leaves the portfolio unvalued rather than valued wrongly.
func (a *afterClose) value(ctx context.Context, log *slog.Logger, day *prices.Day, in *afterCloseInputs) error {
	if in.SnapshotID == 0 && in.cash == nil {
		log.Info("portfolio not valued", "reason", "no holdings or cash have been imported yet")
		return nil
	}
	bars, err := prices.UsableBars(ctx, a.store.Reader(), day.Date)
	if err != nil {
		return err
	}
	closes := make(map[string]money.Price, len(bars))
	for _, b := range bars {
		closes[b.Symbol] = b.Close
	}
	var total money.Money
	var unpriced []string
	for _, h := range in.set.Holdings {
		c, ok := closes[h.Symbol]
		if !ok || day.Excluded(h.Symbol) {
			unpriced = append(unpriced, h.Symbol)
			continue
		}
		total = total.Add(h.Value(c))
	}
	if len(unpriced) > 0 {
		log.Warn("portfolio not valued", "reason", "no agreed close for "+strings.Join(unpriced, ", "))
		return nil
	}
	how := fmt.Sprintf("%d holdings at the %s close", len(in.set.Holdings), day.DateString())
	if in.cash != nil {
		total = total.Add(in.cash.Amount)
		how += " plus cash"
	} else {
		how += "; no cash reading recorded"
	}
	log.Info("portfolio valued", "amount", total, "reason", how)
	return nil
}
