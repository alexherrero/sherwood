package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/signals"
)

// Every symbol and address here is made up.

// newWatchFixture is a fixture with the example strategy file, which sets a
// sector limit, plus a sector for CANDA (but none for CANDB), and with the
// example's second strategy opted in to the watchlist.
func newWatchFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t, config.ModePaper, func(_ *fixture, cfg *Config) {
		s := exampleStrategy(t)
		s.Sectors["CANDA"] = "technology"
		s.Strategies[1].IncludeWatchlist = true
		cfg.Strategy = s
	})
}

func (f *fixture) watch(symbol, token string, from string) *httptest.ResponseRecorder {
	f.t.Helper()
	c, tok := f.session()
	if token == "session" {
		token = tok
	}
	return f.postSwitch(watchPath, token, c, fromAddr(from), symbolField, symbol)
}

func wantFeed(t *testing.T, w *httptest.ResponseRecorder, result string) {
	t.Helper()
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/feed?watch="+result {
		t.Fatalf("answer = %d to %q, want 303 to /feed?watch=%s\n%s", w.Code, w.Header().Get("Location"), result, w.Body)
	}
}

func (f *fixture) watched() []string {
	f.t.Helper()
	w, err := limits.Watchlist(context.Background(), f.store.Reader(), f.srv.strategy)
	if err != nil {
		f.t.Fatal(err)
	}
	return w
}

func (f *fixture) countRows(table string) int {
	f.t.Helper()
	var n int
	if err := f.store.Reader().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestTheWatchlistTapNeedsTheTokenAndAnApprovalDevice(t *testing.T) {
	f := newWatchFixture(t)
	if w := f.watch("CANDA", "", phoneAddr); w.Code != http.StatusForbidden {
		t.Errorf("without the anti-forgery token = %d, want 403", w.Code)
	}
	if w := f.watch("CANDA", "not-the-token", phoneAddr); w.Code != http.StatusForbidden {
		t.Errorf("with a wrong token = %d, want 403", w.Code)
	}
	if w := f.watch("CANDA", "session", laptopAddr); w.Code != http.StatusForbidden {
		t.Errorf("from another device = %d, want 403", w.Code)
	}
	if n := f.phoneChanges(); n != 0 || slices.Contains(f.watched(), "CANDA") {
		t.Fatalf("%d changes recorded and watchlist %v, want nothing added", n, f.watched())
	}
}

func TestTheWatchlistTapRecordsTheStockAndCreatesNoOrder(t *testing.T) {
	f := newWatchFixture(t)
	f.saveFeed(f.clock.Now().Add(-time.Hour),
		candidate("CANDA", "A made-up case.", src("https://news.example.com/a", "A made-up article")...),
		candidate("CANDB", "Another made-up case.", src("https://news.example.com/b", "Another article")...))

	body := f.page("/feed")
	wantIn(t, body, `<form method="post" action="/watchlist/add">`,
		`<input type="hidden" name="csrf_token" value="`, `<input type="hidden" name="symbol" value="CANDA">`,
		"Add to watchlist", "The rules of example-pullback also run over the watchlist",
		"Only an approval device can add it.",
		// CANDB has no sector while the sector limit is in force: no button,
		// and the page says how to add it.
		"the strategy file gives CANDB no sector", "(such as CANDB: technology)")
	wantNotIn(t, body, `name="symbol" value="CANDB"`)

	ordersBefore, signalsBefore := f.countRows("orders"), f.countRows("signals")
	wantFeed(t, f.watch("CANDA", "session", phoneAddr), "added")
	if w := f.watched(); !slices.Equal(w, []string{"AAPL", "MSFT", "KO", "CANDA"}) {
		t.Errorf("watchlist in force = %v, want the file's plus CANDA", w)
	}
	if n := f.phoneChanges(); n != 1 {
		t.Errorf("%d phone changes recorded, want 1", n)
	}
	// The tap only records the stock: no order, no signal.
	if o, s := f.countRows("orders"), f.countRows("signals"); o != ordersBefore || s != signalsBefore {
		t.Errorf("after the tap %d orders and %d signals, want %d and %d", o, s, ordersBefore, signalsBefore)
	}

	body = f.page("/feed?watch=added")
	wantIn(t, body, "Added to the watchlist.", "CANDA is on the watchlist.")
	wantNotIn(t, body, `name="symbol" value="CANDA"`)
	wantIn(t, f.page("/settings"), "CANDA added to the watchlist")

	// A second tap, a stock with no sector and a symbol that is not one.
	wantFeed(t, f.watch("CANDA", "session", phoneAddr), "watched")
	wantFeed(t, f.watch("AAPL", "session", phoneAddr), "watched")
	wantFeed(t, f.watch("CANDB", "session", phoneAddr), "nosector")
	wantFeed(t, f.watch("<b>x</b>", "session", phoneAddr), "invalid")
	if n := f.phoneChanges(); n != 1 {
		t.Errorf("%d phone changes recorded after the refusals, want still 1", n)
	}
	wantIn(t, f.page("/feed?watch=nosector"), "Not added: the stock has no sector.")
}

func TestTheApprovalReCheckAndTheExportSeeAnAddedStock(t *testing.T) {
	f := newWatchFixture(t)
	wantFeed(t, f.watch("CANDA", "session", phoneAddr), "added")

	o := f.propose(signals.KindBuy, "CANDA", "1", "12.00")
	wantApprovals(t, f.act(approvePath, o.ID, fromAddr(phoneAddr)), "approved")
	if _, snap := f.checker.last(t); !slices.Contains(snap.Watchlist, "CANDA") {
		t.Errorf("the approval checked against watchlist %v, want CANDA in it", snap.Watchlist)
	}

	e, err := f.srv.buildExport(httptest.NewRequest(http.MethodGet, exportPath, nil))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, sym := range e.Symbols {
		if sym.Symbol == "CANDA" && sym.Watched {
			found = true
		}
	}
	if !found {
		t.Errorf("the export's symbols %+v do not list CANDA as watched", e.Symbols)
	}
}
