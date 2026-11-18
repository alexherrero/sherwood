// Package risk is sherwood's risk check: the one gate every order passes
// immediately before it is sent, and that a buy also passes when it is
// proposed, so the approval card can show whether it would go through.
//
// The check is a pure function. Check(Order, Snapshot) reads nothing but its
// arguments: the snapshot carries the limits, holdings, cash, the session's
// orders so far, the portfolio's value now and at the previous close, the
// last closes, the watchlist, the sectors, the stop switch and broker state,
// the time and the exchange calendar. The same inputs always give the same
// Verdict.
//
// The verdict lists every check that applies to the order, each with a pass
// or the refusal reason in plain words. An order passes only if every listed
// check passes; a verdict with no checks never passes, and a snapshot left at
// its zero value refuses every order.
//
// Buys and ordinary sells (trims and rule exits) pass the design's full
// list. Protective sells, the stop-loss and trailing-stop sells that rest at
// the broker, pass a shorter list on purpose: shares held (no more than held)
// and the market-open check, which a resting stop always passes. The daily
// loss limit, the order count, the stop switch and the order value never
// block them, so a sell that cuts a loss goes through on a bad day, and a
// holding that has grown past the order-value limit keeps a stop for every
// share. Shares held already bounds a protective sell.
//
// The execution layer takes a Checker and cannot be built without one.
// Standard is the real check.
package risk
