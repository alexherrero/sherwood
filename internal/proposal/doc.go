// Package proposal turns a trading date's signals into orders, and expires
// the orders nobody approved in time.
//
// It runs inside the after-close attempt that succeeds, right after the
// rules have recorded the date's signals, so a late or missing price day
// never produces a proposal. Each signal becomes at most one order, for the
// next trading session after the signal's date:
//
//   - a buy or add is a day limit buy at the date's close times
//     1 + buy_limit_margin, sized by the strategy's sizing rule at that limit
//     with the initial stop its stop rules give a new holding bought there;
//   - a trim (half the holding, rounded down by the share policy) or a rule's
//     exit (the whole holding) is a sell at the next open;
//   - a protective sell (stop_sell) is a stop order for the whole holding at
//     the stop its stop rules give, which skips approval.
//
// Every order passes the risk check at once, as a preview for its session. A
// buy or an ordinary sell that passes waits for your approval; a protective
// sell that passes is approved; any order the check refuses is blocked, with
// the refusals as its reason. The verdict, one line per check, is stored on
// the order either way.
//
// An order's trading date is the session it trades in, not the date of the
// signal it came from, so the session's approvals expire with it. Running
// the proposal twice for a date finds the orders the first run made, by
// their idempotency key, and changes nothing.
//
// A catch-up run, one for a date whose own day has gone, proposes no buy or
// add, and no run proposes a buy, add or ordinary sell once its session has
// opened: the approval would be for a session that is under way or over.
// Protective sells are always proposed, because they protect a holding.
//
// When new buys are waiting, one notification says how many, and nothing
// else. Expire declines every buy and ordinary sell still waiting for
// approval once its trading day has ended.
package proposal
