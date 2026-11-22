package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/store"
)

// Settling by hand is how you record a fill that reconciliation can't: most
// often a partial fill that grew, where the broker gives the filled count and
// one average price for all of it, so the new shares' own price would be a
// guess. You read the new shares' price in the broker's app and run
// "sherwood settle-order" on the NAS, which calls SettleByHand. It records
// the fill through the order state machine (actor operator) and moves the
// holdings and cash with it as a fills snapshot (holdings.Book), in one
// transaction, exactly as a fill reconciliation records. It calls no broker
// and resets no halt: the next check looks the order up, finds its fills
// match the broker's, and settles the rest of its status. If the new shares
// filled at more than one price, run it once per price, each time with the
// running total.

// ErrHandSettle wraps every reason SettleByHand refused. Whatever the
// reason, it wrote nothing.
var ErrHandSettle = errors.New("settle-order: nothing was written")

// HandFill is what you read in the broker's app.
type HandFill struct {
	// Filled is the order's total filled shares once this fill is counted:
	// the broker app's filled count, not the increment.
	Filled money.Quantity
	// Price is the price the new shares filled at, a whole number of cents.
	Price money.Price
}

// HandSettled is what SettleByHand recorded.
type HandSettled struct {
	// Order is the order as it now stands.
	Order orders.Order
	// Fill is the fill recorded: Filled less what was recorded before.
	Fill orders.Fill
	// Before is the filled shares sherwood had recorded before.
	Before money.Quantity
	// Holdings and Cash are the fills snapshot written with it.
	Holdings holdings.Set
	Cash     holdings.Cash
}

// SettleByHand records f for the order ref, which is the order's number (as
// halt evidence names it, "Order 12 (...)") or its key, as of now. It
// refuses, with nothing written:
//
//   - an order it can't find, or one whose state can't take a fill;
//   - a filled count at or below what is already recorded, or above the
//     order's quantity;
//   - a zero price (a price finer than a cent never parses);
//   - for a limit buy, a price above its limit, which no broker fills at;
//   - a sell the holdings can't take, or a snapshot older than the newest.
func SettleByHand(ctx context.Context, st *store.Store, cal *clock.Calendar, now time.Time,
	ref string, f HandFill) (HandSettled, error) {
	if f.Price.IsZero() {
		return HandSettled{}, fmt.Errorf("%w: a fill needs a price above zero", ErrHandSettle)
	}
	var out HandSettled
	err := orders.Update(ctx, st, func(tx *sql.Tx) error {
		o, err := findOrder(ctx, tx, ref)
		if err != nil {
			return err
		}
		recorded, err := filled(ctx, tx, o.ID)
		if err != nil {
			return err
		}
		if f.Filled.Cmp(recorded) <= 0 {
			return refused("%s: sherwood has already recorded %s shares filled, so a filled count of %s adds nothing.",
				describe(o), recorded, f.Filled)
		}
		if f.Filled.Cmp(o.Quantity) > 0 {
			return refused("%s: a filled count of %s is more than the order's %s shares.",
				describe(o), f.Filled, o.Quantity)
		}
		if o.Type == orders.DayLimitBuy && o.Price != nil && f.Price.Cmp(*o.Price) > 0 {
			return refused("%s: $%s is above the order's limit of $%s, and a limit buy never fills above its limit.",
				describe(o), f.Price, *o.Price)
		}
		inc, err := f.Filled.Sub(recorded)
		if err != nil {
			return err
		}
		date, err := fillDateOn(cal, o, now)
		if err != nil {
			return err
		}
		at := now
		if at.Before(o.Updated) {
			at = o.Updated
		}
		cur, fill, err := orders.RecordFill(ctx, tx, o.ID, orders.NewFill{Date: date, Quantity: inc, Price: f.Price,
			Actor: orders.Operator, At: at,
			Reason: fmt.Sprintf("Settled by hand with settle-order: %s more shares at $%s, as the broker's app shows, for %s filled in all.",
				inc, f.Price, f.Filled)})
		var te *orders.TransitionError
		switch {
		case errors.As(err, &te):
			return refused("%s: it is %s, which can't take a fill.", describe(o), o.State)
		case errors.Is(err, orders.ErrOverfill):
			return refused("%s: %v.", describe(o), err)
		case err != nil:
			return err
		}
		set, cash, err := recordInBook(ctx, tx, o, inc, f.Price, date, at)
		if err != nil {
			var diff errDifference
			if errors.As(err, &diff) {
				return refused("%s", diff.what)
			}
			return err
		}
		out = HandSettled{Order: cur, Fill: fill, Before: recorded, Holdings: set, Cash: cash}
		return nil
	})
	if err != nil {
		return HandSettled{}, err
	}
	return out, nil
}

// findOrder reads the order ref names: all digits is its number, anything
// else its key.
func findOrder(ctx context.Context, q orders.Querier, ref string) (orders.Order, error) {
	var (
		o   orders.Order
		err error
	)
	if id, perr := strconv.ParseInt(ref, 10, 64); perr == nil {
		o, err = orders.Get(ctx, q, id)
	} else {
		o, err = orders.ByKey(ctx, q, ref)
	}
	if errors.Is(err, orders.ErrNotFound) {
		return orders.Order{}, refused("no order has the number or key %q.", ref)
	}
	return o, err
}

func refused(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrHandSettle, fmt.Sprintf(format, args...))
}
