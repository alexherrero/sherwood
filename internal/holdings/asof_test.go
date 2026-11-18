package holdings

import (
	"context"
	"errors"
	"testing"

	"github.com/alexherrero/sherwood/internal/money"
)

func TestAsOfReadsTheSnapshotInForceAtATime(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	q := s.Reader()
	if _, err := AsOf(ctx, q, t1); !errors.Is(err, ErrNone) {
		t.Fatalf("AsOf on an empty store: %v, want ErrNone", err)
	}
	if _, err := CashAsOf(ctx, q, t1); !errors.Is(err, ErrNone) {
		t.Fatalf("CashAsOf on an empty store: %v, want ErrNone", err)
	}
	first, cash0, err := Record(ctx, s, Snapshot{Source: SourceImport, AsOf: t0,
		Holdings: []Holding{holding("AAA", "2", "10.00", day(2026, 11, 2))}, Cash: money.MustParseMoney("50.00")})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Record(ctx, s, Snapshot{Source: SourceFills, AsOf: t1, Cash: money.MustParseMoney("70.00")}); err != nil {
		t.Fatal(err)
	}

	if _, err := AsOf(ctx, q, t0.Add(-1)); !errors.Is(err, ErrNone) {
		t.Errorf("AsOf before the first snapshot: %v, want ErrNone", err)
	}
	for _, at := range []string{"t0", "between"} {
		when := t0
		if at == "between" {
			when = t1.Add(-1)
		}
		set, err := AsOf(ctx, q, when)
		if err != nil || set.ID != first.ID || len(set.Holdings) != 1 || set.Holdings[0].Symbol != "AAA" {
			t.Errorf("AsOf(%s) = %+v, %v; want snapshot %d", at, set, err, first.ID)
		}
		cash, err := CashAsOf(ctx, q, when)
		if err != nil || cash.ID != cash0.ID || cash.Amount.String() != "50.00" {
			t.Errorf("CashAsOf(%s) = %+v, %v", at, cash, err)
		}
	}
	set, err := AsOf(ctx, q, t1)
	if err != nil || set.ID == first.ID || len(set.Holdings) != 0 {
		t.Errorf("AsOf(t1) = %+v, %v; want the later, empty snapshot", set, err)
	}
	if cash, err := CashAsOf(ctx, q, t1); err != nil || cash.Amount.String() != "70.00" {
		t.Errorf("CashAsOf(t1) = %+v, %v", cash, err)
	}
}
