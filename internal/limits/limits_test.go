package limits

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every value here is made up.

var t0 = time.Date(2026, 3, 2, 21, 0, 0, 0, time.UTC)

func open(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sherwood.db"), t0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// example is the example strategy file, with hash standing in for its hash.
func example(t *testing.T, hash string) *config.Strategy {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "strategy.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := config.ParseStrategy(b)
	if err != nil {
		t.Fatal(err)
	}
	s.Hash = hash
	return s
}

const (
	hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func effective(t *testing.T, st *store.Store, s *config.Strategy) config.Limits {
	t.Helper()
	l, err := Effective(context.Background(), st.Reader(), s)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func rows(t *testing.T, st *store.Store, source Source) int {
	t.Helper()
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM settings_changes WHERE source = ?`, string(source)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecordWritesTheFileOnceAndAgainWhenItChanges(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	s := example(t, hashA)
	wrote, err := Record(ctx, st, s, t0)
	if err != nil || !wrote {
		t.Fatalf("first Record = %v, %v", wrote, err)
	}
	// The hash and the seven limits (the example sets the sector limit).
	if n := rows(t, st, NAS); n != 1+len(specs) {
		t.Fatalf("%d nas rows, want %d", n, 1+len(specs))
	}
	if wrote, err = Record(ctx, st, s, t0.Add(time.Hour)); err != nil || wrote {
		t.Fatalf("Record of the same file = %v, %v; want nothing written", wrote, err)
	}
	// A new file with one limit changed records the hash and that limit.
	s2 := example(t, hashB)
	s2.Limits.MaxOrdersPerDay = 5
	if wrote, err = Record(ctx, st, s2, t0.Add(2*time.Hour)); err != nil || !wrote {
		t.Fatalf("Record of a new file = %v, %v", wrote, err)
	}
	if n := rows(t, st, NAS); n != 1+len(specs)+2 {
		t.Fatalf("%d nas rows, want %d", n, 1+len(specs)+2)
	}
	hist, err := History(ctx, st.Reader(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if hist[0].Setting != string(OrdersPerDay) || hist[0].Old != "3" || hist[0].New != "5" || hist[0].Source != NAS {
		t.Errorf("newest change = %+v, want max_orders_per_day 3 to 5 from the nas", hist[0])
	}
}

func TestATighteningIsRecordedAndApplies(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	s := example(t, hashA)
	c, err := Tighten(ctx, st, s, t0, OrderValue, "60")
	if err != nil {
		t.Fatal(err)
	}
	if c.Old != "100.00" || c.New != "60.00" || c.Source != Phone || c.Setting != string(OrderValue) {
		t.Fatalf("change = %+v", c)
	}
	if n := rows(t, st, Phone); n != 1 {
		t.Fatalf("%d phone rows, want 1", n)
	}
	l := effective(t, st, s)
	if l.MaxOrderValue.String() != "60.00" {
		t.Errorf("max order value in force is %s, want 60.00", l.MaxOrderValue)
	}
	if s.Limits.MaxOrderValue.String() != "100.00" {
		t.Errorf("the loaded file changed: %s", s.Limits.MaxOrderValue)
	}
	// The other limits are the file's.
	if l.MaxOrdersPerDay != 3 || l.MaxStockShare.String() != "0.10" {
		t.Errorf("other limits = %+v", l)
	}
}

func TestALooseningFromTheAppIsRefused(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	s := example(t, hashA)
	if _, err := Tighten(ctx, st, s, t0, StockShare, "0.08"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  Name
		value string
		want  error
	}{
		{StockShare, "0.09", ErrLooser}, // looser than the tightening, though tighter than the file
		{StockShare, "0.20", ErrLooser},
		{StockShare, "0.080", ErrSame},
		{OrdersPerDay, "4", ErrLooser},
		{DailyLoss, "50.00", ErrSame},
		{Holdings, "21", ErrInvalid},
		{Holdings, "0", ErrInvalid},
		{StockShare, "1.5", ErrInvalid},
		{StockShare, "ten percent", ErrInvalid},
		{OrderValue, "10.001", ErrInvalid},
		{OrderValue, "-5", ErrInvalid},
		{LimitDistance, "0.005", ErrInvalid}, // below the 0.01 buy limit margin
		{"limits.buy_limit_margin", "0.005", ErrUnknown},
	} {
		if _, err := Tighten(ctx, st, s, t0, c.name, c.value); !errors.Is(err, c.want) {
			t.Errorf("Tighten(%s, %s) = %v, want %v", c.name, c.value, err, c.want)
		}
	}
	if n := rows(t, st, Phone); n != 1 {
		t.Errorf("%d phone rows, want only the first tightening", n)
	}
	if l := effective(t, st, s); l.MaxStockShare.String() != "0.08" {
		t.Errorf("max stock share in force is %s, want 0.08", l.MaxStockShare)
	}
}

func TestAnUnsetSectorLimitCanBeSet(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	s := example(t, hashA)
	s.Limits.MaxSectorShare = nil
	if _, err := Tighten(ctx, st, s, t0, SectorShare, "0.40"); err != nil {
		t.Fatal(err)
	}
	l := effective(t, st, s)
	if l.MaxSectorShare == nil || l.MaxSectorShare.String() != "0.40" {
		t.Fatalf("sector limit in force = %v, want 0.40", l.MaxSectorShare)
	}
	if s.Limits.MaxSectorShare != nil {
		t.Error("the loaded file changed")
	}
}

func TestANewStrategyFileClearsTheTightenings(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	a := example(t, hashA)
	if _, err := Record(ctx, st, a, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := Tighten(ctx, st, a, t0.Add(time.Hour), OrdersPerDay, "1"); err != nil {
		t.Fatal(err)
	}
	b := example(t, hashB)
	// Before the new file is recorded, no tightening applies to it: they
	// were made under another file.
	if l := effective(t, st, b); l.MaxOrdersPerDay != 3 {
		t.Errorf("before recording, orders per day = %d, want the file's 3", l.MaxOrdersPerDay)
	}
	if _, err := Record(ctx, st, b, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if l := effective(t, st, b); l.MaxOrdersPerDay != 3 {
		t.Errorf("orders per day = %d, want the new file's 3", l.MaxOrdersPerDay)
	}
	// Going back to the first file does not bring the old tightening back.
	if _, err := Record(ctx, st, a, t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if l := effective(t, st, a); l.MaxOrdersPerDay != 3 {
		t.Errorf("after going back, orders per day = %d, want 3", l.MaxOrdersPerDay)
	}
}

func TestAnUnreadableTighteningIsAnError(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	s := example(t, hashA)
	if _, err := Record(ctx, st, s, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Writer().Exec(`INSERT INTO settings_changes (changed_at, setting, old_value, new_value, source)
		VALUES (?, ?, '3', 'many', 'phone')`, store.FormatTime(t0), string(OrdersPerDay)); err != nil {
		t.Fatal(err)
	}
	if _, err := Effective(ctx, st.Reader(), s); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Effective = %v, want ErrCorrupt", err)
	}
}
