package web

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/feed"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every symbol, link, title and reason below is made up.

// saveFeed stores items as one upload that arrived at arrived, as the upload
// route does once Check has passed them. It skips Check on purpose, so a test
// can store what Check would drop and see that the page still holds.
func (f *fixture) saveFeed(arrived time.Time, items ...feed.Item) {
	f.t.Helper()
	if _, err := feed.Save(context.Background(), f.store.Writer(), arrived, feed.Feed{Items: items}); err != nil {
		f.t.Fatal(err)
	}
}

func src(url, title string) []feed.Source { return []feed.Source{{URL: url, Title: title}} }

func candidate(symbol, text string, sources ...feed.Source) feed.Item {
	return feed.Item{Kind: feed.Candidate, Symbol: symbol, Text: text, Sources: sources}
}

func note(symbol, text string, sources ...feed.Source) feed.Item {
	return feed.Item{Kind: feed.HoldingNote, Symbol: symbol, Text: text, Sources: sources}
}

func explanation(key, symbol, text string, sources ...feed.Source) feed.Item {
	return feed.Item{Kind: feed.SignalExplanation, Symbol: symbol, SignalKey: key, Text: text, Sources: sources}
}

// recordSuggestion writes a holding suggestion the way the rules runner does.
func (f *fixture) recordSuggestion(strategy, symbol, date, suggestion, reason, stop string) {
	f.t.Helper()
	var s any
	if stop != "" {
		s = stop
	}
	if _, err := f.store.Writer().Exec(
		`INSERT INTO holding_suggestions (strategy, symbol, trading_date, suggestion, reason, stop, strategy_hash, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		strategy, symbol, date, suggestion, reason, s, strings.Repeat("a", 64),
		store.FormatTime(at(f.t, startOfTest))); err != nil {
		f.t.Fatal(err)
	}
}

const staleNote = "The research feed is out of date."

func TestTheFeedViewWithNoUploadShowsTheQuietNote(t *testing.T) {
	f := newFixture(t)
	body := f.page("/feed")
	wantIn(t, body, "<h1>Feed</h1>",
		`<section class="notice" aria-label="Research feed">`,
		"No research feed has arrived yet.",
		"Trading does not use the feed, so nothing else changes.")
	wantNotIn(t, body, "Not built yet.", "notice--warning", "notice--halt", `class="sources"`)
	if sent := f.alerts.Sent(); len(sent) != 0 {
		t.Errorf("a missing feed sent alerts: %v", sent)
	}
}

func TestTheFeedViewListsTheLatestCandidatesWithTheirSources(t *testing.T) {
	f := newFixture(t)
	now := f.clock.Now()
	f.saveFeed(now.Add(-30*time.Hour), candidate("OLDC", "An older case.", src("https://old.example.com/a", "Old page")...))
	f.saveFeed(now.Add(-2*time.Hour),
		candidate("CANDA", "A made-up case.\nWith a second line.",
			feed.Source{URL: "https://news.example.com/a", Title: "A made-up article"},
			feed.Source{URL: "https://Filings.Example.ORG:8443/b?x=1&y=2", Title: "A made-up filing"}),
		note("HELDA", "A note the Feed view does not list.", src("https://news.example.com/n", "Note page")...),
		candidate("CANDB", "Another case.", src("http://blog.example.net/c", "A made-up post")...))

	body := f.page("/feed")
	wantIn(t, body,
		`<span class="badge">2 candidates</span>`,
		"The latest feed arrived Mon 16 Nov 2026, 8:00 am ET. 3 items were kept.",
		`<h2 class="card-title" id="candidate-0">CANDA</h2>`,
		`<h2 class="card-title" id="candidate-1">CANDB</h2>`,
		`<p class="card-body feed-text">A made-up case.`+"\n"+`With a second line.</p>`,
		// Each source: its title as a link that opens apart from the app,
		// with the link's domain beside it.
		`<a href="https://news.example.com/a" target="_blank" rel="noopener noreferrer">A made-up article</a> <span class="source-domain">news.example.com</span>`,
		`<a href="https://Filings.Example.ORG:8443/b?x=1&amp;y=2" target="_blank" rel="noopener noreferrer">A made-up filing</a> <span class="source-domain">filings.example.org</span>`,
		`<a href="http://blog.example.net/c" target="_blank" rel="noopener noreferrer">A made-up post</a> <span class="source-domain">blog.example.net</span>`)
	wantNotIn(t, body, "OLDC", "HELDA", "A note the Feed view does not list.", staleNote)
}

func TestAFeedWithNoCandidatesSaysSo(t *testing.T) {
	f := newFixture(t)
	f.saveFeed(f.clock.Now())
	body := f.page("/feed")
	wantIn(t, body, "No candidate cases in the latest feed.", "0 items were kept.")
	wantNotIn(t, body, staleNote, "No research feed has arrived yet.")
}

// markup is feed text carrying the active content a web page could plant in
// the AI's output.
const markup = `<script>alert(1)</script> <a href="https://evil.example.com/">click</a> <img src=x onerror="alert(2)">`

// TestFeedMarkupRendersAsPlainText is the plan's escaping check, on every
// page that shows feed text: the Feed view, Portfolio's notes and the
// approval cards.
func TestFeedMarkupRendersAsPlainText(t *testing.T) {
	f := newFixture(t)
	f.recordHoldings("2026-11-16T14:00:00Z", "100.00", holding(t, "HELDA", "1", "10.00"))
	buy := f.propose(signals.KindBuy, "CANDA", "1", "10.00")
	sources := []feed.Source{
		{URL: `https://news.example.com/a?q="><script>alert(4)</script>`, Title: `<b onmouseover=alert(3)>Bold</b>`},
		// Check drops these; stored anyway, they still get no href.
		{URL: "javascript:alert(5)", Title: "A script link"},
		{URL: "data:text/html,<script>alert(6)</script>", Title: "A data link"},
		// A Cyrillic letter in place of the a.
		{URL: "https://exаmple.com/", Title: "A look-alike site"},
	}
	f.saveFeed(f.clock.Now(),
		candidate("CANDA", markup, sources...),
		note("HELDA", markup, sources...),
		explanation(buy.Key, "CANDA", markup, sources...))

	for _, path := range []string{"/feed", "/portfolio", "/approvals"} {
		t.Run(path, func(t *testing.T) {
			body := f.page(path)
			wantIn(t, body,
				`&lt;script&gt;alert(1)&lt;/script&gt; &lt;a href=&#34;https://evil.example.com/&#34;&gt;click&lt;/a&gt; &lt;img src=x onerror=&#34;alert(2)&#34;&gt;`,
				`&lt;b onmouseover=alert(3)&gt;Bold&lt;/b&gt;</a> <span class="source-domain">news.example.com</span>`,
				`A script link <span class="source-domain">(not a web link, so not linked)</span>`,
				`A data link <span class="source-domain">(not a web link, so not linked)</span>`,
				"exаmple.com</span> <span class=\"source-domain\">(the name has letters outside plain ASCII; check it carefully)</span>")
			wantNotIn(t, body, "<script>alert", `<a href="https://evil`, "<img", "<b ",
				`href="javascript:`, `href="data:`, "#ZgotmplZ", `onerror="alert`)
			// The layout's own script is the only script element.
			if n := strings.Count(body, "<script"); n != 1 {
				t.Errorf("the page has %d script elements, want 1", n)
			}
		})
	}
}

func TestPortfolioShowsTheLatestNoteAndSuggestions(t *testing.T) {
	f := newFixture(t)
	f.recordHoldings("2026-11-16T14:00:00Z", "100.00",
		holding(t, "HELDA", "1", "10.00"), holding(t, "HELDB", "2", "20.00"))
	f.recordSuggestion("steady", "HELDA", "2026-11-12", "sell", "fake_rule: an old made-up reason", "")
	f.recordSuggestion("steady", "HELDA", "2026-11-13", "trim", "fake_rule: a made-up trim reason", "9.50")
	f.recordSuggestion("other", "HELDA", "2026-11-13", "hold", "", "")
	f.recordSuggestion("steady", "NOTHELD", "2026-11-13", "sell", "fake_rule: not held", "")
	now := f.clock.Now()
	f.saveFeed(now.Add(-26*time.Hour), note("HELDA", "An older note.", src("https://old.example.com/n", "Old note page")...))
	f.saveFeed(now.Add(-time.Hour),
		note("HELDA", "The newest made-up note.", src("https://news.example.com/n", "A made-up note page")...),
		candidate("CANDA", "A case.", src("https://news.example.com/c", "Case page")...))

	body := f.page("/portfolio")
	wantIn(t, body,
		`<span class="row-label">Rules' suggestion, other<span class="row-meta">No rule gave a decision. From the rules run of Fri 13 Nov 2026.</span></span><span class="row-value">Hold</span>`,
		`<span class="row-label">Rules' suggestion, steady<span class="row-meta">fake_rule: a made-up trim reason Stop at $9.50. From the rules run of Fri 13 Nov 2026.</span></span><span class="row-value">Trim</span>`,
		"The rules have not run for HELDB yet.",
		`<p class="feed-text">The newest made-up note.</p>`,
		"in the feed that arrived Mon 16 Nov 2026, 9:00 am ET.",
		`<a href="https://news.example.com/n" target="_blank" rel="noopener noreferrer">A made-up note page</a> <span class="source-domain">news.example.com</span>`,
		"The latest research feed has no note on HELDB.")
	wantNotIn(t, body, "an old made-up reason", "An older note.", "not held", "NOTHELD", staleNote)
}

func TestAnApprovalCardShowsTheExplanationMatchedByItsKey(t *testing.T) {
	f := newFixture(t)
	buy := f.propose(signals.KindBuy, "CANDA", "10", "12.00")
	trim := f.propose(signals.KindTrim, "HELDA", "4", "")
	f.saveFeed(f.clock.Now(),
		explanation(buy.Key, "CANDA", "Why the made-up rule bought.", src("https://news.example.com/e", "A made-up explainer")...),
		explanation(signalKey(99), "HELDA", "An explanation of some other signal.", src("https://news.example.com/x", "Other")...),
		// The right key with the wrong symbol is not shown.
		explanation(trim.Key, "ZZZZ", "An explanation naming the wrong stock.", src("https://news.example.com/z", "Wrong")...))

	body := f.page("/approvals")
	wantIn(t, body,
		"Made-up rule: CANDA crossed its made-up line",
		"Made-up rule: HELDA crossed its made-up line",
		"The research feed's explanation",
		`<p class="feed-text">Why the made-up rule bought.</p>`,
		`<a href="https://news.example.com/e" target="_blank" rel="noopener noreferrer">A made-up explainer</a> <span class="source-domain">news.example.com</span>`)
	wantNotIn(t, body, "some other signal", "the wrong stock")
	if n := strings.Count(body, "The research feed's explanation"); n != 1 {
		t.Errorf("%d cards show an explanation, want 1", n)
	}
	// The explanation sits on the buy's card, after its rule's reason.
	card := body[strings.Index(body, `id="order-`+strconv.FormatInt(buy.ID, 10)+`"`):]
	card = card[:strings.Index(card, "</section>")]
	reason := strings.Index(card, "Made-up rule: CANDA")
	if e := strings.Index(card, "Why the made-up rule bought."); reason < 0 || e < reason {
		t.Errorf("the explanation is not on the buy's card after its reason")
	}
}

// world is what the stale-feed test compares: everything a feed could change
// if it reached trading.
type world struct {
	orders  []orders.Order
	history int
	jobs    int
	alerts  int
	checked any
}

func (f *fixture) world(ids ...int64) world {
	f.t.Helper()
	var w world
	for _, id := range ids {
		w.orders = append(w.orders, f.order(id))
		h, err := orders.History(context.Background(), f.store.Reader(), id)
		if err != nil {
			f.t.Fatal(err)
		}
		w.history += len(h)
	}
	if err := f.store.Reader().QueryRow(`SELECT count(*) FROM job_runs`).Scan(&w.jobs); err != nil {
		f.t.Fatal(err)
	}
	w.alerts = len(f.alerts.Sent())
	o, snap := f.checker.last(f.t)
	w.checked = []any{o, snap}
	return w
}

// TestAStaleFeedShowsTheQuietNoteAndChangesNothing is the plan's stale
// check: a feed past feed.StaleAfter shows the quiet note, the pages fall
// back to the rules' own reasons, and no order, risk check, job or alert
// changes, against both no feed at all and a fresh one.
func TestAStaleFeedShowsTheQuietNoteAndChangesNothing(t *testing.T) {
	f := newFixture(t)
	f.recordHoldings("2026-11-16T14:00:00Z", "100.00", holding(t, "HELDA", "1", "10.00"))
	f.recordSuggestion("steady", "HELDA", "2026-11-13", "trim", "fake_rule: a made-up trim reason", "")
	buy := f.propose(signals.KindBuy, "CANDA", "10", "12.00")
	trim := f.propose(signals.KindTrim, "HELDA", "1", "")
	items := []feed.Item{
		candidate("CANDB", "A stale case.", src("https://news.example.com/c", "Case page")...),
		note("HELDA", "A made-up note.", src("https://news.example.com/n", "Note page")...),
		explanation(buy.Key, "CANDA", "A made-up explanation.", src("https://news.example.com/e", "Explainer")...),
	}
	render := func() (feedPage, portfolio, approvals string) {
		t.Helper()
		return f.page("/feed"), f.page("/portfolio"), f.page("/approvals")
	}

	// No feed at all.
	_, _, _ = render()
	before := f.world(buy.ID, trim.ID)

	// A feed that arrived just over StaleAfter ago.
	now := f.clock.Now()
	f.saveFeed(now.Add(-feed.StaleAfter-time.Minute), items...)
	feedPage, portfolio, approvals := render()
	wantIn(t, feedPage,
		`<section class="notice" aria-label="Research feed">`, staleNote,
		"The latest one arrived Sat 14 Nov 2026, 9:59 pm ET, more than 36 hours ago.",
		"Trading does not use the feed, so nothing else changes.",
		// The stale candidates are still listed under the note.
		"CANDB")
	wantNotIn(t, feedPage, "notice--warning", "notice--halt")
	wantIn(t, portfolio, "fake_rule: a made-up trim reason",
		"The research feed is out of date, so only the rules' suggestion is shown.")
	wantNotIn(t, portfolio, "A made-up note.")
	wantIn(t, approvals, "Made-up rule: CANDA crossed its made-up line")
	wantNotIn(t, approvals, "A made-up explanation.", "The research feed's explanation")
	if after := f.world(buy.ID, trim.ID); !reflect.DeepEqual(before, after) {
		t.Errorf("a stale feed changed something:\nbefore %+v\nafter  %+v", before, after)
	}

	// The same feed fresh shows its words, and still changes nothing.
	f.saveFeed(now, items...)
	feedPage, portfolio, approvals = render()
	wantNotIn(t, feedPage, staleNote)
	wantIn(t, portfolio, "A made-up note.")
	wantIn(t, approvals, "A made-up explanation.")
	if after := f.world(buy.ID, trim.ID); !reflect.DeepEqual(before, after) {
		t.Errorf("a fresh feed changed something:\nbefore %+v\nafter  %+v", before, after)
	}
}
