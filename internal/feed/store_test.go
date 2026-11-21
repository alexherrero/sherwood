package feed

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/store"
)

var t0 = time.Date(2026, 11, 14, 7, 30, 0, 0, time.UTC)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sherwood.db"), t0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	if up, err := LatestUpload(ctx, s.Reader()); err != nil || up != nil {
		t.Fatalf("before any upload: %v, %v", up, err)
	}
	if c, err := LatestCandidates(ctx, s.Reader()); err != nil || len(c) != 0 {
		t.Fatalf("candidates before any upload: %v, %v", c, err)
	}
	if e, err := Explanation(ctx, s.Reader(), keyHeld); err != nil || e != nil {
		t.Fatalf("explanation before any upload: %v, %v", e, err)
	}

	items := append(goodItems(), Item{Kind: Candidate, Symbol: "FAKEX", Text: "Another <b>made-up</b> case.",
		Sources: []Source{{URL: "https://example.org/x?a=1&b=2", Title: "One"}, {URL: "http://example.net/y", Title: "Two"}}},
		Item{Kind: Candidate, Symbol: "FAKEY", Text: "Dropped: no source."})
	f, err := Check(fileJSON(t, items), testExport().Known())
	if err != nil {
		t.Fatal(err)
	}
	up1, err := Save(ctx, s.Writer(), t0, f)
	if err != nil {
		t.Fatal(err)
	}
	if up1.Accepted != 4 || up1.Dropped != 1 {
		t.Fatalf("upload counts: %+v", up1)
	}

	latest, err := LatestUpload(ctx, s.Reader())
	if err != nil || latest == nil {
		t.Fatalf("latest upload: %v, %v", latest, err)
	}
	if *latest != up1 || !latest.At.Equal(t0) {
		t.Fatalf("latest upload %+v, want %+v", *latest, up1)
	}

	cands, err := LatestCandidates(ctx, s.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("candidates: %+v", cands)
	}
	want := []Item{items[0], items[3]}
	for i, c := range cands {
		if !reflect.DeepEqual(c.Item, want[i]) {
			t.Errorf("candidate %d: got %+v, want %+v", i, c.Item, want[i])
		}
		if c.UploadID != up1.ID || !c.UploadedAt.Equal(t0) {
			t.Errorf("candidate %d's upload: %+v", i, c)
		}
	}

	notes, err := LatestNotes(ctx, s.Reader(), []string{"FAKEH", "FAKEZ"})
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || !reflect.DeepEqual(notes["FAKEH"].Item, items[1]) {
		t.Fatalf("notes: %+v", notes)
	}

	ex, err := Explanation(ctx, s.Reader(), keyHeld)
	if err != nil || ex == nil {
		t.Fatalf("explanation: %v, %v", ex, err)
	}
	if !reflect.DeepEqual(ex.Item, items[2]) {
		t.Fatalf("explanation: got %+v, want %+v", ex.Item, items[2])
	}

	// A second upload: a new candidate, no note on FAKEH, a new explanation.
	t1 := t0.Add(24 * time.Hour)
	second := []Item{
		{Kind: Candidate, Symbol: "FAKEV", Text: "A newer case.", Sources: src()},
		{Kind: SignalExplanation, Symbol: "FAKEH", SignalKey: keyHeld, Text: "A newer explanation.", Sources: src()},
	}
	f2, err := Check(fileJSON(t, second), testExport().Known())
	if err != nil {
		t.Fatal(err)
	}
	up2, err := Save(ctx, s.Writer(), t1, f2)
	if err != nil {
		t.Fatal(err)
	}
	if up2.ID <= up1.ID {
		t.Fatalf("second upload id %d not after %d", up2.ID, up1.ID)
	}
	cands, err = LatestCandidates(ctx, s.Reader())
	if err != nil || len(cands) != 1 || cands[0].Symbol != "FAKEV" {
		t.Fatalf("candidates after the second upload: %+v, %v", cands, err)
	}
	// The note from the first upload is still the latest on FAKEH.
	notes, err = LatestNotes(ctx, s.Reader(), []string{"FAKEH"})
	if err != nil || notes["FAKEH"].UploadID != up1.ID {
		t.Fatalf("notes after the second upload: %+v, %v", notes, err)
	}
	ex, err = Explanation(ctx, s.Reader(), keyHeld)
	if err != nil || ex == nil || ex.Text != "A newer explanation." || !ex.UploadedAt.Equal(t1) {
		t.Fatalf("explanation after the second upload: %+v, %v", ex, err)
	}

	// An upload with every item dropped is still an upload, with nothing in it.
	f3, err := Check(fileJSON(t, []Item{{Kind: Candidate, Symbol: "FAKEW", Text: "no source"}}), testExport().Known())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Save(ctx, s.Writer(), t1.Add(time.Hour), f3); err != nil {
		t.Fatal(err)
	}
	if cands, err = LatestCandidates(ctx, s.Reader()); err != nil || len(cands) != 0 {
		t.Fatalf("candidates after an empty upload: %+v, %v", cands, err)
	}
}

func TestStoreIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	f, err := Check(fileJSON(t, goodItems()), testExport().Known())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Save(ctx, s.Writer(), t0, f); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`UPDATE feed_uploads SET accepted = 0`,
		`DELETE FROM feed_uploads`,
		`UPDATE feed_items SET text = 'changed'`,
		`DELETE FROM feed_items`,
	} {
		_, err := s.Writer().ExecContext(ctx, stmt)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: err %v, want append-only", stmt, err)
		}
	}
}

func TestSchemaRefusesBadRows(t *testing.T) {
	// The schema holds the same line as Check, so a row Check never made is
	// refused too.
	ctx := context.Background()
	s := openStore(t)
	w := s.Writer()
	res, err := w.ExecContext(ctx, `INSERT INTO feed_uploads (at, accepted, dropped) VALUES (?, 0, 0)`, store.FormatTime(t0))
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	ok := `[{"url":"https://example.com/","title":"x"}]`
	for name, row := range map[string][]any{
		"unknown kind":            {id, "buy_now", "FAKEW", nil, "t", ok},
		"lowercase symbol":        {id, "candidate", "fakew", nil, "t", ok},
		"explanation with no key": {id, "signal_explanation", "FAKEH", nil, "t", ok},
		"candidate with a key":    {id, "candidate", "FAKEW", keyHeld, "t", ok},
		"bad key":                 {id, "signal_explanation", "FAKEH", "xyz", "t", ok},
		"blank text":              {id, "candidate", "FAKEW", nil, " ", ok},
		"long text":               {id, "candidate", "FAKEW", nil, strings.Repeat("a", MaxText+1), ok},
		"no sources":              {id, "candidate", "FAKEW", nil, "t", `[]`},
		"sources not json":        {id, "candidate", "FAKEW", nil, "t", `nope`},
		"sources an object":       {id, "candidate", "FAKEW", nil, "t", `{"url":"x"}`},
		"no such upload":          {id + 99, "candidate", "FAKEW", nil, "t", ok},
	} {
		_, err := w.ExecContext(ctx,
			`INSERT INTO feed_items (upload_id, kind, symbol, signal_key, text, sources) VALUES (?, ?, ?, ?, ?, ?)`, row...)
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := w.ExecContext(ctx,
		`INSERT INTO feed_items (upload_id, kind, symbol, signal_key, text, sources) VALUES (?, 'candidate', 'FAKEW', NULL, ?, ?)`,
		id, strings.Repeat("é", MaxText), ok); err != nil {
		t.Errorf("text of exactly MaxText characters refused: %v", err)
	}
}
