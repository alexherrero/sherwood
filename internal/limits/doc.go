// Package limits works out the risk check's effective limits: the strategy
// file's limits, tightened by any tightenings made from the phone app since
// the strategy file last changed.
//
// The limits' values live in the strategy file on the NAS. From the phone
// app you may only tighten a limit, and the change applies at once; a
// loosening is always a deliberate edit of the file at the NAS. Every change
// is recorded in settings_changes with its time and its source:
//
//   - Record runs at start-up. When the strategy file's hash differs from the
//     newest hash recorded, it adds a row for the hash (setting
//     strategy_file.hash, source nas) and one row for each risk limit whose
//     file value differs from the newest value recorded from the file.
//   - Tighten records one tightening (source phone), refusing anything that is
//     not strictly tighter than the limit in force.
//   - Effective reads the limits in force: each limit is the tighter of the
//     file's value and the phone's tightenings recorded after the newest
//     hash row, and only when that row names the file loaded now. Once the
//     file changes, the old tightenings no longer apply.
//
// Only the risk check's limits can be tightened: the largest stock share, the
// largest order value, the most orders a day, the daily loss limit, the most
// holdings, the largest sector share and the largest limit distance. For
// every one of them a lower value is tighter. The strategy file's other
// settings (the buy limit margin, fractional shares, the price cross-check
// tolerance and the fewest paper weeks) are shown but only change at the NAS.
//
// The watchlist works the same way. Watch records a stock added from the
// Feed view's "Add to watchlist" tap (setting watchlist.add, source phone),
// and Watchlist reads the watchlist in force: the file's watchlist followed
// by the additions recorded after the newest hash row, while that row names
// the file loaded now. Watchlist is the one reader of the watchlist for the
// proposal, the approval re-check, the 09:35 send, the after-close price
// fetch and the research export. An addition creates no order; a strategy
// whose file sets include_watchlist runs its rules over the watchlist in
// force (config.Strategy.WithWatchlist), and any buy goes through the risk
// check and approval like any other. This package must never import the
// feed package, so the feed itself cannot reach trading.
//
// Every value is exact decimal text; nothing passes through a float.
package limits
