package execution

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/store"
)

// ExpireUnsent moves every approved order whose session is through or
// earlier to expired: it was approved but never sent, because the switch was
// on, the 09:35 send did not run, or it was approved after the send. An
// approval lasts only until the end of its session, so it is never sent
// later. It returns the orders it expired; running it again expires nothing
// more. Every move goes through sw.OrderError.
func ExpireUnsent(ctx context.Context, st *store.Store, sw *halt.Switch, c clock.Clock, log *slog.Logger,
	through time.Time) ([]orders.Order, error) {
	last := clock.DateOf(through)
	now := c.Now()
	var expired []orders.Order
	err := orders.Update(ctx, st, func(tx *sql.Tx) error {
		expired = expired[:0]
		approved, err := orders.InState(ctx, tx, orders.Approved)
		if err != nil {
			return err
		}
		for _, o := range approved {
			if o.Date.After(last) {
				continue
			}
			at := now
			if at.Before(o.Updated) {
				at = o.Updated
			}
			o, err = orders.Move(ctx, tx, o.ID, orders.Change{To: orders.Expired, Actor: orders.Expiry, At: at,
				Reason: "It was approved but not sent during its trading day, " + o.Date.Format(clock.DateLayout) + "."})
			if err != nil {
				return err
			}
			expired = append(expired, o)
		}
		return nil
	})
	if err = sw.OrderError(ctx, err); err != nil {
		return nil, err
	}
	for _, o := range expired {
		log.Info("order expired unsent", "strategy", o.Strategy, "symbol", o.Symbol, "signal", string(o.Kind),
			"order_key", o.Key, "trading_date", o.Date.Format(clock.DateLayout), "from_state", string(orders.Approved),
			"state", string(o.State))
	}
	return expired, nil
}
