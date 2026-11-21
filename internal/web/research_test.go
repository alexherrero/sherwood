package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/feed"
	"github.com/alexherrero/sherwood/internal/store"
)

// Made-up values: none of them is anyone's real data.
const (
	heldQuantity = "7.25"
	heldCost     = "18.37"
	heldCash     = "98765.43"
	wrongToken   = "a-wrong-feed-token-that-is-long-enough-0000"
)

// machineReq is one request from the research job's script.
type machineReq struct {
	method  string
	path    string
	body    string
	token   string // sent as "Bearer <token>" unless empty
	headers map[string][]string
	noLogin bool
}

func (f *fixture) machine(rq machineReq) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(rq.method, rq.path, strings.NewReader(rq.body))
	if rq.method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
	}
	if !rq.noLogin {
		r.Header.Set(headerLogin, login)
	}
	if rq.token != "" {
		r.Header.Set("Authorization", "Bearer "+rq.token)
	}
	for k, vs := range rq.headers {
		r.Header.Del(k)
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	w := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(w, r)
	return w
}

func (f *fixture) export() *httptest.ResponseRecorder {
	f.t.Helper()
	return f.machine(machineReq{method: http.MethodGet, path: exportPath, token: feedToken})
}

func (f *fixture) upload(body string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.machine(machineReq{method: http.MethodPost, path: uploadPath, token: feedToken, body: body})
}

// signalKey is a made-up signal key: 64 lowercase hex characters.
func signalKey(n int) string { return fmt.Sprintf("%064x", n) }

// recordSignal writes a signal the way the rules runner stores one.
func (f *fixture) recordSignal(key, symbol, date, side, kind, reason string) {
	f.t.Helper()
	if _, err := f.store.Writer().Exec(
		`INSERT INTO signals (idempotency_key, strategy, symbol, trading_date, side, kind, rule_type, reason, strategy_hash, created_at)
		 VALUES (?, 'steady', ?, ?, ?, ?, 'fake_rule', ?, ?, ?)`,
		key, symbol, date, side, kind, reason, strings.Repeat("a", 64), store.FormatTime(at(f.t, startOfTest))); err != nil {
		f.t.Fatal(err)
	}
}

// researchFixture holds two stocks (BBB and CCC), watches AAA and CCC, has
// closes on two days and signals on two days.
func researchFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixtureWith(t, config.ModePaper, func(_ *fixture, c *Config) {
		c.Strategy = &config.Strategy{Watchlist: []string{"CCC", "AAA"}}
	})
	f.recordHoldings("2026-11-16T14:00:00Z", heldCash,
		holding(t, "CCC", heldQuantity, heldCost), holding(t, "BBB", "3", "40.00"))
	f.recordPrices("2026-11-12",
		priceCheck{symbol: "AAA", outcome: "agreed", close: "11.00"},
		priceCheck{symbol: "BBB", held: true, outcome: "agreed", close: "19.00"},
		priceCheck{symbol: "CCC", held: true, outcome: "agreed", close: "30.00"})
	f.recordPrices("2026-11-13",
		priceCheck{symbol: "AAA", outcome: "agreed", close: "12.00"},
		priceCheck{symbol: "BBB", held: true, outcome: "disagreed", close: "20.00"},
		priceCheck{symbol: "CCC", held: true, outcome: "agreed", close: "31.00"},
		priceCheck{symbol: "ZZZ", outcome: "agreed", close: "99.00"})
	f.recordSignal(signalKey(1), "BBB", "2026-11-12", "sell", "trim", "fake_rule: an old made-up reason")
	f.recordSignal(signalKey(2), "CCC", "2026-11-13", "sell", "trim", "fake_rule: made up for the test")
	f.recordSignal(signalKey(3), "AAA", "2026-11-13", "buy", "buy", "fake_rule: also made up")
	return f
}

// fieldPaths lists every object key in v as a dotted path, with [] for an
// array's elements.
func fieldPaths(prefix string, v any, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			out[p] = true
			fieldPaths(p, child, out)
		}
	case []any:
		for _, child := range x {
			fieldPaths(prefix+"[]", child, out)
		}
	}
}

// TestTheExportCarriesOnlyTheAllowedFields pins the export's field names
// exactly. A new field must be added here on purpose, after checking it
// carries no balance, share count, cash, order or account detail.
func TestTheExportCarriesOnlyTheAllowedFields(t *testing.T) {
	f := researchFixture(t)
	w := f.export()
	if w.Code != http.StatusOK {
		t.Fatalf("export = %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var v any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	fieldPaths("", v, got)
	want := []string{
		"version", "generated_at", "signal_date", "symbols", "signals",
		"symbols[].symbol", "symbols[].held", "symbols[].watched", "symbols[].closes",
		"symbols[].closes[].date", "symbols[].closes[].close",
		"signals[].key", "signals[].symbol", "signals[].side", "signals[].kind", "signals[].reason",
	}
	var gotList []string
	for p := range got {
		gotList = append(gotList, p)
	}
	slices.Sort(gotList)
	slices.Sort(want)
	if !slices.Equal(gotList, want) {
		t.Errorf("export fields:\n got %v\nwant %v", gotList, want)
	}
	body := w.Body.String()
	for _, leak := range []string{heldQuantity, heldCost, heldCash, "40.00", "quantity", "cash", "cost", "steady", "strategy"} {
		if strings.Contains(body, leak) {
			t.Errorf("the export carries %q: %s", leak, body)
		}
	}
	if len(w.Result().Cookies()) != 0 {
		t.Error("a machine route set a session cookie")
	}
}

func TestTheExportListsHeldAndWatchedStocksWithTheLatestSignals(t *testing.T) {
	f := researchFixture(t)
	e, err := feed.DecodeExport(f.export().Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !e.GeneratedAt.Equal(f.clock.Now()) {
		t.Errorf("generated_at = %s, want the clock's %s", e.GeneratedAt, f.clock.Now())
	}
	type row struct {
		symbol        string
		held, watched bool
		closes        string
	}
	var got []row
	for _, s := range e.Symbols {
		var cs []string
		for _, c := range s.Closes {
			cs = append(cs, c.Date+"="+c.Close.String())
		}
		got = append(got, row{s.Symbol, s.Held, s.Watched, strings.Join(cs, " ")})
	}
	want := []row{
		{"AAA", false, true, "2026-11-12=11.00 2026-11-13=12.00"},
		// BBB's sources disagreed on the 13th, so that close is not checked.
		{"BBB", true, false, "2026-11-12=19.00"},
		{"CCC", true, true, "2026-11-12=30.00 2026-11-13=31.00"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("symbols:\n got %+v\nwant %+v", got, want)
	}
	if e.SignalDate != "2026-11-13" {
		t.Errorf("signal_date = %q", e.SignalDate)
	}
	if len(e.Signals) != 2 || e.Signals[0].Key != signalKey(2) || e.Signals[1].Key != signalKey(3) ||
		e.Signals[0].Reason != "fake_rule: made up for the test" || e.Signals[0].Kind != "trim" || e.Signals[0].Side != "sell" {
		t.Errorf("signals = %+v, want the 13th's two in order", e.Signals)
	}
}

func TestAnEmptyExportHasNoSignalDateAndEmptyLists(t *testing.T) {
	f := newFixture(t)
	w := f.export()
	if w.Code != http.StatusOK {
		t.Fatalf("export = %d", w.Code)
	}
	for _, want := range []string{`"signal_date":""`, `"symbols":[]`, `"signals":[]`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("export lacks %s: %s", want, w.Body.String())
		}
	}
}

func TestAMachineRouteNeedsTheLoginAndTheFeedToken(t *testing.T) {
	f := researchFixture(t)
	file := `{"version":1,"items":[]}`
	cases := map[string]struct {
		rq   machineReq
		code int
	}{
		"no login":      {machineReq{token: feedToken, noLogin: true}, http.StatusForbidden},
		"another login": {machineReq{token: feedToken, headers: map[string][]string{headerLogin: {"someone@example.com"}}}, http.StatusForbidden},
		"no token":      {machineReq{}, http.StatusUnauthorized},
		"wrong token":   {machineReq{token: wrongToken}, http.StatusUnauthorized},
		"token prefix":  {machineReq{token: feedToken[:32]}, http.StatusUnauthorized},
		"not bearer":    {machineReq{headers: map[string][]string{"Authorization": {"Basic " + feedToken}}}, http.StatusUnauthorized},
		"bare token":    {machineReq{headers: map[string][]string{"Authorization": {feedToken}}}, http.StatusUnauthorized},
		"two tokens": {machineReq{headers: map[string][]string{
			"Authorization": {"Bearer " + feedToken, "Bearer " + feedToken}}}, http.StatusUnauthorized},
		"from a browser": {machineReq{token: feedToken, headers: map[string][]string{"Sec-Fetch-Site": {"same-origin"}}}, http.StatusForbidden},
	}
	for name, c := range cases {
		for _, route := range []struct{ method, path string }{{http.MethodGet, exportPath}, {http.MethodPost, uploadPath}} {
			t.Run(name+" "+route.method, func(t *testing.T) {
				rq := c.rq
				rq.method, rq.path, rq.body = route.method, route.path, file
				w := f.machine(rq)
				if w.Code != c.code {
					t.Errorf("status = %d, want %d", w.Code, c.code)
				}
				if strings.Contains(w.Body.String(), "AAA") || strings.Contains(w.Body.String(), "accepted") {
					t.Errorf("the route answered: %s", w.Body.String())
				}
				if len(w.Result().Cookies()) != 0 {
					t.Error("a refused machine caller was given a cookie")
				}
			})
		}
	}
	if n := f.rows("feed_uploads"); n != 0 {
		t.Errorf("%d uploads were stored by refused requests", n)
	}
	// The scheme is matched without regard to case.
	w := f.machine(machineReq{method: http.MethodGet, path: exportPath,
		headers: map[string][]string{"Authorization": {"bearer " + feedToken}}})
	if w.Code != http.StatusOK {
		t.Errorf("lower-case bearer = %d", w.Code)
	}
}

func TestAPageRouteDoesNotTakeTheFeedToken(t *testing.T) {
	f := newFixture(t)
	c, _ := f.session()
	w := f.do(req{method: http.MethodPost, path: "/action", form: map[string][]string{}, cookie: c,
		headers: map[string][]string{"Authorization": {"Bearer " + feedToken}}})
	if w.Code != http.StatusForbidden {
		t.Errorf("a change with only the feed token = %d, want 403", w.Code)
	}
	f.wantChanges(0)
}

func TestAnOffSchemaUploadIsRefusedAndStoresNothing(t *testing.T) {
	f := researchFixture(t)
	for name, body := range map[string]string{
		"not JSON":        `this is not json`,
		"unknown field":   `{"version":1,"items":[],"orders":[]}`,
		"wrong version":   `{"version":2,"items":[]}`,
		"no items":        `{"version":1}`,
		"trailing data":   `{"version":1,"items":[]} {}`,
		"item wrong type": `{"version":1,"items":[{"kind":"candidate","symbol":"DDD","text":7,"sources":[]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := f.upload(body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"error":"The feed file was refused: `) {
				t.Errorf("answer = %s", w.Body.String())
			}
		})
	}
	w := f.machine(machineReq{method: http.MethodPost, path: uploadPath, token: feedToken,
		body: `{"version":1,"items":[]}`, headers: map[string][]string{"Content-Type": {"text/plain"}}})
	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain = %d, want 415", w.Code)
	}
	big := `{"version":1,"items":[],"x":"` + strings.Repeat("a", feed.MaxFileBytes) + `"}`
	if w := f.upload(big); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an oversized file = %d, want 413", w.Code)
	}
	if u, i := f.rows("feed_uploads"), f.rows("feed_items"); u != 0 || i != 0 {
		t.Errorf("stored %d uploads and %d items, want none", u, i)
	}
}

func TestAnUploadStoresTheGoodItemsAndReportsTheDropped(t *testing.T) {
	f := researchFixture(t)
	src := `"sources":[{"url":"https://news.example.com/a","title":"A made-up story"}]`
	body := `{"version":1,"items":[
		{"kind":"candidate","symbol":"DDD","text":"A made-up case.",` + src + `},
		{"kind":"holding_note","symbol":"CCC","text":"A made-up note.",` + src + `},
		{"kind":"candidate","symbol":"EEE","text":"No source at all.","sources":[]},
		{"kind":"signal_explanation","symbol":"CCC","signal_key":"` + signalKey(2) + `","text":"Why it trimmed.",` + src + `},
		{"kind":"signal_explanation","symbol":"BBB","signal_key":"` + signalKey(1) + `","text":"An old signal.",` + src + `}
	]}`
	w := f.upload(body)
	if w.Code != http.StatusOK {
		t.Fatalf("upload = %d: %s", w.Code, w.Body.String())
	}
	var got uploadAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := uploadAnswer{Upload: 1, Accepted: 3, Dropped: []feed.Drop{
		{Index: 2, Kind: "candidate", Symbol: "EEE", Reason: "no source"},
		// Only the latest rules run's signals can be explained.
		{Index: 4, Kind: "signal_explanation", Symbol: "BBB", Reason: "no signal has this key"},
	}}
	if got.Upload != want.Upload || got.Accepted != want.Accepted || !slices.Equal(got.Dropped, want.Dropped) {
		t.Errorf("answer:\n got %+v\nwant %+v", got, want)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Error("the upload set a session cookie")
	}

	ctx := context.Background()
	up, err := feed.LatestUpload(ctx, f.store.Reader())
	if err != nil || up == nil {
		t.Fatalf("latest upload %v, %v", up, err)
	}
	if !up.At.Equal(f.clock.Now()) || up.Accepted != 3 || up.Dropped != 2 {
		t.Errorf("upload = %+v, want taken in at the clock's time with 3 kept and 2 dropped", up)
	}
	cands, err := feed.LatestCandidates(ctx, f.store.Reader())
	if err != nil || len(cands) != 1 || cands[0].Symbol != "DDD" {
		t.Errorf("candidates = %+v, %v", cands, err)
	}
	if ex, err := feed.Explanation(ctx, f.store.Reader(), signalKey(2)); err != nil || ex == nil || ex.Text != "Why it trimmed." {
		t.Errorf("explanation = %+v, %v", ex, err)
	}

	// A second upload only adds: the first's rows stay.
	if w := f.upload(`{"version":1,"items":[]}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"dropped":[]`) {
		t.Errorf("an empty upload = %d: %s", w.Code, w.Body.String())
	}
	if u, i := f.rows("feed_uploads"), f.rows("feed_items"); u != 2 || i != 3 {
		t.Errorf("%d uploads and %d items, want 2 and 3", u, i)
	}
}

func TestTheFeedTokenAndBodyNeverReachTheLog(t *testing.T) {
	f := researchFixture(t)
	planted := "planted-body-text"
	f.export()
	f.upload(`{"version":1,"items":[{"kind":"candidate","symbol":"DDD","text":"` + planted + `","sources":[]}]}`)
	f.upload(`{"version":1,"` + planted + `":1}`)
	f.machine(machineReq{method: http.MethodGet, path: exportPath, token: wrongToken})
	f.machine(machineReq{method: http.MethodPost, path: uploadPath, token: wrongToken, body: planted})
	f.machine(machineReq{method: http.MethodGet, path: exportPath, token: feedToken, noLogin: true})
	log := f.log.String()
	for _, want := range []string{`"route":"GET /research/export"`, `"route":"POST /feed/upload"`,
		`"reason":"missing or wrong feed token"`, `"reason":"off-schema feed file"`} {
		if !strings.Contains(log, want) {
			t.Errorf("log is missing %s:\n%s", want, log)
		}
	}
	for _, leak := range []string{feedToken, feedToken[:16], wrongToken, "Bearer", planted, "DDD"} {
		if strings.Contains(log, leak) {
			t.Errorf("log carries %q:\n%s", leak, log)
		}
	}
}

func TestHandleMachineTakesOnlyGetAndPost(t *testing.T) {
	f := newFixture(t)
	defer func() {
		if recover() == nil {
			t.Error("HandleMachine accepted PUT")
		}
	}()
	f.srv.HandleMachine(http.MethodPut, "/put", http.NotFoundHandler())
}
