package robinhood

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// OrderKeys lists every order that may have reached the broker, finished or
// not, and leaves out those never sent.
func TestOrderKeysListsEverySentOrder(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 3, 2, 16, 0, 0, 0, clock.Eastern)
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sherwood.db"), at)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	date := time.Date(2026, 3, 3, 0, 0, 0, 0, clock.Eastern)
	newOrder := func(symbol string, path ...orders.State) string {
		var o orders.Order
		err := orders.Update(ctx, s, func(tx *sql.Tx) error {
			var err error
			o, _, err = orders.Create(ctx, tx, orders.New{Strategy: "example-test", Symbol: symbol, Date: date,
				Kind: signals.KindSell, Quantity: money.MustParseQuantity("1"), Reason: "test: made-up reason",
				StrategyHash: strings.Repeat("cd", 32)}, orders.Proposer, at)
			if err != nil {
				return err
			}
			for _, to := range path {
				if o, err = orders.Move(ctx, tx, o.ID, orders.Change{To: to, Actor: orders.Execution, Reason: "test", At: at}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return o.Key
	}
	pending := []orders.State{orders.AwaitingApproval, orders.Approved}
	newOrder("ZZA", pending...) // approved, never sent
	sent := newOrder("ZZB", append(pending, orders.Submitted)...)
	accepted := newOrder("ZZC", append(pending, orders.Submitted, orders.Accepted)...)
	called := newOrder("ZZD", append(pending, orders.Submitted, orders.Accepted, orders.Cancelled)...)

	keys, err := OrderKeys(s.Reader())(ctx)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(keys)
	want := []string{sent, accepted, called}
	slices.Sort(want)
	if !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}
