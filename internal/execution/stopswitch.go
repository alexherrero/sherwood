package execution

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// What the stop switch does to orders once it is on. Throwing the switch is
// the halt itself (halt.Switch.Throw), which the caller records first, outside
// any transaction. Then:
//
//   - StopBuys declines every buy awaiting approval and calls off every
//     approved buy not yet sent, in one transaction, with actor stop_switch.
//     Nothing about them is at the broker, so no call is made.
//   - CancelOpen cancels every open buy at the broker, one call each.
//   - On the second, separate confirmation, CancelOpen with Stops cancels
//     the resting protective stops instead, and sets aside approved stops
//     not yet sent. The caller first throws halt.StopsCancelled, so the
//     09:35 send places no new stop until the switch is reset.
//
// Sells awaiting approval are left alone: while the switch is on nothing is
// sent, and they expire with their session.

// Stopped is what StopBuys changed.
type Stopped struct {
	// Declined are the buys that were waiting for approval.
	Declined []orders.Order
	// Cancelled are the approved buys that had not been sent.
	Cancelled []orders.Order
}

// StopBuys declines every buy awaiting approval and calls off every approved
// buy not yet sent, with actor stop_switch and why as the reason. It writes in
// one transaction and passes any error through sw.OrderError after it, so
// call it with no writer transaction held. Running it again changes nothing.
func StopBuys(ctx context.Context, st *store.Store, sw *halt.Switch, c clock.Clock, log *slog.Logger,
	why string) (Stopped, error) {
	var res Stopped
	err := orders.Update(ctx, st, func(tx *sql.Tx) error {
		res = Stopped{}
		os, err := orders.InState(ctx, tx, orders.AwaitingApproval, orders.Approved)
		if err != nil {
			return err
		}
		now := c.Now()
		for _, o := range os {
			if o.Side != signals.SideBuy {
				continue
			}
			at := now
			if at.Before(o.Updated) {
				at = o.Updated
			}
			to := orders.Declined
			if o.State == orders.Approved {
				to = orders.Cancelled
			}
			moved, err := orders.Move(ctx, tx, o.ID, orders.Change{To: to, Actor: orders.StopSwitch, Reason: why, At: at})
			if err != nil {
				return err
			}
			if to == orders.Declined {
				res.Declined = append(res.Declined, moved)
			} else {
				res.Cancelled = append(res.Cancelled, moved)
			}
		}
		return nil
	})
	if err = sw.OrderError(ctx, err); err != nil {
		return Stopped{}, err
	}
	for _, o := range append(append([]orders.Order(nil), res.Declined...), res.Cancelled...) {
		log.Info("order stopped by the stop switch", "strategy", o.Strategy, "symbol", o.Symbol,
			"signal", string(o.Kind), "order_key", o.Key, "state", string(o.State), "reason", why)
	}
	return res, nil
}

// CancelRequest says which open orders CancelOpen cancels.
type CancelRequest struct {
	// Stops cancels the resting protective stops, and sets aside approved
	// stops not yet sent. Without it, the open buys are cancelled.
	Stops bool
	// Why is the reason recorded on each order.
	Why string
}

// Cancelled is what CancelOpen did.
type Cancelled struct {
	// Cancelled are the orders the broker confirmed cancelled.
	Cancelled []orders.Order
	// SetAside are approved stops that were never sent, now cancelled.
	SetAside []orders.Order
	// Unknown counts the orders whose cancellation the broker did not
	// confirm: each is now unknown, and trading halted.
	Unknown int
}

// CancelOpen cancels open orders at the broker with the stop switch as the
// actor: every open buy, or with req.Stops every resting protective stop. It
// makes exactly one Cancel call per order and never retries. An error or an
// answer other than cancelled moves that order to unknown and throws
// halt.UnknownOrder, and CancelOpen goes on to the next order, so every open
// order gets its one call. The error then wraps ErrHalted.
//
// It waits for a send or reconciliation in progress to finish first, so it
// never misses an order a send is placing.
func (e *Executor) CancelOpen(ctx context.Context, req CancelRequest) (Cancelled, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ctx = context.WithoutCancel(ctx) // a cancel half done is still recorded
	var res Cancelled
	open, err := orders.InState(ctx, e.store.Reader(), orders.Accepted, orders.PartiallyFilled)
	if err != nil {
		return res, err
	}
	var errs []error
	for _, o := range open {
		want := orders.DayLimitBuy
		if req.Stops {
			want = orders.Stop
		}
		if o.Type != want {
			continue
		}
		moved, err := e.cancel(ctx, o, orders.StopSwitch, req.Why)
		if err != nil {
			if errors.Is(err, ErrHalted) {
				res.Unknown++
			}
			errs = append(errs, err)
			continue
		}
		res.Cancelled = append(res.Cancelled, moved)
	}
	if req.Stops {
		approved, err := orders.InState(ctx, e.store.Reader(), orders.Approved)
		if err != nil {
			return res, errors.Join(append(errs, err)...)
		}
		for _, o := range approved {
			if o.Type != orders.Stop {
				continue
			}
			moved, err := e.move(ctx, o, orders.Change{To: orders.Cancelled, Actor: orders.StopSwitch,
				Reason: "Set aside before it was sent: " + req.Why})
			if err != nil {
				errs = append(errs, err)
				continue
			}
			res.SetAside = append(res.SetAside, moved)
		}
	}
	return res, errors.Join(errs...)
}
