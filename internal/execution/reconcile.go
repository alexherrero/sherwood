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
	"github.com/alexherrero/sherwood/internal/signals"
)

// Reconciliation compares sherwood's records with the broker's, through the
// adapter, so the same code serves the paper broker and the live broker. It
// runs at 09:00 ET on trading days and after the after-close attempt that
// succeeds. One run:
//
//  1. Lists the broker's open orders.
//  2. Settles every order whose answer was never recorded (each unknown order,
//     and each submitted order: a send that never got its answer; no send is
//     running, because a run holds the executor's lock), and every order
//     sherwood has open that the broker no longer lists (most often one that
//     filled, or a resting stop the broker cancelled or expired). It makes one
//     Lookup call per order and never sends one again. The broker's answer
//     moves the order (actor reconciliation); a fill the broker reports is
//     recorded at the broker's fill price, and only when the order had no
//     fill recorded, so a price is never guessed. A recorded fill changes the
//     holdings and cash in the same transaction, as a new snapshot with
//     source fills (holdings.Book), so the records follow the fills.
//  3. Compares the holdings (shares per stock), the cash and the open orders
//     (which ones, their status and their filled shares) with the adapter's.
//
// Any difference throws halt.Reconciliation with every difference in the
// evidence; the broker's figures are never copied over sherwood's. A resting
// stop the broker cancelled or expired is a difference too: the holding has
// no stop at the broker until the next 09:35 send places a new one (at a
// price never below the lost stop's), so trading halts and you are told. A failed
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
	open, err := e.settleListed(ctx, res, "reconciliation")
	if err != nil {
		return res, err
	}
	if err := e.compare(ctx, res, open); err != nil {
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

// settleListed lists the broker's open orders, then settles, by one Lookup
// each, every order whose answer sherwood never recorded and every order
// sherwood has open that the broker no longer lists. It returns the open
// orders to compare: the listing, plus any order the listing missed but
// whose lookup still finds open (it was placed or moved between the two
// calls). what names the run in a failed listing's evidence.
func (e *Executor) settleListed(ctx context.Context, res *Reconciled, what string) ([]broker.Report, error) {
	open, err := call(ctx, e.timeout, e.adapter.OpenOrders)
	if err != nil {
		return nil, e.brokerFailed(ctx, "Listing the broker's open orders for "+what+" failed: "+err.Error(), err)
	}
	listed := map[string]bool{}
	for _, r := range open {
		listed[r.Ref] = true
	}
	mine, err := orders.InState(ctx, e.store.Reader(),
		orders.Submitted, orders.Unknown, orders.Accepted, orders.PartiallyFilled)
	if err != nil {
		return nil, fmt.Errorf("execution: %w", err)
	}
	var pending []orders.Order
	for _, o := range mine {
		if o.State == orders.Submitted || o.State == orders.Unknown || !listed[o.Key] {
			pending = append(pending, o)
		}
	}
	answers, err := e.settleOrders(ctx, res, pending)
	if err != nil {
		return nil, err
	}
	for _, o := range pending {
		if rep, ok := answers[o.Key]; ok && !listed[o.Key] &&
			(rep.Status == broker.StatusAccepted || rep.Status == broker.StatusPartiallyFilled) {
			open = append(open, rep)
		}
	}
	return open, nil
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
		settled, err := e.settleOne(ctx, o, rep, orders.Reconciliation, "Found filled when reconciliation looked it up")
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
			if lost := lostStop(o, settled); lost != "" {
				e.log.Error("a resting stop is no longer at the broker", "order_key", settled.Key, "symbol", settled.Symbol,
					"state", string(settled.State), "reason", "the next 09:35 send places a new one; trading halts for review")
				res.Differences = append(res.Differences, lost)
			}
		}
	}
	return answers, errors.Join(errs...)
}

// lostStop says, as a difference, that a resting stop the broker had has
// gone without filling: the broker cancelled or expired it (a good-till-
// cancelled order does not rest for ever), or it was cancelled in the
// broker's own app. Sherwood never sends a second order from a check, so the
// holding has no stop at the broker until the next 09:35 send places a new
// one, at a price no lower than this one's. It returns "" for anything else.
func lostStop(was, now orders.Order) string {
	if was.Type != orders.Stop || (now.State != orders.Cancelled && now.State != orders.Expired) {
		return ""
	}
	price := "no price"
	if now.Price != nil {
		price = "$" + now.Price.String()
	}
	return fmt.Sprintf("%s: the resting stop at %s is %s at the broker, so %s has no stop there until the next 09:35 send places a new one, at no less than %s.",
		describe(now), price, now.State, now.Symbol, price)
}

// settleOne records the broker's answer about o in one transaction, with
// actor as the actor of the move and how opening a fill's reason. A fill
// changes the holdings and cash in the same transaction (holdings.Book). A
// report it cannot record without guessing is an errDifference, and then
// nothing is written.
func (e *Executor) settleOne(ctx context.Context, o orders.Order, rep broker.Report, actor orders.Actor, how string) (orders.Order, error) {
	now := e.clock.Now()
	fillDate, err := e.fillDate(o, now)
	if err != nil {
		return orders.Order{}, err
	}
	reason := fmt.Sprintf("Reconciliation looked it up: the broker reports it %s.", rep.Status)
	if actor != orders.Reconciliation {
		reason = fmt.Sprintf("The broker reports it %s.", rep.Status)
	}
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
				Price: *rep.FillPrice, Actor: actor, At: now,
				Reason: fmt.Sprintf("%s, at the broker's price of %s.", how, rep.FillPrice)})
			if errors.Is(err, orders.ErrOverfill) {
				return errDifference{fmt.Sprintf("%s: the broker reports %s shares filled, more than the order's %s.",
					describe(o), rep.Filled, o.Quantity)}
			}
			if err != nil {
				return err
			}
			if err := recordInBook(ctx, tx, o, rep.Filled, *rep.FillPrice, fillDate, now); err != nil {
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
		out, err = orders.Move(ctx, tx, o.ID, orders.Change{To: to, Actor: actor, Reason: reason, At: now})
		return err
	})
	if err = e.sw.OrderError(ctx, err); err != nil {
		return orders.Order{}, err
	}
	return out, nil
}

// recordInBook applies a fill of o to the holdings and cash, and writes them
// as a new snapshot with source fills as of now, inside tx. A sell of shares
// sherwood does not hold, or a snapshot that would land before one already
// recorded, is an errDifference: the records and the broker disagree, and a
// guess would hide it.
func recordInBook(ctx context.Context, tx *sql.Tx, o orders.Order, qty money.Quantity, price money.Price,
	date, now time.Time) error {
	bk, err := holdings.ReadBook(ctx, tx)
	if err != nil {
		return err
	}
	if o.Side == signals.SideBuy {
		bk.Buy(o.Symbol, qty, price, date)
	} else if err := bk.Sell(o.Symbol, qty, price); err != nil {
		return errDifference{fmt.Sprintf("%s: the broker reports %s shares sold at $%s, which sherwood's holdings cannot take: %v.",
			describe(o), qty, price, err)}
	}
	if _, _, err := bk.Write(ctx, tx, holdings.SourceFills, now); err != nil {
		if errors.Is(err, holdings.ErrOutOfOrder) {
			return errDifference{fmt.Sprintf("%s: its fill could not be written to the holdings: %v.", describe(o), err)}
		}
		return err
	}
	return nil
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

// compare compares holdings, cash and open orders with the adapter's; open
// is the broker's open orders, from settleListed.
func (e *Executor) compare(ctx context.Context, res *Reconciled, open []broker.Report) error {
	positions, err := call(ctx, e.timeout, e.adapter.Holdings)
	if err != nil {
		return e.brokerFailed(ctx, "Reading the broker's holdings for reconciliation failed: "+err.Error(), err)
	}
	brokerCash, err := call(ctx, e.timeout, e.cash)
	if err != nil {
		return e.brokerFailed(ctx, "Reading the broker's cash for reconciliation failed: "+err.Error(), err)
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
