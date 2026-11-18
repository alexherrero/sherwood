// Package halt is the stop switch: the record of every halt, the switch's
// state, and its reset (migration 0007).
//
// Anything that finds a reason to stop trading calls Switch.Throw with a
// cause and the evidence. The halt is recorded, logged and pushed to your
// phone as notify.TradingHalted, which carries no detail; the cause and
// evidence are on the Today view. The switch stays on until you reset it
// from an approval device with a reason, and the reset is recorded too.
//
// A second throw while the switch is on adds its own record and keeps the
// first. A throw that repeats an active halt's cause and evidence exactly,
// such as a broker helper failing the same way on each restart, adds nothing
// and sends no second notification.
//
// Whatever decides whether to send an order reads the switch with Active.
// The switch stops sending, never the rules: while it is on, the rules still
// run and record their signals, so you can see what they would have done.
//
// The causes wired today are a broker helper failure (the supervisor's
// OnError), a disagreement between the price sources on a held stock (the
// after-close job) and an illegal order state change (OrderError, which
// every caller of orders.Move and orders.RecordFill puts its result
// through). Later tasks throw the rest of the design's causes with Throw.
package halt
