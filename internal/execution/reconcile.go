package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/orders"
)

// Reconciliation compares sherwood's records with the broker's, through the
// adapter, so the same code serves the paper broker now and the live broker
// later. It runs at 09:00 ET on trading days and after the after-close
// attempt that succeeds. One run:
//
//  1. Settles every order whose answer was never recorded: each unknown order,
//     and each submitted order (a send that never got its answer; no send is
//     running, because a run holds the executor's lock). It makes one Lookup
//     call per order and never sends one again. The broker's answer moves the
//     order (actor reconciliation); a fill the broker reports is recorded at
//     the broker's fill price, and only when the order had no fill recorded,
//     so a price is never guessed.
//  2. Compares the holdings (shares per stock), the cash and the open orders
//     (which ones, their status and their filled shares) with the adapter's.
//
// Any difference throws halt.Reconciliation with every difference in the
// evidence; the broker's figures are never copied over sherwood's. A failed
// broker call throws a halt as every broker error does (unknown_order for a
// lookup, broker_error for a listing) and stops the run, which then returns
// an error wrapping ErrHalted.

// Reconciled is what one reconciliation found.
type Reconciled struct {
	// Settled are the orders a lookup settled, as they now stand.
	Settled []orders.Order
	// Differences are the differences found, one sentence each. Any
	// difference halted trading.
	Differences []string
}

// errDifference ends a settling transaction that found a difference instead
// of a state to record.
type errDifference struct{ what string }

func (e errDifference) Error() string { return e.what }

// Reconcile runs one reconciliation; see above. It waits for a send or a
// stop switch cancel in progress to finish first.
func (e *Executor) Reconcile(ctx context.Context) (*Reconciled, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	res := &Reconciled{}
	if err := e.settle(ctx, res); err != nil {
		return res, err
	}
	if err := e.compare(ctx, res); err != nil {
		return res, err
	}
	if len(res.Differences) == 0 {
		e.log.Info("reconciled with the broker", "reason", fmt.Sprintf("holdings, cash and open orders agree; %d orders settled by lookup", len(res.Settled)))
		return res, nil
	}
	evidence := "Reconciliation found these differences with the broker: " + strings.Join(res.Differences, " ")
	e.log.Error("reconciliation found differences: trading halts for review", "halt_cause", string(halt.Reconciliation),
		"reason", fmt.Sprintf("%d differences", len(res.Differences)))
	if _, _, err := e.sw.Throw(ctx, halt.Reconciliation, evidence); err != nil {
		return res, err
	}
	return res, nil
}

// settle looks up every order whose answer was never recorded.
func (e *Executor) settle(ctx context.Context, res *Reconciled) error {
	pending, err := orders.InState(ctx, e.store.Reader(), orders.Submitted, orders.Unknown)
	if err != nil {
		return fmt.Errorf("execution: %w", err)
	}
	_, err = e.settleOrders(ctx, res, pending)
	return err
}

// settleOrders looks up each of pending once and records the broker's
// answer. It returns the answers it could record, by order key.
func (e *Executor) settleOrders(ctx context.Context, res *Reconciled, pending []orders.Order) (map[string]broker.Report, error) {
	answers := map[string]broker.Report{}
	var errs []error
	for _, o := range pending {
		rep, err := call(ctx, e.timeout, func(ctx context.Context) (broker.Report, error) { return e.adapter.Lookup(ctx, o.Key) })
		if err != nil {
			evidence := describe(o) + " could not be looked up during reconciliation: " + err.Error()
			if o.State == orders.Submitted {
				errs = append(errs, e.unknown(ctx, o, evidence, err))
			} else {
				errs = append(errs, e.halt(ctx, halt.UnknownOrder, evidence, err))
			}
			continue
		}
		if rep.Ref != o.Key {
			res.Differences = append(res.Differences, describe(o)+": the broker's lookup answered about another order.")
			continue
		}
		settled, err := e.settleOne(ctx, o, rep)
		var diff errDifference
		switch {
		case errors.As(err, &diff):
			res.Differences = append(res.Differences, diff.what)
		case err != nil:
			return answers, errors.Join(append(errs, err)...)
		default:
			answers[o.Key] = rep
			res.Settled = append(res.Settled, settled)
			e.log.Info("order settled by lookup", "order_key", settled.Key, "symbol", settled.Symbol,
				"from_state", string(o.State), "state", string(settled.State))
		}
	}
	return answers, errors.Join(errs...)
}

// settleOne records the broker's answer about o in one transaction. A
// report it cannot record without guessing is an errDifference.
func (e *Executor) settleOne(ctx context.Context, o orders.Order, rep broker.Report) (orders.Order, error) {
	now := e.clock.Now()
	fillDate, err := e.fillDate(o, now)
	if err != nil {
		return orders.Order{}, err
	}
	reason := fmt.Sprintf("Reconciliation looked it up: the broker reports it %s.", rep.Status)
	var out orders.Order
	err = orders.Update(ctx, e.store, func(tx *sql.Tx) error {
		cur, err := orders.Get(ctx, tx, o.ID)
		if err != nil {
			return err
		}
		recorded, err := filled(ctx, tx, o.ID)
		if err != nil {
			return err
		}
		switch c := rep.Filled.Cmp(recorded); {
		case c < 0:
			return errDifference{fmt.Sprintf("%s: the broker reports %s shares filled, but sherwood has recorded %s.",
				describe(o), rep.Filled, recorded)}
		case c > 0:
			if rep.FillPrice == nil || !recorded.IsZero() {
				return errDifference{fmt.Sprintf("%s: the broker reports %s shares filled without a fill price sherwood can record.",
					describe(o), rep.Filled)}
			}
			cur, _, err = orders.RecordFill(ctx, tx, o.ID, orders.NewFill{Date: fillDate, Quantity: rep.Filled,
				Price: *rep.FillPrice, Actor: orders.Reconciliation, At: now,
				Reason: fmt.Sprintf("Found filled when reconciliation looked it up, at the broker's price of %s.", rep.FillPrice)})
			if err != nil {
				return err
			}
		}
		var to orders.State
		switch rep.Status {
		case broker.StatusAccepted:
			to = orders.Accepted
		case broker.StatusPartiallyFilled:
			to = orders.PartiallyFilled
		case broker.StatusFilled:
			to = orders.Filled
		case broker.StatusCancelled:
			to = orders.Cancelled
		case broker.StatusExpired:
			to = orders.Expired
		case broker.StatusRejected:
			to = orders.Rejected
		default:
			return errDifference{fmt.Sprintf("%s: the broker's lookup answered %q, which sherwood does not know.", describe(o), rep.Status)}
		}
		if cur.State == to {
			out = cur
			return nil
		}
		if to == orders.Filled || to == orders.PartiallyFilled || !orders.Allowed(cur.State, to, cur.Kind) {
			// A fill moves the order itself, so a report whose fills leave
			// the order short of its status, or a status the order cannot
			// reach from where it stands, disagrees with sherwood's records.
			return errDifference{fmt.Sprintf("%s: the broker reports it %s with %s shares filled, which does not fit its record (%s).",
				describe(o), rep.Status, rep.Filled, cur.State)}
		}
		out, err = orders.Move(ctx, tx, o.ID, orders.Change{To: to, Actor: orders.Reconciliation, Reason: reason, At: now})
		return err
	})
	if err = e.sw.OrderError(ctx, err); err != nil {
		return orders.Order{}, err
	}
	return out, nil
}

// fillDate is the trading date a fill found by lookup is recorded on: a day
// order's own session, or for a resting stop the latest trading date up to
// now.
func (e *Executor) fillDate(o orders.Order, now time.Time) (time.Time, error) {
	if o.Type != orders.Stop {
		return o.Date, nil
	}
	today := clock.DateOf(now.In(clock.Eastern))
	open, err := e.calendar.IsTradingDay(today)
	if err != nil {
		return time.Time{}, fmt.Errorf("execution: %w", err)
	}
	if open {
		return today, nil
	}
	prev, err := e.calendar.PreviousTradingDay(today)
	if err != nil {
		return time.Time{}, fmt.Errorf("execution: %w", err)
	}
	return prev, nil
}

func filled(ctx context.Context, q orders.Querier, id int64) (money.Quantity, error) {
	fs, err := orders.Fills(ctx, q, id)
	if err != nil {
		return money.Quantity{}, err
	}
	var total money.Quantity
	for _, f := range fs {
		total = total.Add(f.Quantity)
	}
	return total, nil
}

// compare compares holdings, cash and open orders with the adapter's.
func (e *Executor) compare(ctx context.Context, res *Reconciled) error {
	positions, err := call(ctx, e.timeout, e.adapter.Holdings)
	if err != nil {
		return e.brokerFailed(ctx, "Reading the broker's holdings for reconciliation failed: "+err.Error(), err)
	}
	brokerCash, err := call(ctx, e.timeout, e.cash)
	if err != nil {
		return e.brokerFailed(ctx, "Reading the broker's cash for reconciliation failed: "+err.Error(), err)
	}
	open, err := call(ctx, e.timeout, e.adapter.OpenOrders)
	if err != nil {
		return e.brokerFailed(ctx, "Listing the broker's open orders for reconciliation failed: "+err.Error(), err)
	}

	// Read sherwood's side in one read transaction, so it is one moment.
	tx, err := e.store.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("execution: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	ours := map[string]money.Quantity{}
	switch set, err := holdings.Current(ctx, tx); {
	case errors.Is(err, holdings.ErrNone):
	case err != nil:
		return fmt.Errorf("execution: %w", err)
	default:
		for _, h := range set.Holdings {
			ours[h.Symbol] = ours[h.Symbol].Add(h.Quantity)
		}
	}
	theirs := map[string]money.Quantity{}
	for _, p := range positions {
		theirs[p.Symbol] = theirs[p.Symbol].Add(p.Quantity)
	}
	for _, sym := range keys(ours, theirs) {
		a, b := ours[sym], theirs[sym]
		if !a.Equal(b) {
			res.Differences = append(res.Differences,
				fmt.Sprintf("%s: sherwood holds %s shares, the broker %s.", sym, a, b))
		}
	}

	var cash money.Money
	recorded := true
	switch c, err := holdings.LatestCash(ctx, tx); {
	case errors.Is(err, holdings.ErrNone):
		recorded = false
	case err != nil:
		return fmt.Errorf("execution: %w", err)
	default:
		cash = c.Amount
	}
	if !cash.Equal(brokerCash) {
		ourCash := "$" + cash.String()
		if !recorded {
			ourCash = "no cash"
		}
		res.Differences = append(res.Differences,
			fmt.Sprintf("Cash: sherwood has recorded %s, the broker $%s.", ourCash, brokerCash))
	}

	diffs, err := compareOpen(ctx, tx, open)
	if err != nil {
		return err
	}
	res.Differences = append(res.Differences, diffs...)
	return nil
}

// compareOpen compares the orders sherwood has open (or whose answer it never
// recorded) with the broker's open orders: which ones, their status and their
// filled shares. It returns the differences, one sentence each.
func compareOpen(ctx context.Context, tx orders.Querier, open []broker.Report) ([]string, error) {
	var diffs []string
	mine, err := orders.InState(ctx, tx, orders.Submitted, orders.Accepted, orders.PartiallyFilled, orders.Unknown)
	if err != nil {
		return nil, fmt.Errorf("execution: %w", err)
	}
	byRef := map[string]orders.Order{}
	for _, o := range mine {
		byRef[o.Key] = o
	}
	seen := map[string]bool{}
	for _, r := range open {
		seen[r.Ref] = true
		o, ok := byRef[r.Ref]
		if !ok {
			diffs = append(diffs,
				fmt.Sprintf("The broker has an open order (reference %s) that sherwood does not have open.", r.Ref))
			continue
		}
		got, err := filled(ctx, tx, o.ID)
		if err != nil {
			return nil, fmt.Errorf("execution: %w", err)
		}
		if !got.Equal(r.Filled) {
			diffs = append(diffs, fmt.Sprintf("%s: the broker reports %s shares filled, sherwood has recorded %s.",
				describe(o), r.Filled, got))
		}
		if want := openStatus(o.State); want != "" && r.Status != want {
			diffs = append(diffs, fmt.Sprintf("%s: it is %s in sherwood's records, %s at the broker.",
				describe(o), o.State, r.Status))
		}
	}
	for _, o := range mine {
		if !seen[o.Key] {
			diffs = append(diffs,
				fmt.Sprintf("%s: it is %s in sherwood's records, but the broker does not list it as open.", describe(o), o.State))
		}
	}
	return diffs, nil
}

// cash reads the broker's cash: on its own when the adapter can, otherwise
// from the account.
func (e *Executor) cash(ctx context.Context) (money.Money, error) {
	if c, ok := e.adapter.(broker.CashReader); ok {
		return c.Cash(ctx)
	}
	a, err := e.adapter.Account(ctx)
	return a.Cash, err
}

// openStatus is the broker status an open order in state s should have, or
// "" when sherwood does not know it (an order whose answer was never
// recorded).
func openStatus(s orders.State) broker.Status {
	switch s {
	case orders.Accepted:
		return broker.StatusAccepted
	case orders.PartiallyFilled:
		return broker.StatusPartiallyFilled
	}
	return ""
}

// keys returns the stocks held on either side with shares, sorted.
func keys(a, b map[string]money.Quantity) []string {
	var out []string
	for _, m := range []map[string]money.Quantity{a, b} {
		for k, q := range m {
			if !q.IsZero() {
				out = append(out, k)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
