package proposal

import (
	"context"
	"database/sql"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/orders"
)

// Expire declines every buy and ordinary sell still waiting for approval, or
// never checked, whose trading day is through or earlier: an approval lasts
// only until the end of the day it was made for. It returns how many it
// declined. Run it after the session has closed; running it again declines
// nothing more.
//
// Protective sells are left alone: they never wait for approval. So are
// approved orders, which the execution layer sends or expires.
func (p *Proposer) Expire(ctx context.Context, through time.Time) (int, error) {
	last := clock.DateOf(through)
	now := p.cfg.Clock.Now()
	type expired struct {
		order orders.Order
		from  orders.State
	}
	var declined []expired
	err := orders.Update(ctx, p.cfg.Store, func(tx *sql.Tx) error {
		declined = declined[:0]
		waiting, err := orders.InState(ctx, tx, orders.Proposed, orders.AwaitingApproval)
		if err != nil {
			return err
		}
		for _, o := range waiting {
			if o.Protective() || o.Date.After(last) {
				continue
			}
			at := now
			if at.Before(o.Updated) {
				at = o.Updated // a clock stepped back never refuses the move
			}
			from := o.State
			o, err = orders.Move(ctx, tx, o.ID, orders.Change{To: orders.Declined, Actor: orders.Expiry,
				Reason: "It was not approved by the end of its trading day, " + o.Date.Format(clock.DateLayout) + ".",
				At:     at})
			if err != nil {
				return err
			}
			declined = append(declined, expired{o, from})
		}
		return nil
	})
	if err = p.cfg.Switch.OrderError(ctx, err); err != nil {
		return 0, err
	}
	for _, e := range declined {
		o := e.order
		p.cfg.Logger.Info("order expired unapproved", "strategy", o.Strategy, "symbol", o.Symbol,
			"signal", string(o.Kind), "order_key", o.Key, "trading_date", o.Date.Format(clock.DateLayout),
			"from_state", string(e.from), "state", string(o.State))
	}
	return len(declined), nil
}
