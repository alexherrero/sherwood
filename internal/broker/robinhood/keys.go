package robinhood

import (
	"context"
	"fmt"

	"github.com/alexherrero/sherwood/internal/orders"
)

// sentStates are the states of an order that may have reached the broker:
// every state from submitted on. An order sherwood has already finished is
// included, so an order the broker still lists as open after sherwood called
// it off is named by its key, not as a stranger.
var sentStates = []orders.State{
	orders.Submitted, orders.Accepted, orders.PartiallyFilled, orders.Unknown,
	orders.Filled, orders.Cancelled, orders.Rejected, orders.Expired,
}

// OrderKeys returns the Keys that read every sent order's key from the orders
// table through q (the store's reader).
func OrderKeys(q orders.Querier) Keys {
	return func(ctx context.Context) ([]string, error) {
		os, err := orders.InState(ctx, q, sentStates...)
		if err != nil {
			return nil, fmt.Errorf("robinhood: %w", err)
		}
		keys := make([]string, len(os))
		for i, o := range os {
			keys[i] = o.Key
		}
		return keys, nil
	}
}
