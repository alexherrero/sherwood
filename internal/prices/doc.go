// Package prices fetches daily price bars from two free end-of-day sources,
// cross-checks them, and records which bars the rules may use.
//
// A Source returns split-adjusted daily bars for one stock over a range of
// trading dates. Sherwood has two: Tiingo, the primary, whose bars the rules
// use, and Stooq, the cross-check. The Fetcher asks both for each held or
// watched stock on one trading date and fails closed:
//
//   - If either source gives no bar for that date for any stock, the day is
//     missing, and no bar of that date is usable for anyone. A missing day
//     means no signals, never signals on stale prices.
//   - If the two sources' open, high, low or close for a stock differ by more
//     than the strategy file's price_cross_check_tolerance (as a share of the
//     primary's value), that stock is excluded and gets no signals that day.
//     A held stock that disagrees is also flagged for a halt; the halt itself
//     belongs to the order side.
//
// Prices are parsed from the sources' text and never pass through a float.
// Trading dates come from the injected clock and the NYSE calendar.
//
// What is stored lives in three tables from migration 0002: price_bars (every
// bar as fetched), price_days (one row per checked date) and price_checks (one
// row per stock per checked date). The usable_bars view is the single
// definition of a usable bar; UsableBars reads it.
package prices
