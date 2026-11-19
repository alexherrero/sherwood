// Package execution is the execution layer: the one place orders are sent to
// the broker.
//
// It cannot be built without a risk check (New refuses a nil risk.Checker),
// and every order passes that check immediately before its own send, with
// the session's earlier sends counted, so the order count, cash and
// same-day rules see them.
//
// # The send window
//
// Send runs once a trading day, at 09:35 US Eastern, for that day's session,
// and refuses to run while the market is shut. It works the session's
// approved orders in a fixed order:
//
//  1. Ordinary sells (trims and rule exits), unless the stop switch is on.
//     Before each one goes, the holding's resting stop is resized to the
//     shares that will be left, or called off when none will, so the stop
//     and the sell never cover more shares than are held.
//  2. Protective stops, even while the switch is on. Each holding keeps one
//     resting stop for all its shares. Its price is the highest of the stop
//     resting now, any protective sell proposed for the session, and the
//     stop the strategies' stop rules gave the holding at the last close, so
//     a trailing rule's raise after the close is applied here and a stop
//     never moves down. When the price or the share count changes, the old
//     stop is called off and then the new one sent, never the other way
//     round, so two stops never cover the same shares.
//  3. Buys, unless the stop switch is on.
//
// An order the risk check refuses moves to `blocked` with the refusal as its
// reason and the verdict stored. An order the switch holds back stays
// `approved`; the end-of-day expiry calls it off.
//
// # Sending
//
// A send is two moves. The order moves to `submitted` and that is committed
// before the broker is called, so a crash mid-call leaves it `submitted` for
// reconciliation to look up. The broker is then called exactly once, under a
// time limit. An acceptance moves the order to `accepted`. A refusal moves it
// to `rejected` and halts trading. Any error, time-out or unclear answer
// moves it to `unknown` in its own transaction and then halts trading, and
// the run stops there: nothing is ever retried, and nothing more is sent
// until you reset the switch.
//
// A failed call to call off a resting stop does the same to that stop. An
// illegal state change halts trading through halt.Switch.OrderError.
package execution
