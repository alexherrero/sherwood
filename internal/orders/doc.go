// Package orders is the order record: orders made from signals, the state
// machine they move through, the append-only history of every move, and
// their fills (migration 0006).
//
// The state machine is one table, in transitions.go. Allowed and Next read
// it; Move and RecordFill refuse any move it does not list with a
// *TransitionError wrapping ErrIllegalTransition, which callers turn into a
// halt. Each order's idempotency key is signals.Key of its strategy, stock,
// side, kind and trading date, held unique by the schema, so Create on a
// key that exists returns the existing order instead of making a second.
//
// Every write runs on a transaction the caller owns (Update opens one on the
// store's writer), so an order's row, its history and its fills change
// together or not at all. Money, prices and share counts are the exact
// money types; nothing here uses floating point.
package orders
