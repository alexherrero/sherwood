package limits

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/store"
)

// Every symbol here is made up.

func TestAnAdditionIsRecordedAndJoinsTheWatchlistInForce(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	s := example(t, hashA)
	s.Sectors["NEWA"] = "technology"
	s.Sectors["NEWB"] = "technology"

	c, err := Watch(ctx, st, s, t0, "NEWA")
	if err != nil {
		t.Fatal(err)
	}
	if c.Setting != WatchSetting || c.New != "NEWA" || c.Old != "" || c.Source != Phone {
		t.Errorf("change = %+v, want watchlist.add NEWA from the phone", c)
	}
	if _, err := Watch(ctx, st, s, t0.Add(time.Minute), "NEWB"); err != nil {
		t.Fatal(err)
	}
	got, err := Watchlist(ctx, st.Reader(), s)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"AAPL", "MSFT", "KO", "NEWA", "NEWB"}; !slices.Equal(got, want) {
		t.Errorf("watchlist in force = %v, want %v", got, want)
	}
	if !slices.Equal(s.Watchlist, []string{"AAPL", "MSFT", "KO"}) {
		t.Errorf("the strategy file's own watchlist changed to %v", s.Watchlist)
	}
	// The additions sit beside the limits without disturbing them.
	if l := effective(t, st, s); l.MaxOrdersPerDay != s.Limits.MaxOrdersPerDay {
		t.Errorf("limits in force changed after an addition: %+v", l)
	}
	if _, err := Tighten(ctx, st, s, t0.Add(2*time.Minute), OrdersPerDay, "2"); err != nil {
		t.Fatal(err)
	}
	if l := effective(t, st, s); l.MaxOrdersPerDay != 2 {
		t.Errorf("orders per day = %d after the tightening, want 2", l.MaxOrdersPerDay)
	}
	h, err := History(ctx, st.Reader(), 10)
	if err != nil {
		t.Fatal(err)
	}
	var added []string
	for _, c := range h {
		if c.Setting == WatchSetting {
			added = append(added, c.New)
		}
	}
	if !slices.Equal(added, []string{"NEWB", "NEWA"}) {
		t.Errorf("history lists additions %v, want NEWB then NEWA", added)
	}
}

func TestWatchRefuses(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	s := example(t, hashA)
	s.Sectors["NEWA"] = "technology"
	if _, err := Watch(ctx, st, s, t0, "NEWA"); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		sym  string
		want error
	}{
		"on the file's watchlist":   {"AAPL", ErrWatched},
		"added already":             {"NEWA", ErrWatched},
		"no sector, limit in force": {"NOSEC", ErrNoSector},
		"markup":                    {"<b>X</b>", ErrNotSymbol},
		"empty":                     {"", ErrNotSymbol},
		"lower case":                {"newa", ErrNotSymbol},
		"too long":                  {"ABCDEFGHIJK", ErrNotSymbol},
	}
	for name, c := range cases {
		if _, err := Watch(ctx, st, s, t0.Add(time.Minute), c.sym); !errors.Is(err, c.want) {
			t.Errorf("%s: Watch(%q) = %v, want %v", name, c.sym, err, c.want)
		}
	}
	if n := rows(t, st, Phone); n != 1 {
		t.Errorf("%d phone rows, want only the first addition", n)
	}
}

func TestWithNoSectorLimitAStockNeedsNoSector(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	s := example(t, hashA)
	s.Limits.MaxSectorShare = nil
	if _, err := Watch(ctx, st, s, t0, "NOSEC"); err != nil {
		t.Fatalf("Watch with no sector limit = %v, want it added", err)
	}
	// A sector limit tightened in from the phone counts as in force.
	s2 := example(t, hashB)
	s2.Limits.MaxSectorShare = nil
	if _, err := Tighten(ctx, st, s2, t0.Add(time.Minute), SectorShare, "0.30"); err != nil {
		t.Fatal(err)
	}
	if _, err := Watch(ctx, st, s2, t0.Add(2*time.Minute), "NOSEC"); !errors.Is(err, ErrNoSector) {
		t.Errorf("Watch under a tightened sector limit = %v, want ErrNoSector", err)
	}
}

func TestTheAdditionsResetWhenTheStrategyFileChanges(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	a := example(t, hashA)
	a.Sectors["NEWA"] = "technology"
	if _, err := Record(ctx, st, a, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := Watch(ctx, st, a, t0.Add(time.Minute), "NEWA"); err != nil {
		t.Fatal(err)
	}

	// The file changes and sherwood restarts: the addition no longer applies.
	b := example(t, hashB)
	b.Sectors["NEWA"] = "technology"
	if _, err := Record(ctx, st, b, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := Watchlist(ctx, st.Reader(), b)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, b.Watchlist) {
		t.Errorf("after the file changed the watchlist is %v, want the file's %v", got, b.Watchlist)
	}
	// Before the restart records the new file, the old addition does not
	// apply to it either.
	c := example(t, "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	if got, err := Watchlist(ctx, st.Reader(), c); err != nil || !slices.Equal(got, c.Watchlist) {
		t.Errorf("an unrecorded file's watchlist = %v, %v; want the file's own", got, err)
	}

	// Going back to the first file does not bring the old addition back.
	if _, err := Record(ctx, st, a, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, err := Watchlist(ctx, st.Reader(), a); err != nil || !slices.Equal(got, a.Watchlist) {
		t.Errorf("back on the first file the watchlist is %v, %v; want the file's own", got, err)
	}
	// And it can be added again under the file in force.
	if _, err := Watch(ctx, st, a, t0.Add(3*time.Hour), "NEWA"); err != nil {
		t.Errorf("adding again after the reset = %v", err)
	}
}

func TestAnUnreadableAdditionFailsClosed(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	s := example(t, hashA)
	if _, err := Record(ctx, st, s, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Writer().Exec(
		`INSERT INTO settings_changes (changed_at, setting, old_value, new_value, source) VALUES (?, ?, NULL, ?, 'phone')`,
		store.FormatTime(t0.Add(time.Minute)), WatchSetting, "not a symbol"); err != nil {
		t.Fatal(err)
	}
	if _, err := Watchlist(ctx, st.Reader(), s); !errors.Is(err, ErrCorrupt) {
		t.Errorf("Watchlist over an unreadable row = %v, want ErrCorrupt", err)
	}
	// The limits never read the watchlist's rows.
	if _, err := Effective(ctx, st.Reader(), s); err != nil {
		t.Errorf("Effective = %v, want the limits read past the watchlist row", err)
	}
}
