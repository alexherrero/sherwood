// Package signals is the rule runner: it runs each strategy's rules for one
// trading date and records what they say.
//
// A run is a pure function of the date's checked prices, the holdings, the
// strategy file and the trading date (Evaluate), followed by one write
// (Runner.Run). It records:
//
//   - A signal for each stock whose combined verdict is a buy, add, trim,
//     sell or stop sell, with the winning rule's reason, the strategy file's
//     hash and an idempotency key built from the strategy, stock, side,
//     signal kind and trading date.
//   - A suggestion for each holding a strategy trades: hold, add, trim or
//     sell, with its reason and the highest stop the strategy's stop rules
//     give it.
//
// A strategy's rules are combined by rules.Set.Combine, the same code the
// back-tester uses: a stop rule's sell, then any other sell, a trim, a buy or
// add, then no change, with the reason of the first rule in the file's order
// that gave the winning action.
//
// # Prices
//
// A date whose prices were never checked, or were checked and found missing,
// gives no signals and no suggestions at all; Plan.Missing says why. On a
// usable date, a stock whose sources disagreed, or that was not checked, is
// excluded: it gets no signal and no suggestion, though it stays in a
// ranking rule's field, ending on its last bar before the date, so it is
// reported as unranked rather than changing the other stocks' ranks. The
// rules read the primary source's stored bars, which for an agreed stock end
// on exactly the bar the usable_bars view gives for the date.
//
// # Idempotency
//
// Both tables are append-only and keyed (signals on the idempotency key and
// on its five fields, suggestions on strategy, stock and date). A second run
// of the same date inserts nothing that is already there, so two runs on the
// same inputs leave a single row each. If the inputs changed between runs,
// rows the first run wrote stand, and anything new is added beside them.
//
// Holdings belong to every strategy whose stocks list them; there is no
// record yet of which strategy bought what. The stop in force is not known
// until paper-trading records it, so Holding.Stop is nil for now.
package signals
