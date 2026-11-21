package feed

import "time"

// StaleAfter is how long an upload's items stay fresh. The research job runs
// nightly, so a feed older than this means at least one night was missed,
// usually because the Mac was asleep. Past it, the app shows a quiet note on
// the Feed view and drops back to the rules' own reasons on Portfolio and the
// approval cards. Nothing else changes: trading never reads the feed, and a
// stale feed sends no alert.
const StaleAfter = 36 * time.Hour

// Fresh reports whether an upload taken in at at is still fresh at now. An
// upload exactly StaleAfter old is still fresh; one moment older is stale.
func Fresh(at, now time.Time) bool {
	return now.Sub(at) <= StaleAfter
}
