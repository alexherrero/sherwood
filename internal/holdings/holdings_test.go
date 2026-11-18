package holdings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every symbol and amount here is made up.

var (
	t0 = time.Date(2026, 11, 16, 14, 30, 0, 0, time.UTC)
	t1 = t0.Add(time.Hour)
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sherwood.db"), t0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, clock.Eastern) }

func holding(sym, qty, cost string, opened time.Time) Holding {
	return Holding{Symbol: sym, Quantity: money.MustParseQuantity(qty), AverageCost: money.MustParseMoney(cost), Opened: opened}
}

func fakeHoldings(n int) []Holding {
	hs := make([]Holding, n)
	for i := range hs {
		hs[i] = holding(fmt.Sprintf("TST%02d", i), "1", "10.00", day(2026, 1, 5))
	}
	return hs
}

func inTx(t *testing.T, s *store.Store, f func(tx *sql.Tx) error) error {
	t.Helper()
	tx, err := s.Writer().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	if _, err := Current(ctx, s.Reader()); !errors.Is(err, ErrNone) {
		t.Fatalf("Current on an empty store: err = %v, want ErrNone", err)
	}
	if _, err := LatestCash(ctx, s.Reader()); !errors.Is(err, ErrNone) {
		t.Fatalf("LatestCash on an empty store: err = %v, want ErrNone", err)
	}

	in := Snapshot{
		Source: SourceImport,
		AsOf:   t0,
		Holdings: []Holding{
			holding("XYZ.B", "2.500001", "41.123456", day(2025, 3, 4)),
			holding("ABC", "10", "12.50", day(2026, 6, 1)),
		},
		Cash: money.MustParseMoney("1234.5678"),
	}
	wrote, cash, err := Record(ctx, s, in)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, err := Current(ctx, s.Reader())
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if got.ID != wrote.ID || got.Source != SourceImport || !got.AsOf.Equal(t0) || got.OverLimit {
		t.Fatalf("header = %+v, want id %d, import, %v, not over the limit", got, wrote.ID, t0)
	}
	if len(got.Holdings) != 2 || got.Holdings[0].Symbol != "ABC" || got.Holdings[1].Symbol != "XYZ.B" {
		t.Fatalf("holdings = %+v, want ABC then XYZ.B", got.Holdings)
	}
	x, ok := got.Find("XYZ.B")
	if !ok {
		t.Fatal("Find(XYZ.B) found nothing")
	}
	if x.Quantity.String() != "2.500001" || x.AverageCost.String() != "41.123456" {
		t.Fatalf("XYZ.B = %s @ %s, want 2.500001 @ 41.123456 exactly", x.Quantity, x.AverageCost)
	}
	if !x.Opened.Equal(day(2025, 3, 4)) || x.Opened.Location() != clock.Eastern {
		t.Fatalf("XYZ.B opened %v, want midnight Eastern on 2025-03-04", x.Opened)
	}
	if cp, err := x.CostPrice(); err != nil || cp.String() != "41.12" {
		t.Fatalf("CostPrice = %s, %v; want 41.12", cp, err)
	}
	if _, ok := got.Find("NOPE"); ok {
		t.Fatal("Find(NOPE) found a holding")
	}
	for i := range got.Holdings {
		if !got.Holdings[i].Opened.Equal(wrote.Holdings[i].Opened) || got.Holdings[i].Symbol != wrote.Holdings[i].Symbol {
			t.Fatalf("Record returned %+v but Current read %+v", wrote.Holdings[i], got.Holdings[i])
		}
	}

	c, err := LatestCash(ctx, s.Reader())
	if err != nil {
		t.Fatalf("LatestCash: %v", err)
	}
	if c.ID != cash.ID || c.Amount.String() != "1234.5678" || c.Source != SourceImport || !c.AsOf.Equal(t0) {
		t.Fatalf("cash = %+v, want 1234.5678 from import as of %v", c, t0)
	}

	// The values are stored as text, exactly as written.
	var qty, cost, amount string
	if err := s.Reader().QueryRow(`SELECT quantity, average_cost FROM current_holdings WHERE symbol = 'XYZ.B'`).Scan(&qty, &cost); err != nil {
		t.Fatal(err)
	}
	if err := s.Reader().QueryRow(`SELECT amount FROM current_cash`).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	if qty != "2.500001" || cost != "41.123456" || amount != "1234.5678" {
		t.Fatalf("stored text = %q, %q, %q", qty, cost, amount)
	}
	var typ string
	if err := s.Reader().QueryRow(`SELECT typeof(amount) FROM cash_snapshots`).Scan(&typ); err != nil || typ != "text" {
		t.Fatalf("typeof(amount) = %q, %v; want text", typ, err)
	}
}

func TestReplaceKeepsHistory(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	first, _, err := Record(ctx, s, Snapshot{Source: SourceImport, AsOf: t0, Holdings: fakeHoldings(3), Cash: money.Cents(100)})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := Record(ctx, s, Snapshot{
		Source:   SourceFills,
		AsOf:     t1,
		Holdings: []Holding{holding("ABC", "4", "9.99", day(2026, 11, 16))},
		Cash:     money.MustParseMoney("-5.00"),
	})
	if err != nil {
		t.Fatal(err)
	}

	cur, err := Current(ctx, s.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if cur.ID != second.ID || cur.Source != SourceFills || len(cur.Holdings) != 1 || cur.Holdings[0].Symbol != "ABC" {
		t.Fatalf("current = %+v, want the fills snapshot with only ABC", cur)
	}
	old, err := Get(ctx, s.Reader(), first.ID)
	if err != nil || len(old.Holdings) != 3 || old.Source != SourceImport {
		t.Fatalf("earlier snapshot = %+v, %v; want the 3-holding import kept", old, err)
	}
	if _, err := Get(ctx, s.Reader(), 999); !errors.Is(err, ErrNone) {
		t.Fatalf("Get(999): err = %v, want ErrNone", err)
	}
	c, err := LatestCash(ctx, s.Reader())
	if err != nil || c.Amount.String() != "-5.00" || c.Source != SourceFills {
		t.Fatalf("latest cash = %+v, %v; want -5.00 from fills", c, err)
	}

	// The current_holdings view agrees with Current.
	var n int
	if err := s.Reader().QueryRow(`SELECT count(*) FROM current_holdings`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("current_holdings has %d rows (%v), want 1", n, err)
	}

	// An empty set is recorded, and is not ErrNone.
	if _, _, err := Record(ctx, s, Snapshot{Source: SourceReconcile, AsOf: t1, Cash: money.Cents(0)}); err != nil {
		t.Fatalf("recording an empty set: %v", err)
	}
	empty, err := Current(ctx, s.Reader())
	if err != nil || len(empty.Holdings) != 0 || empty.Source != SourceReconcile {
		t.Fatalf("current after an empty set = %+v, %v", empty, err)
	}
}

func TestOverLimitIsRecordedAndFlagged(t *testing.T) {
	if Limit != 20 {
		t.Fatalf("Limit = %d; migration 0003's CHECK says 20, so change both together", Limit)
	}
	ctx := context.Background()
	s := openStore(t)

	at, _, err := Record(ctx, s, Snapshot{Source: SourceImport, AsOf: t0, Holdings: fakeHoldings(Limit), Cash: money.Cents(1)})
	if err != nil {
		t.Fatal(err)
	}
	if at.OverLimit {
		t.Fatal("20 holdings flagged as over the limit")
	}

	over, _, err := Record(ctx, s, Snapshot{Source: SourceImport, AsOf: t1, Holdings: fakeHoldings(Limit + 1), Cash: money.Cents(1)})
	if err != nil {
		t.Fatalf("a 21-holding import was refused: %v", err)
	}
	if !over.OverLimit {
		t.Fatal("Record did not flag 21 holdings")
	}
	got, err := Current(ctx, s.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if !got.OverLimit || len(got.Holdings) != Limit+1 {
		t.Fatalf("read back over_limit=%v with %d holdings, want true with 21", got.OverLimit, len(got.Holdings))
	}
	var flag int
	if err := s.Reader().QueryRow(`SELECT DISTINCT over_limit FROM current_holdings`).Scan(&flag); err != nil || flag != 1 {
		t.Fatalf("current_holdings.over_limit = %d (%v), want 1", flag, err)
	}
}

// The schema itself refuses a 21st holding written without the flag, however
// the rows are written.
func TestSchemaRefuses21stHoldingWithoutFlag(t *testing.T) {
	s := openStore(t)
	w := s.Writer()
	insRow := `INSERT INTO snapshot_holdings (snapshot_id, symbol, quantity, average_cost, opened_on) VALUES (?, ?, '1', '1.00', '2026-01-05')`

	if _, err := w.Exec(`INSERT INTO holding_snapshots (source, as_of, holding_count, over_limit) VALUES ('import', ?, 21, 0)`, store.FormatTime(t0)); err == nil {
		t.Fatal("a 21-holding snapshot without the over-limit flag was accepted")
	}
	if _, err := w.Exec(`INSERT INTO holding_snapshots (source, as_of, holding_count, over_limit) VALUES ('import', ?, 20, 1)`, store.FormatTime(t0)); err == nil {
		t.Fatal("a 20-holding snapshot with the over-limit flag was accepted")
	}

	res, err := w.Exec(`INSERT INTO holding_snapshots (source, as_of, holding_count, over_limit) VALUES ('import', ?, 20, 0)`, store.FormatTime(t0))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	for i := range 20 {
		if _, err := w.Exec(insRow, id, fmt.Sprintf("TST%02d", i)); err != nil {
			t.Fatalf("holding %d: %v", i+1, err)
		}
	}
	if _, err := w.Exec(insRow, id, "TST20"); err == nil || !strings.Contains(err.Error(), "holding_count") {
		t.Fatalf("21st holding without the flag: err = %v, want a holding_count refusal", err)
	}
}

func TestSchemaIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	first, _, err := Record(ctx, s, Snapshot{Source: SourceImport, AsOf: t0, Holdings: fakeHoldings(2), Cash: money.Cents(100)})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Record(ctx, s, Snapshot{Source: SourceImport, AsOf: t1, Holdings: fakeHoldings(1), Cash: money.Cents(100)}); err != nil {
		t.Fatal(err)
	}
	w := s.Writer()
	for _, q := range []string{
		`UPDATE holding_snapshots SET source = 'fills'`,
		`DELETE FROM holding_snapshots`,
		`UPDATE snapshot_holdings SET quantity = '2'`,
		`DELETE FROM snapshot_holdings`,
		`UPDATE cash_snapshots SET amount = '1.00'`,
		`DELETE FROM cash_snapshots`,
	} {
		if _, err := w.Exec(q); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: err = %v, want an append-only refusal", q, err)
		}
	}
	// An earlier snapshot can't gain rows, even below its count.
	if _, err := w.Exec(`INSERT INTO snapshot_holdings (snapshot_id, symbol, quantity, average_cost, opened_on) VALUES (?, 'ZZZ', '1', '1.00', '2026-01-05')`, first.ID); err == nil || !strings.Contains(err.Error(), "newest") {
		t.Fatalf("adding to an earlier snapshot: err = %v, want a newest-snapshot refusal", err)
	}
}

func TestSchemaRefusesBadRows(t *testing.T) {
	s := openStore(t)
	w := s.Writer()
	res, err := w.Exec(`INSERT INTO holding_snapshots (source, as_of, holding_count, over_limit) VALUES ('import', ?, 5, 0)`, store.FormatTime(t1))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	// A number bound to a STRICT TEXT column is stored as its text, so the
	// guard against a float is the money types' Valuer, not these CHECKs.
	row := `INSERT INTO snapshot_holdings (snapshot_id, symbol, quantity, average_cost, opened_on) VALUES (?, ?, ?, ?, ?)`
	for name, args := range map[string][]any{
		"lower-case symbol":    {id, "abc", "1", "1.00", "2026-01-05"},
		"zero quantity":        {id, "ABC", "0.000", "1.00", "2026-01-05"},
		"seven places":         {id, "ABC", "0.0000001", "1.00", "2026-01-05"},
		"negative quantity":    {id, "ABC", "-1", "1.00", "2026-01-05"},
		"exponent":             {id, "ABC", "1e3", "1.00", "2026-01-05"},
		"negative cost":        {id, "ABC", "1", "-1.00", "2026-01-05"},
		"cost with two points": {id, "ABC", "1", "1.0.0", "2026-01-05"},
		"cost ends in point":   {id, "ABC", "1", "1.", "2026-01-05"},
		"bad opened date":      {id, "ABC", "1", "1.00", "01/05/2026"},
	} {
		if _, err := w.Exec(row, args...); err == nil {
			t.Errorf("%s: bad holding accepted", name)
		}
	}
	cash := `INSERT INTO cash_snapshots (source, as_of, amount) VALUES (?, ?, ?)`
	for name, args := range map[string][]any{
		"unknown source": {"broker", store.FormatTime(t0), "1.00"},
		"bad time":       {"import", "yesterday", "1.00"},
		"plus sign":      {"import", store.FormatTime(t0), "+1.00"},
		"double minus":   {"import", store.FormatTime(t0), "--1.00"},
		"letters":        {"import", store.FormatTime(t0), "1.00a"},
	} {
		if _, err := w.Exec(cash, args...); err == nil {
			t.Errorf("%s: bad cash reading accepted", name)
		}
	}
	if _, err := w.Exec(cash, "import", store.FormatTime(t1), "1.00"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(cash, "import", store.FormatTime(t0), "1.00"); err == nil || !strings.Contains(err.Error(), "as_of order") {
		t.Fatalf("an older cash reading: err = %v, want an as_of order refusal", err)
	}
	if _, err := w.Exec(`INSERT INTO holding_snapshots (source, as_of, holding_count, over_limit) VALUES ('import', ?, 0, 0)`, store.FormatTime(t0)); err == nil || !strings.Contains(err.Error(), "as_of order") {
		t.Fatalf("an older holdings snapshot: err = %v, want an as_of order refusal", err)
	}
}

func TestReplaceRefusesAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	good := day(2026, 1, 5)
	cases := map[string]struct {
		src  Source
		asOf time.Time
		hs   []Holding
		want error
	}{
		"unknown source":   {"broker", t1, nil, ErrInvalid},
		"zero time":        {SourceImport, time.Time{}, nil, ErrInvalid},
		"bad symbol":       {SourceImport, t1, []Holding{holding("abc", "1", "1.00", good)}, ErrInvalid},
		"long symbol":      {SourceImport, t1, []Holding{holding("ABCDEFGHIJK", "1", "1.00", good)}, ErrInvalid},
		"repeated symbol":  {SourceImport, t1, []Holding{holding("ABC", "1", "1.00", good), holding("ABC", "2", "1.00", good)}, ErrInvalid},
		"zero shares":      {SourceImport, t1, []Holding{holding("ABC", "0", "1.00", good)}, ErrInvalid},
		"negative cost":    {SourceImport, t1, []Holding{holding("ABC", "1", "-1.00", good)}, ErrInvalid},
		"no opened date":   {SourceImport, t1, []Holding{holding("ABC", "1", "1.00", time.Time{})}, ErrInvalid},
		"older than now":   {SourceImport, t0.Add(-time.Second), fakeHoldings(1), ErrOutOfOrder},
		"bad later in set": {SourceImport, t1, []Holding{holding("ABC", "1", "1.00", good), holding("ABD", "0", "1.00", good)}, ErrInvalid},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := openStore(t)
			before, _, err := Record(ctx, s, Snapshot{Source: SourceImport, AsOf: t0, Holdings: fakeHoldings(2), Cash: money.Cents(7)})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = Record(ctx, s, Snapshot{Source: tc.src, AsOf: tc.asOf, Holdings: tc.hs, Cash: money.Cents(9)})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			cur, err := Current(ctx, s.Reader())
			if err != nil || cur.ID != before.ID {
				t.Fatalf("current = %+v, %v; want the earlier snapshot unchanged", cur, err)
			}
			c, err := LatestCash(ctx, s.Reader())
			if err != nil || c.Amount.String() != "0.07" {
				t.Fatalf("cash = %+v, %v; want 0.07 unchanged", c, err)
			}
		})
	}
}

func TestRecordCashRefusesOlderReading(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if err := inTx(t, s, func(tx *sql.Tx) error {
		_, err := RecordCash(ctx, tx, SourceImport, t1, money.Cents(5))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err := inTx(t, s, func(tx *sql.Tx) error {
		_, err := RecordCash(ctx, tx, SourceImport, t0, money.Cents(6))
		return err
	})
	if !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("err = %v, want ErrOutOfOrder", err)
	}
	// The same time again is allowed, and the later write wins.
	if err := inTx(t, s, func(tx *sql.Tx) error {
		_, err := RecordCash(ctx, tx, SourceReconcile, t1, money.Cents(8))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	c, err := LatestCash(ctx, s.Reader())
	if err != nil || c.Amount.String() != "0.08" || c.Source != SourceReconcile {
		t.Fatalf("cash = %+v, %v; want 0.08 from reconcile", c, err)
	}
}

// Replace and the reads work on one transaction, so a caller can write and
// read back before committing.
func TestReplaceAndReadInOneTransaction(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	err := inTx(t, s, func(tx *sql.Tx) error {
		set, err := Replace(ctx, tx, SourceImport, t0, fakeHoldings(2))
		if err != nil {
			return err
		}
		got, err := Current(ctx, tx)
		if err != nil {
			return err
		}
		if got.ID != set.ID || len(got.Holdings) != 2 {
			return fmt.Errorf("read in the transaction: %+v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHoldingArithmetic(t *testing.T) {
	h := holding("ABC", "2.5", "10.004", day(2026, 1, 5))
	if got := h.Cost(); !got.Equal(money.MustParseMoney("25.01")) {
		t.Fatalf("Cost = %s, want 25.01", got)
	}
	if got := h.Value(money.MustParsePrice("12.00")).String(); got != "30.00" {
		t.Fatalf("Value = %s, want 30.00", got)
	}
	if got, err := h.CostPrice(); err != nil || got.String() != "10.00" {
		t.Fatalf("CostPrice = %s, %v; want 10.00", got, err)
	}
}

func TestLimitMatchesConfig(t *testing.T) {
	if Limit != config.MaxHoldings {
		t.Fatalf("Limit = %d, config.MaxHoldings = %d", Limit, config.MaxHoldings)
	}
}
