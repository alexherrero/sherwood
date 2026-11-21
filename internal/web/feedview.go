package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/alexherrero/sherwood/internal/feed"
	"github.com/alexherrero/sherwood/internal/limits"
)

// The research feed in the app: the Feed view, and the pieces Portfolio and
// the approval cards borrow from it.
//
// Everything in the feed was written by an AI from web pages, so every
// string here is untrusted. It reaches the page only through html/template's
// own escaping, as plain strings: never as template.HTML, template.URL or any
// other type that switches the escaping off. A link gets an href only when it
// is http or https.

// staleAfterText says feed.StaleAfter in words, for the quiet note.
var staleAfterText = fmt.Sprintf("%d hours", int(feed.StaleAfter/time.Hour))

// feedData is what the Feed view shows.
type feedData struct {
	// Result is the outcome of the last watchlist tap, if any.
	Result *notice
	// OptedIn names the strategies whose rules also run over the watchlist,
	// so an added stock can get a buy signal from them.
	OptedIn []string
	// HasUpload is false until a feed has been uploaded.
	HasUpload bool
	// Arrived is when the latest upload arrived, and Accepted and Dropped
	// how many of its items were kept and dropped.
	Arrived  string
	Accepted int
	Dropped  int
	// Stale is true when there is no upload, or the latest is older than
	// feed.StaleAfter. StaleAfter says that threshold in words.
	Stale      bool
	StaleAfter string
	// Candidates are the latest upload's candidate cases, in its order.
	Candidates []feedItemView
}

// feedItemView is one feed item as a page shows it.
type feedItemView struct {
	Symbol string
	// Text is the AI's words, shown as escaped plain text.
	Text    string
	Sources []sourceView
	// Arrived is when the upload holding the item arrived.
	Arrived string
	// Watched is true when a candidate is already on the watchlist in
	// force, so the Feed view shows no "Add to watchlist" button.
	Watched bool
	// NeedsSector is true when a sector limit is in force and the strategy
	// file gives the candidate no sector, so the tap would be refused; the
	// view says how to add it instead.
	NeedsSector bool
}

// sourceView is one source link as a page shows it.
type sourceView struct {
	Title string
	// Href is the link, or "" when it is not an http or https link with a
	// host, in which case the title shows without a link.
	Href string
	// Domain is the link's host name in lower case, shown beside the
	// title so you can see where a claim came from.
	Domain string
	// Unusual is true when the host name has letters outside plain ASCII,
	// which can make one site's name look like another's.
	Unusual bool
}

// loadFeed reads the Feed view's data in one read transaction.
func loadFeed(s *Server, r *http.Request) (any, error) {
	ctx := r.Context()
	tx, err := s.store.Reader().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	d := feedData{Stale: true, StaleAfter: staleAfterText}
	if res, ok := watchResults[r.URL.Query().Get("watch")]; ok {
		d.Result = &res
	}
	for _, def := range s.strategy.Strategies {
		if def.IncludeWatchlist {
			d.OptedIn = append(d.OptedIn, def.Name)
		}
	}
	up, err := feed.LatestUpload(ctx, tx)
	if err != nil {
		return nil, err
	}
	if up == nil {
		return d, nil
	}
	d.HasUpload = true
	d.Arrived = when(up.At)
	d.Accepted, d.Dropped = up.Accepted, up.Dropped
	d.Stale = !feed.Fresh(up.At, s.clock.Now())
	candidates, err := feed.LatestCandidates(ctx, tx)
	if err != nil {
		return nil, err
	}
	watched, err := limits.Watchlist(ctx, tx, s.strategy)
	if err != nil {
		return nil, err
	}
	lim, err := limits.Effective(ctx, tx, s.strategy)
	if err != nil {
		return nil, err
	}
	for _, c := range candidates {
		v := itemView(c)
		v.Watched = slices.Contains(watched, c.Symbol)
		v.NeedsSector = !v.Watched && lim.MaxSectorShare != nil && s.strategy.Sectors[c.Symbol] == ""
		d.Candidates = append(d.Candidates, v)
	}
	return d, nil
}

// itemView turns a stored item into what a page shows.
func itemView(it feed.Stored) feedItemView {
	v := feedItemView{Symbol: it.Symbol, Text: it.Text, Arrived: when(it.UploadedAt)}
	for _, src := range it.Sources {
		v.Sources = append(v.Sources, sourceOf(src))
	}
	return v
}

// sourceOf works out a source's link and domain. Check has already dropped
// any item with a link that is not http or https, but the page checks again
// rather than trust what is stored.
func sourceOf(src feed.Source) sourceView {
	v := sourceView{Title: src.Title}
	u, err := url.Parse(src.URL)
	if err != nil || u.User != nil {
		return v
	}
	if scheme := strings.ToLower(u.Scheme); scheme != "http" && scheme != "https" {
		return v
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return v
	}
	v.Href = src.URL
	v.Domain = host
	for _, c := range host {
		if c > 127 {
			v.Unusual = true
			break
		}
	}
	return v
}

// freshItem is the view of an item when its upload is still fresh at now,
// or nil when it is stale and the page falls back to the rules' own words.
func freshItem(it *feed.Stored, now time.Time) *feedItemView {
	if it == nil || !feed.Fresh(it.UploadedAt, now) {
		return nil
	}
	v := itemView(*it)
	return &v
}
