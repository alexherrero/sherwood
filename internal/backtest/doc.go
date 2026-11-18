// Package backtest replays past daily bars through the rule library and the
// paper fill model, one strategy at a time, and reports each strategy's
// return, maximum drawdown, win rate and turnover.
//
// It uses the same rule functions as the daily run (internal/rules) and the
// same fill model as paper mode (internal/fill), so a back-test shows what
// paper trading would have done with the same prices. It is a replay of past
// prices, not a forecast, and nothing it reports is financial advice.
//
// # Each trading day
//
// The calendar is every date on which any of the strategy's stocks has a bar.
// On each date D in the requested range, in this order:
//
//  1. Orders made after the previous date's close are worked against D's bar
//     with the fill model: sells at the open first, then resting stops on the
//     holdings that were already held, then buy limits. There is no look-ahead:
//     a signal from D's close can fill on the next date at the earliest.
//  2. Every holding is valued at its latest close, which gives the day's
//     value for the drawdown and turnover figures.
//  3. After D's close the rules run on the bars up to and including D, and
//     their decisions become the next date's orders.
//
// # Combining a strategy's rules
//
// Each stock with a bar on D gets one action, picked from its rules'
// decisions in this order, first match wins:
//
//  1. A stop rule's sell (the holding closed at or below its stop).
//  2. Any other rule's sell.
//  3. Any rule's trim. A trim sells half the holding, rounded down under the
//     share policy; if that is no shares it does nothing.
//  4. Any rule's buy or add.
//  5. No change.
//
// So a stop sell comes first, any sell beats a buy, and a trim beats a buy.
// The reason recorded is the first rule's, in the file's rule order, that
// gave the winning outcome. The combining itself is rules.Set.Combine, which
// the rule runner (internal/signals) also uses, so a back-test and a live
// day decide alike; the trim size and the orders are this package's own.
//
// Stop rules run on held stocks only. The highest stop any of them gives is
// the holding's stop, resting from the next date. A new holding has no stop
// on the day it is bought; the stop rules set it after that day's close.
//
// A buy or add is a day limit order at the latest close times one plus the
// strategy file's buy_limit_margin, rounded to the cent. It is sized by the
// strategy's sizing rule at that limit, with the strategy's capital (the
// starting capital times its portfolio_share, cut to the cent) as Capital.
// The stop passed to the sizing rule is the one the strategy's stop rules give
// a holding bought at the limit on D. A strategy with no sizing rule makes no
// buys.
//
// # Limits
//
// Each strategy trades its own cash and nothing else, with no margin: buys
// are taken in symbol order and a buy whose shares at its limit cost more
// than the cash not yet promised to earlier buys is blocked. A buy of a stock
// not yet held is blocked if it would take the strategy past the strategy
// file's max_holdings (at most 20), counting stocks with a sell pending as
// still held. The other risk-check limits (order value, stock share, sector,
// order count, daily loss) are not applied in a back-test.
//
// # Figures
//
//   - Return is the final value (cash plus holdings at their last close)
//     against the strategy's capital.
//   - Maximum drawdown is the largest fall from a previous high in the daily
//     value, as a share of that high.
//   - Win rate is the share of sells that made money against the average
//     cost of the shares sold. A trim and a stop each count as a sell.
//     Holdings still open at the end are not counted.
//   - Turnover is the value bought plus the value sold, halved, over the
//     average daily value: 1.00 means the strategy traded its whole value
//     once.
//
// Every figure is worked out in exact decimals; divisions keep twelve places
// and the report rounds to two, halves away from zero.
package backtest
