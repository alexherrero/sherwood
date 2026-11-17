// Package rules is sherwood's rule library: the rule types a strategy in the
// strategy file can pick, each a pure function of a stock's daily bars, the
// holding if there is one, and the rule's own settings.
//
// A rule type holds no settings of its own. Parse reads a rule entry's
// settings from the strategy file's text, checks every name and range, and
// returns a rule ready to run. There are four kinds:
//
//   - Signal rules (moving_average_cross, breakout, pullback) look at one
//     stock and give one outcome: buy, add, trim, sell or no change.
//   - The ranking rule (momentum_ranking) looks at all of a strategy's stocks
//     together, because a rank only means something among the others, and
//     gives one outcome per stock.
//   - Stop rules (fixed_stop, trailing_stop) give the stop price for a
//     holding, and a sell when the stock has closed at or below it.
//   - Sizing rules (size_fixed_amount, size_by_risk) give a share count for
//     an order, rounded down through the caller's money.QuantityPolicy.
//
// Every outcome carries a short mechanical reason written by the rule itself,
// such as "closed at 105.00, above its 50-day high of 104.00".
//
// Signal and ranking rules signal on the day their condition starts to hold,
// not on every day it goes on holding: a breakout, a cross or a move into the
// top ranks is one signal, so a declined buy is not proposed again the next
// day. Stop rules are levels instead: a holding that has closed at or below
// its stop gets a sell on every day it is still held.
//
// Determinism: a rule reads nothing but its arguments. It never reads the
// clock, the network or a global, and ties are broken by symbol, so the same
// inputs always give the same outcome and the same reason. All maths is exact
// decimal arithmetic on Decimal() values. Averages and changes are compared by
// cross-multiplying, so no division is needed; a computed price comes back
// through money.RoundPrice and a share count through the caller's
// QuantityPolicy, which rounds down. No float type appears in this package,
// and a test fails if one does.
//
// The rule type names are the ones config already checks the strategy file
// against (config.RuleTypes). This package cannot be imported by config,
// because it reads config's types, so config keeps the one list of names and
// this package gives each name its implementation. A test fails if the two
// disagree in either direction.
package rules
