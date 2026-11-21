package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexherrero/sherwood/internal/broker/paper"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/execution"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/risk"
	"github.com/alexherrero/sherwood/internal/scheduler"
	"github.com/alexherrero/sherwood/internal/store"
)

// sendJob is the 09:35 ET job that sends the session's approved orders and
// keeps the resting stops, and reconcileJob the 09:00 ET reconciliation with
// the broker. Never rename either once it has run: job_runs keys on the name.
const (
	sendJob      = "send-orders"
	reconcileJob = "reconcile"
)

// The 15-minute checks, interval jobs inside market hours, and the alert
// group they share. Never rename them once they have run.
const (
	openOrdersCheckJob = "open-orders-check"
	dailyLossCheckJob  = "daily-loss-check"
	checksAlertGroup   = "intraday-checks"
	checkEvery         = 15 * time.Minute
)

// trading is what registerTrading built. Both are nil in live mode.
type trading struct {
	// paper settles the day's paper fills after the close.
	paper *paper.Broker
	// exec sends, cancels and reconciles through the broker adapter.
	exec *execution.Executor
}

// registerTrading builds the paper broker and the execution layer, and
// registers the 09:00 reconciliation and the 09:35 send job. In live mode
// there is no adapter to send through until live trading is built, so it
// registers nothing and says so, and it returns nothing.
func registerTrading(e env, sched *scheduler.Scheduler, mode config.Mode, strategy *config.Strategy,
	db *store.Store, halts *halt.Switch, logger *slog.Logger) (trading, error) {
	if mode != config.ModePaper {
		logger.Warn("orders are not sent", "mode", string(mode),
			"reason", "live mode has no broker adapter yet, so the 09:35 send and 09:00 reconciliation jobs are off")
		return trading{}, nil
	}
	cal := clock.NYSE()
	pb, err := paper.New(paper.Config{Store: db, Clock: e.clock, Calendar: cal,
		Policy: money.PolicyFor(strategy.Limits.FractionalShares)})
	if err != nil {
		return trading{}, err
	}
	ex, err := execution.New(execution.Config{Store: db, Clock: e.clock, Calendar: cal, Adapter: pb,
		Checker: risk.Standard{}, Switch: halts, Strategy: strategy, Logger: logger})
	if err != nil {
		return trading{}, err
	}
	err = sched.Register(scheduler.Job{
		Name:   reconcileJob,
		Hour:   9,
		Minute: 0,
		// One alert a date, should retries ever be added.
		AlertGroup: reconcileJob,
		Run: func(ctx context.Context, r scheduler.Run) error {
			if r.CatchUp {
				return scheduler.Skip("this morning has passed; the next reconciliation compares the records as they are then")
			}
			return reconcile(ctx, ex)
		},
	})
	if err != nil {
		return trading{}, err
	}
	err = sched.Register(scheduler.Job{
		Name:   sendJob,
		Hour:   9,
		Minute: 35,
		Run: func(ctx context.Context, r scheduler.Run) error {
			return send(ctx, ex, logger.With("job", r.Job, "trading_date", r.Date()), r)
		},
	})
	return trading{paper: pb, exec: ex}, err
}

// registerChecks registers the 15-minute checks through ex: the broker's open
// orders against sherwood's, and the day's loss from the broker's account
// value against the daily loss limit in force. Both run every 15 minutes
// inside market hours on trading days, never make up a missed slot, and share
// one failure alert a date. Each halts trading itself on what it finds; a run
// fails when a broker call failed (which halted trading too) or the loss
// could not be measured.
//
// Live mode registers them once its adapter is built (live-trading task 4);
// paper mode does not, because the paper broker's open orders and value are
// sherwood's own records.
func registerChecks(sched *scheduler.Scheduler, ex *execution.Executor) error {
	err := sched.Register(scheduler.Job{
		Name:       openOrdersCheckJob,
		Every:      checkEvery,
		AlertGroup: checksAlertGroup,
		Run: func(ctx context.Context, _ scheduler.Run) error {
			_, err := ex.CheckOpenOrders(ctx)
			return err
		},
	})
	if err != nil {
		return err
	}
	return sched.Register(scheduler.Job{
		Name:       dailyLossCheckJob,
		Every:      checkEvery,
		AlertGroup: checksAlertGroup,
		Run: func(ctx context.Context, r scheduler.Run) error {
			_, err := ex.CheckDailyLoss(ctx, r.TradingDate)
			return err
		},
	})
}

// reconcile runs one reconciliation. A difference halts trading and is not
// an error; a broker call that failed halted trading too, and fails the run.
func reconcile(ctx context.Context, ex *execution.Executor) error {
	_, err := ex.Reconcile(ctx)
	return err
}

// send is one 09:35 run. A run for a session that has passed, or made once
// the market has shut, sends nothing: a send never goes into the wrong
// session, and the session's approvals expire with it.
func send(ctx context.Context, ex *execution.Executor, log *slog.Logger, r scheduler.Run) error {
	if r.CatchUp {
		return scheduler.Skip("this session has passed, so nothing was sent")
	}
	res, err := ex.Send(ctx, r.TradingDate)
	if errors.Is(err, execution.ErrNotOpen) {
		return scheduler.Skip("the market is not open now, so nothing was sent")
	}
	if res != nil {
		log.Info("orders sent", "reason", fmt.Sprintf("%d sent, %d blocked by the risk check, %d held by the stop switch, %d stops called off",
			len(res.Sent), len(res.Blocked), len(res.Held), len(res.Called)))
	}
	return err
}

// settle works the date's open paper orders against its checked bars, once
// the after-close attempt has them, and hands the holdings and cash after the
// fills to the rest of the attempt. It does nothing in live mode. A failed
// settlement halts trading, because the records can no longer be trusted.
func (a *afterClose) settle(ctx context.Context, log *slog.Logger, r scheduler.Run, in *afterCloseInputs) error {
	if a.paper == nil {
		return nil
	}
	bars, err := prices.UsableBars(ctx, a.store.Reader(), r.TradingDate)
	if err != nil {
		return err
	}
	res, err := a.paper.Settle(ctx, r.TradingDate, bars)
	if err != nil {
		_, _, herr := a.halts.Throw(ctx, halt.Reconciliation,
			fmt.Sprintf("Settling the paper fills for %s failed: %v", r.Date(), err))
		return errors.Join(err, herr)
	}
	for _, f := range res.Fills {
		log.Info("paper order filled", "order_key", f.Order.Key, "symbol", f.Order.Symbol,
			"side", string(f.Order.Side), "quantity", f.Fill.Quantity, "price", f.Fill.Price,
			"state", string(f.Order.State), "reason", f.Fill.Reason)
	}
	for _, w := range res.Unfilled {
		log.Info("paper order not filled", "order_key", w.Order.Key, "symbol", w.Order.Symbol, "reason", w.Reason)
	}
	for _, o := range res.Expired {
		log.Info("paper order expired unfilled", "order_key", o.Key, "symbol", o.Symbol, "state", string(o.State))
	}
	for _, w := range res.Skipped {
		log.Warn("paper order not worked", "order_key", w.Order.Key, "symbol", w.Order.Symbol, "reason", w.Reason)
	}
	if res.Holdings != nil {
		in.set, in.SnapshotID = *res.Holdings, res.Holdings.ID
		in.cash, in.CashID, in.Cash = res.Cash, res.Cash.ID, &res.Cash.Amount
	}
	return nil
}
