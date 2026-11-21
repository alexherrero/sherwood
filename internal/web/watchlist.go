package web

import (
	"errors"
	"net/http"

	"github.com/alexherrero/sherwood/internal/limits"
)

// The watchlist tap: "Add to watchlist" on a candidate case in the Feed view.
//
// The tap only records the stock in settings_changes (limits.Watch). It
// creates no order and no signal. From the next after-close on, the stock's
// prices are fetched, the research export lists it, the risk check counts it
// as watched, and every strategy whose strategy file sets
// include_watchlist: true runs its rules over it. Any buy those rules make
// is proposed, risk-checked and waits for approval like any other. The
// addition lasts until the strategy file changes, as a limit's tightening
// does.
const (
	watchPath = "/watchlist/add"
	// symbolField is the tap form's one field: the stock to add.
	symbolField = "symbol"
)

// watchResults are the words Feed shows after a tap, picked by the ?watch=
// value. The value only picks fixed words; it never changes anything.
var watchResults = map[string]notice{
	"added": {Class: "notice--info", Title: "Added to the watchlist.",
		Body: "From the next evening's run, its prices are fetched and the strategies that run over the watchlist look at it. Any buy they suggest waits for your approval like any other. It stays watched until the strategy file changes."},
	"watched": {Class: "notice--info", Title: "Already on the watchlist.", Body: "Nothing was changed."},
	"nosector": {Class: "notice--warning", Title: "Not added: the stock has no sector.",
		Body: "A sector limit is in force, and the risk check needs every watched stock's sector. Give it a sector under sectors: in the strategy file on the NAS, add it to watchlist: there too, and restart."},
	"invalid": {Class: "notice--warning", Title: "Not added.", Body: "That is not a stock symbol."},
}

// mountWatchlist registers the tap. Adding a stock widens what the rules may
// propose to buy and what the risk check lets through as watched, so like
// every other change to trading it is limited to the approval devices.
func (s *Server) mountWatchlist() {
	s.HandleChange(watchPath, http.HandlerFunc(s.watch), ApprovalDevicesOnly)
}

func backToFeed(w http.ResponseWriter, r *http.Request, result string) {
	http.Redirect(w, r, "/feed?watch="+result, http.StatusSeeOther)
}

// watch records one stock added to the watchlist.
func (s *Server) watch(w http.ResponseWriter, r *http.Request) {
	c, err := limits.Watch(r.Context(), s.store, s.strategy, s.clock.Now(), r.PostForm.Get(symbolField))
	switch {
	case err == nil:
		s.logger.Warn("added to the watchlist", "symbol", c.New, "change_source", string(limits.Phone))
		backToFeed(w, r, "added")
	case errors.Is(err, limits.ErrWatched):
		backToFeed(w, r, "watched")
	case errors.Is(err, limits.ErrNoSector):
		backToFeed(w, r, "nosector")
	case errors.Is(err, limits.ErrNotSymbol):
		backToFeed(w, r, "invalid")
	default:
		refuse(w, r, "the handler failed", http.StatusInternalServerError,
			"The stock could not be added, so the watchlist is unchanged. Try again.")
	}
}
