package feed

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/money"
)

// Every symbol, link, key and price here is made up.

var (
	keyHeld  = strings.Repeat("ab", 32)
	keyOther = strings.Repeat("cd", 32)
)

// export is a small made-up export: FAKEH held, FAKEW watched, one signal on
// each.
func testExport() Export {
	return Export{
		Version:     ExportVersion,
		GeneratedAt: time.Date(2026, 11, 13, 22, 0, 0, 0, time.UTC),
		SignalDate:  "2026-11-13",
		Symbols: []ExportSymbol{
			{Symbol: "FAKEH", Held: true, Closes: []Close{{Date: "2026-11-13", Close: money.MustParsePrice("12.34")}}},
			{Symbol: "FAKEW", Watched: true, Closes: []Close{{Date: "2026-11-13", Close: money.MustParsePrice("5.00")}}},
		},
		Signals: []ExportSignal{
			{Key: keyHeld, Symbol: "FAKEH", Side: "sell", Kind: "trim", Reason: "fake_rule: made up"},
			{Key: keyOther, Symbol: "FAKEW", Side: "buy", Kind: "buy", Reason: "fake_rule: made up"},
		},
	}
}

func src() []Source {
	return []Source{{URL: "https://news.example.com/a", Title: "A made-up story"}}
}

func goodItems() []Item {
	return []Item{
		{Kind: Candidate, Symbol: "FAKEW", Text: "A made-up case.", Sources: src()},
		{Kind: HoldingNote, Symbol: "FAKEH", Text: "A made-up note.", Sources: src()},
		{Kind: SignalExplanation, Symbol: "FAKEH", SignalKey: keyHeld, Text: "A made-up explanation.", Sources: src()},
	}
}

func fileJSON(t *testing.T, items []Item) []byte {
	t.Helper()
	b, err := json.Marshal(File{Version: Version, Items: &items})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCheckKeepsGoodItems(t *testing.T) {
	f, err := Check(fileJSON(t, goodItems()), testExport().Known())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Items) != 3 || len(f.Dropped) != 0 {
		t.Fatalf("kept %d, dropped %v; want 3 kept, none dropped", len(f.Items), f.Dropped)
	}
}

func TestCheckRefusesOffSchemaFiles(t *testing.T) {
	item := `{"kind":"candidate","symbol":"FAKEW","text":"t","sources":[{"url":"https://example.com/","title":"x"}]}`
	many := make([]string, MaxItems+1)
	for i := range many {
		many[i] = item
	}
	cases := map[string]string{
		"not json":           `this is not json`,
		"empty":              ``,
		"null":               `null`,
		"an array":           `[]`,
		"no version":         `{"items":[]}`,
		"wrong version":      `{"version":2,"items":[]}`,
		"no items":           `{"version":1}`,
		"null items":         `{"version":1,"items":null}`,
		"unknown field":      `{"version":1,"items":[],"note":"hi"}`,
		"unknown item field": `{"version":1,"items":[{"kind":"candidate","symbol":"FAKEW","text":"t","sources":[],"html":"<b>"}]}`,
		"unknown source field": `{"version":1,"items":[{"kind":"candidate","symbol":"FAKEW","text":"t",` +
			`"sources":[{"url":"https://example.com/","title":"x","onclick":"y"}]}]}`,
		"text a number":    `{"version":1,"items":[{"kind":"candidate","symbol":"FAKEW","text":5,"sources":[]}]}`,
		"trailing data":    `{"version":1,"items":[]} {"version":1,"items":[]}`,
		"trailing junk":    `{"version":1,"items":[]} x`,
		"too many items":   `{"version":1,"items":[` + strings.Join(many, ",") + `]}`,
		"too many bytes":   `{"version":1,"items":[]}` + strings.Repeat(" ", MaxFileBytes),
		"version a string": `{"version":"1","items":[]}`,
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			f, err := Check([]byte(data), testExport().Known())
			if err == nil {
				t.Fatalf("accepted: kept %d, dropped %d", len(f.Items), len(f.Dropped))
			}
			if f.Items != nil || f.Dropped != nil {
				t.Fatalf("a refused file still gave a feed: %+v", f)
			}
		})
	}
}

func TestCheckAcceptsEmptyFeed(t *testing.T) {
	f, err := Check([]byte(`{"version":1,"items":[]}`), Known{})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Items) != 0 || len(f.Dropped) != 0 {
		t.Fatalf("got %+v", f)
	}
}

func TestCheckDropsBadItems(t *testing.T) {
	long := strings.Repeat("é", MaxText+1)
	tooMany := make([]Source, MaxSources+1)
	for i := range tooMany {
		tooMany[i] = src()[0]
	}
	cases := map[string]struct {
		item Item
		want string
	}{
		"no source":     {Item{Kind: Candidate, Symbol: "FAKEW", Text: "t"}, "no source"},
		"empty sources": {Item{Kind: Candidate, Symbol: "FAKEW", Text: "t", Sources: []Source{}}, "no source"},
		"javascript link": {Item{Kind: Candidate, Symbol: "FAKEW", Text: "t",
			Sources: []Source{{URL: "javascript:alert(1)", Title: "x"}}}, "not http or https"},
		"javascript link in caps": {Item{Kind: Candidate, Symbol: "FAKEW", Text: "t",
			Sources: []Source{{URL: "JavaScript:alert(1)", Title: "x"}}}, "not http or https"},
		"one bad link of two": {Item{Kind: Candidate, Symbol: "FAKEW", Text: "t",
			Sources: []Source{src()[0], {URL: "data:text/html,<b>x</b>", Title: "x"}}}, "not http or https"},
		"ftp link":      {Item{Kind: Candidate, Symbol: "FAKEW", Text: "t", Sources: []Source{{URL: "ftp://example.com/x", Title: "x"}}}, "not http or https"},
		"relative link": {Item{Kind: Candidate, Symbol: "FAKEW", Text: "t", Sources: []Source{{URL: "/news/a", Title: "x"}}}, "not http or https"},
		"no host":       {Item{Kind: Candidate, Symbol: "FAKEW", Text: "t", Sources: []Source{{URL: "https:///a", Title: "x"}}}, "no host"},
		"user in link": {Item{Kind: Candidate, Symbol: "FAKEW", Text: "t",
			Sources: []Source{{URL: "https://someone@example.com/", Title: "x"}}}, "user name"},
		"link too long": {Item{Kind: Candidate, Symbol: "FAKEW", Text: "t",
			Sources: []Source{{URL: "https://example.com/" + strings.Repeat("a", MaxURL), Title: "x"}}}, "more than"},
		"no title":                 {Item{Kind: Candidate, Symbol: "FAKEW", Text: "t", Sources: []Source{{URL: "https://example.com/"}}}, "no title"},
		"too many sources":         {Item{Kind: Candidate, Symbol: "FAKEW", Text: "t", Sources: tooMany}, "sources, more than"},
		"unknown kind":             {Item{Kind: "buy_now", Symbol: "FAKEW", Text: "t", Sources: src()}, "unknown kind"},
		"no kind":                  {Item{Symbol: "FAKEW", Text: "t", Sources: src()}, "unknown kind"},
		"text too long":            {Item{Kind: Candidate, Symbol: "FAKEW", Text: long, Sources: src()}, "more than 2000"},
		"blank text":               {Item{Kind: Candidate, Symbol: "FAKEW", Text: "  \n", Sources: src()}, "no text"},
		"lowercase symbol":         {Item{Kind: Candidate, Symbol: "fakew", Text: "t", Sources: src()}, "not a stock symbol"},
		"long symbol":              {Item{Kind: Candidate, Symbol: "FAKEWFAKEWF", Text: "t", Sources: src()}, "not a stock symbol"},
		"held candidate":           {Item{Kind: Candidate, Symbol: "FAKEH", Text: "t", Sources: src()}, "do not hold"},
		"candidate with key":       {Item{Kind: Candidate, Symbol: "FAKEW", SignalKey: keyOther, Text: "t", Sources: src()}, "no signal key"},
		"note on a stock not held": {Item{Kind: HoldingNote, Symbol: "FAKEW", Text: "t", Sources: src()}, "you hold"},
		"note with key":            {Item{Kind: HoldingNote, Symbol: "FAKEH", SignalKey: keyHeld, Text: "t", Sources: src()}, "no signal key"},
		"explanation without key":  {Item{Kind: SignalExplanation, Symbol: "FAKEH", Text: "t", Sources: src()}, "needs the signal's key"},
		"explanation with uppercase key": {Item{Kind: SignalExplanation, Symbol: "FAKEH",
			SignalKey: strings.ToUpper(keyHeld), Text: "t", Sources: src()}, "needs the signal's key"},
		"explanation of no signal": {Item{Kind: SignalExplanation, Symbol: "FAKEH",
			SignalKey: strings.Repeat("ef", 32), Text: "t", Sources: src()}, "no signal has this key"},
		"explanation with the wrong symbol": {Item{Kind: SignalExplanation, Symbol: "FAKEW",
			SignalKey: keyHeld, Text: "t", Sources: src()}, "is for FAKEH"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			items := append(goodItems(), c.item)
			f, err := Check(fileJSON(t, items), testExport().Known())
			if err != nil {
				t.Fatalf("the whole file was refused: %v", err)
			}
			if len(f.Items) != 3 {
				t.Errorf("kept %d items, want the 3 good ones", len(f.Items))
			}
			if len(f.Dropped) != 1 {
				t.Fatalf("dropped %v, want one", f.Dropped)
			}
			d := f.Dropped[0]
			if d.Index != 3 || !strings.Contains(d.Reason, c.want) {
				t.Errorf("dropped %+v, want index 3 with a reason containing %q", d, c.want)
			}
		})
	}
}

func TestCheckDropsRepeats(t *testing.T) {
	items := append(goodItems(), goodItems()...)
	f, err := Check(fileJSON(t, items), testExport().Known())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Items) != 3 || len(f.Dropped) != 3 {
		t.Fatalf("kept %d, dropped %d; want 3 and 3", len(f.Items), len(f.Dropped))
	}
	for _, d := range f.Dropped {
		if d.Index < 3 || !strings.Contains(d.Reason, "earlier item") {
			t.Errorf("dropped %+v", d)
		}
	}
}

func TestCheckKeepsMarkupAsText(t *testing.T) {
	// Escaping is the view's job; Check keeps the words exactly as written.
	text := `<script>alert("x")</script> & <b>bold</b>`
	items := []Item{{Kind: Candidate, Symbol: "FAKEW", Text: text, Sources: src()}}
	f, err := Check(fileJSON(t, items), testExport().Known())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Items) != 1 || f.Items[0].Text != text {
		t.Fatalf("got %+v", f.Items)
	}
}

func TestDropQuotesAreShort(t *testing.T) {
	items := []Item{{Kind: Kind(strings.Repeat("k", 500)), Symbol: strings.Repeat("S", 500), Text: "t", Sources: src()}}
	f, err := Check(fileJSON(t, items), Known{})
	if err != nil {
		t.Fatal(err)
	}
	d := f.Dropped[0]
	if len([]rune(d.Kind)) > 41 || len([]rune(d.Symbol)) > 21 || len(d.Reason) > 100 {
		t.Fatalf("drop is too long: %+v", d)
	}
}

func TestExportRoundTripsAndKnown(t *testing.T) {
	e := testExport()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeExport(b)
	if err != nil {
		t.Fatal(err)
	}
	if !got.GeneratedAt.Equal(e.GeneratedAt) || got.SignalDate != e.SignalDate ||
		len(got.Symbols) != 2 || len(got.Signals) != 2 ||
		!got.Symbols[0].Closes[0].Close.Equal(money.MustParsePrice("12.34")) {
		t.Fatalf("round trip: got %+v", got)
	}
	k := got.Known()
	if !k.Held["FAKEH"] || k.Held["FAKEW"] || k.Signals[keyHeld] != "FAKEH" || k.Signals[keyOther] != "FAKEW" {
		t.Fatalf("known: %+v", k)
	}

	for name, data := range map[string]string{
		"wrong version": `{"version":2,"generated_at":"2026-11-13T22:00:00Z","signal_date":"","symbols":[],"signals":[]}`,
		"unknown field": `{"version":1,"cash":"100.00"}`,
		"float close":   `{"version":1,"symbols":[{"symbol":"FAKEH","held":true,"watched":false,"closes":[{"date":"2026-11-13","close":12.34}]}]}`,
		"trailing data": `{"version":1} {}`,
	} {
		if _, err := DecodeExport([]byte(data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
