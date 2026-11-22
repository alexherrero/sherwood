package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/prices"
)

// The 15-minute checks. In live mode the scheduler runs both every 15
// minutes inside market hours (an interval job, scheduler.Job.Every), through
// the executor's adapter:
//
//   - CheckOpenOrders compares the broker's open orders with sherwood's, using
//     reconciliation's comparison, and halts on a difference.
//   - CheckDailyLoss reads the broker's account value and halts trading
//     (halt.DailyLoss) once the loss since the previous close reaches the
//     daily loss limit in force. It halts rather than declines, so nothing
//     more is sent until you reset the switch.
//
// A failed broker call in either halts trading as a broker error, once for
// as long as that halt is active (halt.Switch.ThrowOnce): an expired broker
// session fails every check until you log in again, and gives one halt and
// one push, not one every 15 minutes.

// ErrLossUnknown means the day's loss cannot be measured: the portfolio's
// value at the previous close is not known (a holding has no close), or no
// daily loss limit is set. The check fails, and its job alerts you; the risk
// check refuses every order that needs the limit in the meantime.
var ErrLossUnknown = errors.New("execution: the day's loss cannot be measured")

// CheckOpenOrders is the 15-minute open-order check. It lists the broker's
// open orders, then settles by one Lookup each, exactly as reconciliation
// does, every order whose answer sherwood never recorded and every order
// sherwood has open that the broker no longer lists (most often one that has
// filled since the last check), and then compares the open orders with
// reconciliation's comparison. Holdings and cash are left to the full
// reconciliation at 09:00 and after the close.
//
// Any difference throws halt.Reconciliation with every difference in the
// evidence. A failed listing halts as a broker error (once while that halt
// is active); a failed lookup halts as reconciliation's does. Either way the
// error wraps ErrHalted. Nothing is ever placed or cancelled.
func (e *Executor) CheckOpenOrders(ctx context.Context) (*Reconciled, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	res := &Reconciled{}
	open, err := e.settleListed(ctx, res, "the 15-minute check")
	if err != nil {
		return res, err
	}

	tx, err := e.store.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return res, fmt.Errorf("execution: %w", err)
	}
	diffs, err := compareOpen(ctx, tx, open)
	_ = tx.Rollback()
	if err != nil {
		return res, err
	}
	res.Differences = append(res.Differences, diffs...)
	if len(res.Differences) == 0 {
		e.log.Info("open orders agree with the broker", "reason",
			fmt.Sprintf("%d open at the broker; %d orders settled by lookup", len(open), len(res.Settled)))
		return res, nil
	}
	evidence := "The 15-minute check found these differences with the broker's open orders: " + strings.Join(res.Differences, " ")
	e.log.Error("open orders differ from the broker's: trading halts for review", "halt_cause", string(halt.Reconciliation),
		"reason", fmt.Sprintf("%d differences", len(res.Differences)))
	if _, _, err := e.sw.Throw(ctx, halt.Reconciliation, evidence); err != nil {
		return res, err
	}
	return res, nil
}

// Loss is what one daily loss check measured.
type Loss struct {
	// PreviousDate is the trading date whose close the loss is measured
	// from.
	PreviousDate time.Time
	// Previous is the portfolio's value at that close: the cash and the
	// holdings in sherwood's records, each holding at its close, the same
	// figure the risk check uses at the send.
	Previous money.Money
	// Value is the account's value at the broker now (Account().Value).
	Value money.Money
	// Loss is Previous less Value; it is negative on a day that gained.
	Loss money.Money
	// Limit is the daily loss limit in force (limits.Effective).
	Limit money.Money
	// Reached is true when Loss reaches Limit, so trading was halted.
	Reached bool
}

// CheckDailyLoss is the 15-minute daily loss check for session's date. It
// reads the account's value from the broker and the previous close's value
// and the limit in force from the store. When the loss reaches the limit (the
// same rule as the risk check: a loss equal to the limit reaches it), it
// throws halt.DailyLoss with the figures as evidence, once for as long as
// that halt is active. A failed broker call halts as a broker error (once
// while that halt is active) and returns an error wrapping ErrHalted; a loss
// that cannot be measured returns ErrLossUnknown and halts nothing.
func (e *Executor) CheckDailyLoss(ctx context.Context, session time.Time) (Loss, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var l Loss
	prev, err := e.calendar.PreviousTradingDay(clock.DateOf(session))
	if err != nil {
		return l, fmt.Errorf("execution: %w", err)
	}
	l.PreviousDate = prev
	acct, err := call(ctx, e.timeout, e.adapter.Account)
	if err != nil {
		return l, e.brokerFailed(ctx, "Reading the account for the 15-minute daily loss check failed: "+err.Error(), err)
	}
	l.Value = acct.Value

	known, err := e.previousAndLimit(ctx, &l)
	if err != nil {
		return l, err
	}
	switch {
	case !l.Limit.IsPositive():
		return l, fmt.Errorf("%w: the daily loss limit (daily_loss_limit) is not set", ErrLossUnknown)
	case !known || !l.Previous.IsPositive():
		return l, fmt.Errorf("%w: the portfolio's value at the close of %s is not known", ErrLossUnknown,
			prev.Format(clock.DateLayout))
	}
	l.Loss = l.Previous.Sub(l.Value)
	if l.Loss.Cmp(l.Limit) < 0 {
		e.log.Info("daily loss under the limit", "amount", l.Loss.String(),
			"reason", fmt.Sprintf("the limit in force is $%s", l.Limit))
		return l, nil
	}
	l.Reached = true
	evidence := fmt.Sprintf("The account is worth $%s at the broker, down $%s from $%s at the close of %s, which reaches the $%s daily loss limit.",
		l.Value, l.Loss, l.Previous, prev.Format(clock.DateLayout), l.Limit)
	e.log.Error("the daily loss limit is reached: trading halts", "halt_cause", string(halt.DailyLoss),
		"amount", l.Loss.String())
	if _, _, err := e.sw.ThrowOnce(ctx, halt.DailyLoss, evidence); err != nil {
		return l, err
	}
	return l, nil
}

// previousAndLimit reads, in one read transaction, the portfolio's value at
// l.PreviousDate's close into l.Previous and the daily loss limit in force
// into l.Limit. known is false when a holding has no close by then.
func (e *Executor) previousAndLimit(ctx context.Context, l *Loss) (known bool, err error) {
	tx, err := e.store.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, fmt.Errorf("execution: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	set, err := holdings.Current(ctx, tx)
	if err != nil && !errors.Is(err, holdings.ErrNone) {
		return false, fmt.Errorf("execution: %w", err)
	}
	var cash money.Money
	switch c, err := holdings.LatestCash(ctx, tx); {
	case errors.Is(err, holdings.ErrNone):
	case err != nil:
		return false, fmt.Errorf("execution: %w", err)
	default:
		cash = c.Amount
	}
	closes, err := prices.LatestCloses(ctx, tx, l.PreviousDate)
	if err != nil {
		return false, fmt.Errorf("execution: %w", err)
	}
	lim, err := limits.Effective(ctx, tx, e.strategy)
	if err != nil {
		return false, fmt.Errorf("execution: %w", err)
	}
	l.Limit = lim.DailyLossLimit
	l.Previous, known = previousValue(set, cash, closes)
	return known, nil
}
