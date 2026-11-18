package orders

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
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every symbol, price and strategy here is made up.

var (
	t0      = time.Date(2026, 3, 2, 21, 0, 0, 0, time.UTC)
	day0    = time.Date(2026, 3, 2, 0, 0, 0, 0, clock.Eastern)
	hash    = strings.Repeat("ab", 32)
	kinds   = []signals.Kind{signals.KindBuy, signals.KindAdd, signals.KindTrim, signals.KindSell, signals.KindStopSell}
	tenShrs = money.MustParseQuantity("10")
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sherwood.db"), t0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func price(s string) *money.Price {
	p := money.MustParsePrice(s)
	return &p
}

// newOrder is a valid order of kind k; n varies the symbol so each call
// gets its own key.
func newOrder(k signals.Kind, n int) New {
	o := New{
		Strategy:     "example-pullback",
		Symbol:       fmt.Sprintf("T%d", n),
		Date:         day0,
		Kind:         k,
		Quantity:     tenShrs,
		Reason:       "pullback: made-up reason",
		StrategyHash: hash,
	}
	if TypeFor(k) != SellAtOpen {
		o.Price = price("10.00")
	}
	return o
}

func create(t *testing.T, s *store.Store, n New) Order {
	t.Helper()
	var o Order
	err := Update(context.Background(), s, func(tx *sql.Tx) error {
		var err error
		o, _, err = Create(context.Background(), tx, n, Proposer, t0)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// step moves an order to `to` the way callers do: through RecordFill for the
// two filled states, through Move for the rest.
func step(s *store.Store, o Order, to State, at time.Time) (Order, error) {
	ctx := context.Background()
	var out Order
	err := Update(ctx, s, func(tx *sql.Tx) error {
		var err error
		switch to {
		case Filled, PartiallyFilled:
			fills, err := Fills(ctx, tx, o.ID)
			if err != nil {
				return err
			}
			left := o.Quantity
			for _, f := range fills {
				if left, err = left.Sub(f.Quantity); err != nil {
					return err
				}
			}
			q := left
			if to == PartiallyFilled {
				q = money.MustParseQuantity("1")
			}
			out, _, err = RecordFill(ctx, tx, o.ID, NewFill{Date: day0, Quantity: q,
				Price: money.MustParsePrice("9.99"), Reason: "made-up fill", Actor: Broker, At: at})
			return err
		default:
			out, err = Move(ctx, tx, o.ID, Change{To: to, Actor: Execution, Reason: "test move to " + string(to), At: at})
			return err
		}
	})
	return out, err
}

// pathTo is the shortest path through the state machine from Proposed to
// target for kind k, or nil if target can't be reached.
func pathTo(k signals.Kind, target State) []State {
	prev := map[State]State{Proposed: ""}
	queue := []State{Proposed}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		if at == target {
			var path []State
			for s := at; s != Proposed; s = prev[s] {
				path = append([]State{s}, path...)
			}
			return append([]State{Proposed}, path...)
		}
		for _, next := range Next(at, k) {
			if _, seen := prev[next]; !seen {
				prev[next] = at
				queue = append(queue, next)
			}
		}
	}
	return nil
}

// expected is the state machine written out independently of transitions.go.
// "!stop" means every kind but stop_sell; "stop" means stop_sell only.
var expected = map[[2]State]string{
	{Proposed, AwaitingApproval}: "!stop",
	{Proposed, Approved}:         "stop",
	{Proposed, Blocked}:          "all",
	{Proposed, Declined}:         "all",

	{AwaitingApproval, Approved}: "all",
	{AwaitingApproval, Declined}: "all",
	{AwaitingApproval, Blocked}:  "all",

	{Approved, Submitted}: "all",
	{Approved, Blocked}:   "all",
	{Approved, Cancelled}: "all",
	{Approved, Expired}:   "all",

	{Submitted, Accepted}:        "all",
	{Submitted, Rejected}:        "all",
	{Submitted, PartiallyFilled}: "all",
	{Submitted, Filled}:          "all",
	{Submitted, Cancelled}:       "all",
	{Submitted, Expired}:         "all",
	{Submitted, Unknown}:         "all",

	{Accepted, PartiallyFilled}: "all",
	{Accepted, Filled}:          "all",
	{Accepted, Cancelled}:       "all",
	{Accepted, Rejected}:        "all",
	{Accepted, Expired}:         "all",
	{Accepted, Unknown}:         "all",

	{PartiallyFilled, Filled}:    "all",
	{PartiallyFilled, Cancelled}: "all",
	{PartiallyFilled, Expired}:   "all",
	{PartiallyFilled, Unknown}:   "all",

	{Unknown, Accepted}:        "all",
	{Unknown, PartiallyFilled}: "all",
	{Unknown, Filled}:          "all",
	{Unknown, Cancelled}:       "all",
	{Unknown, Rejected}:        "all",
	{Unknown, Expired}:         "all",
}

func expectAllowed(from, to State, k signals.Kind) bool {
	switch expected[[2]State{from, to}] {
	case "all":
		return true
	case "!stop":
		return k != signals.KindStopSell
	case "stop":
		return k == signals.KindStopSell
	}
	return false
}

func TestTableMatchesTheDesign(t *testing.T) {
	if len(States) != 13 {
		t.Fatalf("%d states, want the design's 13", len(States))
	}
	for _, k := range kinds {
		for _, from := range States {
			for _, to := range States {
				if got, want := Allowed(from, to, k), expectAllowed(from, to, k); got != want {
					t.Errorf("Allowed(%s → %s, %s) = %v, want %v", from, to, k, got, want)
				}
			}
		}
	}
	for _, s := range []State{Declined, Blocked, Filled, Cancelled, Rejected, Expired} {
		if !s.Terminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range []State{Proposed, AwaitingApproval, Approved, Submitted, Accepted, PartiallyFilled, Unknown} {
		if s.Terminal() {
			t.Errorf("%s should not be terminal", s)
		}
	}
	if Allowed("nonsense", Proposed, signals.KindBuy) || State("nonsense").Valid() {
		t.Error("an unknown state was accepted")
	}
}

// TestEveryPairThroughTheStore drives a fresh order to each reachable state
// for each kind and then tries every possible next state: the allowed ones
// succeed and record history; every other pair is refused with a
// *TransitionError (or ErrInvalid for a direct Move into a filled state) and
// writes nothing.
func TestEveryPairThroughTheStore(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	n := 0
	allowedSeen := 0
	for _, k := range kinds {
		for _, from := range States {
			path := pathTo(k, from)
			if path == nil {
				// Only awaiting_approval is out of a protective sell's reach.
				if k != signals.KindStopSell || from != AwaitingApproval {
					t.Errorf("%s can't reach %s", k, from)
				}
				continue
			}
			for _, to := range States {
				n++
				o := create(t, s, newOrder(k, n))
				at := t0
				var err error
				for _, st := range path[1:] {
					at = at.Add(time.Minute)
					if o, err = step(s, o, st, at); err != nil {
						t.Fatalf("%s: reaching %s via %v: %v", k, from, path, err)
					}
				}
				before, err := History(ctx, s.Reader(), o.ID)
				if err != nil {
					t.Fatal(err)
				}
				got, err := step(s, o, to, at.Add(time.Minute))
				name := fmt.Sprintf("%s: %s → %s", k, from, to)

				if expectAllowed(from, to, k) {
					allowedSeen++
					if err != nil {
						t.Errorf("%s: refused: %v", name, err)
						continue
					}
					stored, err := Get(ctx, s.Reader(), o.ID)
					if err != nil {
						t.Fatal(err)
					}
					if got.State != to || stored.State != to {
						t.Errorf("%s: state %s (stored %s)", name, got.State, stored.State)
					}
					after, _ := History(ctx, s.Reader(), o.ID)
					if len(after) != len(before)+1 || after[len(after)-1].From != from || after[len(after)-1].To != to {
						t.Errorf("%s: history not appended: %+v", name, after)
					}
					continue
				}

				var te *TransitionError
				switch {
				case from == to && (to == Filled || to == PartiallyFilled):
					// A second partial fill stays partially_filled without a
					// move, and a filled order can take no more shares; the
					// self-move itself is refused by Move (before it touches
					// the transaction).
					if _, err := Move(ctx, nil, o.ID, Change{To: to, Actor: Broker, Reason: "x", At: at}); !errors.Is(err, ErrInvalid) {
						t.Errorf("%s: Move gave %v, want ErrInvalid", name, err)
					}
				case err == nil:
					t.Errorf("%s: allowed, want refused", name)
				case from == Filled && to == PartiallyFilled:
					if !errors.Is(err, ErrOverfill) {
						t.Errorf("%s: got %v, want ErrOverfill", name, err)
					}
				case !errors.As(err, &te) || !errors.Is(err, ErrIllegalTransition):
					t.Errorf("%s: got %v, want a TransitionError", name, err)
				case te.From != from || te.To != to || te.Kind != k || te.OrderID != o.ID:
					t.Errorf("%s: TransitionError %+v", name, te)
				}
				if from == to && (to == Filled || to == PartiallyFilled) {
					continue
				}
				after, _ := History(ctx, s.Reader(), o.ID)
				stored, _ := Get(ctx, s.Reader(), o.ID)
				fills, _ := Fills(ctx, s.Reader(), o.ID)
				if len(after) != len(before) || stored.State != from {
					t.Errorf("%s: a refused move wrote something: state %s, %d events (was %d)", name, stored.State, len(after), len(before))
				}
				if from != PartiallyFilled && from != Filled && len(fills) != 0 {
					t.Errorf("%s: a refused fill was written", name)
				}
			}
		}
	}
	// 34 pairs, less one per kind for the approval split, times five kinds,
	// less stop_sell's three moves out of awaiting_approval.
	if want := (34-1)*5 - 3; allowedSeen != want {
		t.Errorf("tested %d allowed moves, want %d", allowedSeen, want)
	}
}

func TestBuysNeedApprovalAndProtectiveSellsSkipIt(t *testing.T) {
	s := openStore(t)
	buy := create(t, s, newOrder(signals.KindBuy, 1))
	if _, err := step(s, buy, Approved, t0.Add(time.Minute)); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("a buy skipped approval: %v", err)
	}
	stop := create(t, s, newOrder(signals.KindStopSell, 2))
	if _, err := step(s, stop, AwaitingApproval, t0.Add(time.Minute)); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("a protective sell went to approval: %v", err)
	}
	if o, err := step(s, stop, Approved, t0.Add(time.Minute)); err != nil || o.State != Approved {
		t.Errorf("a protective sell couldn't go straight to approved: %v", err)
	}
}

func TestDuplicateKeyReturnsTheExistingOrder(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	n := newOrder(signals.KindBuy, 1)
	first := create(t, s, n)
	if first.Key != signals.Key(n.Strategy, n.Symbol, signals.SideBuy, signals.KindBuy, day0) {
		t.Errorf("key %s is not signals.Key", first.Key)
	}
	moved, err := step(s, first, AwaitingApproval, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	again := n
	again.Quantity = money.MustParseQuantity("99")
	again.Reason = "a different reason"
	again.Date = day0.Add(15 * time.Hour) // same Eastern date
	var got Order
	var created bool
	if err := Update(ctx, s, func(tx *sql.Tx) error {
		got, created, err = Create(ctx, tx, again, Proposer, t0.Add(time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if created || got.ID != first.ID || got.State != AwaitingApproval || !got.Quantity.Equal(tenShrs) || got.Reason != n.Reason {
		t.Errorf("duplicate key gave %+v (created %v), want the existing order %+v", got, created, moved)
	}
	var count, events int
	_ = s.Reader().QueryRow(`SELECT count(*) FROM orders`).Scan(&count)
	_ = s.Reader().QueryRow(`SELECT count(*) FROM order_history`).Scan(&events)
	if count != 1 || events != 2 {
		t.Errorf("%d orders and %d history rows, want 1 and 2", count, events)
	}
}

func TestStateRebuildsFromHistory(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	o := create(t, s, newOrder(signals.KindBuy, 1))
	at := t0
	for _, to := range []State{AwaitingApproval, Approved, Submitted, Unknown, Accepted, PartiallyFilled, Unknown, PartiallyFilled, Filled} {
		at = at.Add(time.Minute)
		var err error
		if o, err = step(s, o, to, at); err != nil {
			t.Fatalf("→ %s: %v", to, err)
		}
		h, err := History(ctx, s.Reader(), o.ID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Replay(o.Kind, h)
		if err != nil || got != to {
			t.Fatalf("after → %s, replay gives %s, %v", to, got, err)
		}
	}
	h, _ := History(ctx, s.Reader(), o.ID)
	if len(h) != 10 || h[0].From != "" || h[0].To != Proposed || h[0].Actor != Proposer {
		t.Errorf("history %+v", h)
	}
	fills, _ := Fills(ctx, s.Reader(), o.ID)
	total := money.MustParseQuantity("0")
	for _, f := range fills {
		total = total.Add(f.Quantity)
	}
	if len(fills) != 3 || !total.Equal(tenShrs) {
		t.Errorf("fills %+v", fills)
	}

	// Replay refuses a broken chain and an illegal move.
	broken := append([]Event(nil), h...)
	broken[3].From = Proposed
	if _, err := Replay(o.Kind, broken); !errors.Is(err, ErrCorrupt) {
		t.Errorf("broken chain: %v", err)
	}
	illegal := []Event{{To: Proposed}, {From: Proposed, To: Filled}}
	if _, err := Replay(o.Kind, illegal); !errors.Is(err, ErrIllegalTransition) {
		t.Errorf("illegal move: %v", err)
	}
	if _, err := Replay(o.Kind, nil); !errors.Is(err, ErrCorrupt) {
		t.Errorf("empty history: %v", err)
	}
}

func TestFillsAreExactAndCannotOverfill(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	n := newOrder(signals.KindStopSell, 1)
	n.Quantity = money.MustParseQuantity("3.5")
	o := create(t, s, n)
	for _, to := range []State{Approved, Submitted, Accepted} {
		var err error
		if o, err = step(s, o, to, t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	record := func(q string) (Order, Fill, error) {
		var (
			ord Order
			f   Fill
		)
		err := Update(ctx, s, func(tx *sql.Tx) error {
			var err error
			ord, f, err = RecordFill(ctx, tx, o.ID, NewFill{Date: day0, Quantity: money.MustParseQuantity(q),
				Price: money.MustParsePrice("10.01"), Reason: "opened at 10.01, at or below its stop", Actor: Broker, At: t0.Add(time.Hour)})
			return err
		})
		return ord, f, err
	}
	ord, f, err := record("1.5")
	if err != nil || ord.State != PartiallyFilled || f.Value.String() != "15.015" {
		t.Fatalf("first fill: %v %s %s", err, ord.State, f.Value)
	}
	if _, _, err := record("2.000001"); !errors.Is(err, ErrOverfill) {
		t.Errorf("overfill: %v", err)
	}
	ord, f, err = record("2")
	if err != nil || ord.State != Filled || f.Value.String() != "20.02" {
		t.Fatalf("second fill: %v %s %s", err, ord.State, f.Value)
	}
	if _, _, err := record("0.000001"); !errors.Is(err, ErrOverfill) {
		t.Errorf("fill after filled: %v", err)
	}
	var stored string
	_ = s.Reader().QueryRow(`SELECT group_concat(value, ',') FROM fills`).Scan(&stored)
	if stored != "15.015,20.02" {
		t.Errorf("stored values %q", stored)
	}
}

func TestVerdictIsKeptOnTheOrderAndItsHistory(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	o := create(t, s, newOrder(signals.KindBuy, 1))
	err := Update(ctx, s, func(tx *sql.Tx) error {
		_, err := Move(ctx, tx, o.ID, Change{To: AwaitingApproval, Actor: Risk, Reason: "passed the risk check",
			Verdict: "max order value: pass", At: t0.Add(time.Minute)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := step(s, o, Approved, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ := Get(ctx, s.Reader(), o.ID)
	h, _ := History(ctx, s.Reader(), o.ID)
	if got.Verdict != "max order value: pass" || h[1].Verdict != "max order value: pass" || h[2].Verdict != "" {
		t.Errorf("verdict %q, history %+v", got.Verdict, h)
	}
	if !got.Updated.Equal(t0.Add(2*time.Minute)) || !got.Created.Equal(t0) {
		t.Errorf("times %s %s", got.Created, got.Updated)
	}
}

func TestRefusals(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	bad := map[string]func(*New){
		"no strategy":         func(n *New) { n.Strategy = "" },
		"lower-case symbol":   func(n *New) { n.Symbol = "abc" },
		"long symbol":         func(n *New) { n.Symbol = "ABCDEFGHIJK" },
		"no date":             func(n *New) { n.Date = time.Time{} },
		"unknown kind":        func(n *New) { n.Kind = "short" },
		"no shares":           func(n *New) { n.Quantity = money.MustParseQuantity("0") },
		"no reason":           func(n *New) { n.Reason = "" },
		"bad hash":            func(n *New) { n.StrategyHash = "abc" },
		"buy without a limit": func(n *New) { n.Price = nil },
		"zero limit":          func(n *New) { n.Price = price("0.00") },
	}
	for name, mutate := range bad {
		n := newOrder(signals.KindBuy, 1)
		mutate(&n)
		err := Update(ctx, s, func(tx *sql.Tx) error {
			_, _, err := Create(ctx, tx, n, Proposer, t0)
			return err
		})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	trim := newOrder(signals.KindTrim, 1)
	trim.Price = price("10.00")
	if err := Update(ctx, s, func(tx *sql.Tx) error { _, _, err := Create(ctx, tx, trim, Proposer, t0); return err }); !errors.Is(err, ErrInvalid) {
		t.Errorf("a sell at the open with a price: %v", err)
	}
	if err := Update(ctx, s, func(tx *sql.Tx) error {
		_, _, err := Create(ctx, tx, newOrder(signals.KindBuy, 1), "Bad Actor", t0)
		return err
	}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad actor: %v", err)
	}

	o := create(t, s, newOrder(signals.KindBuy, 1))
	for name, c := range map[string]Change{
		"no reason":     {To: AwaitingApproval, Actor: Risk, At: t0},
		"no time":       {To: AwaitingApproval, Actor: Risk, Reason: "x"},
		"unknown state": {To: "lost", Actor: Risk, Reason: "x", At: t0},
		"bad actor":     {To: AwaitingApproval, Actor: "", Reason: "x", At: t0},
		"going back":    {To: AwaitingApproval, Actor: Risk, Reason: "x", At: t0.Add(-time.Second)},
		"direct fill":   {To: Filled, Actor: Broker, Reason: "x", At: t0},
	} {
		err := Update(ctx, s, func(tx *sql.Tx) error { _, err := Move(ctx, tx, o.ID, c); return err })
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	err := Update(ctx, s, func(tx *sql.Tx) error {
		_, err := Move(ctx, tx, 999, Change{To: AwaitingApproval, Actor: Risk, Reason: "x", At: t0})
		return err
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("missing order: %v", err)
	}
	if _, err := ByKey(ctx, s.Reader(), strings.Repeat("0", 64)); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing key: %v", err)
	}
}

func TestReaders(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	a := create(t, s, newOrder(signals.KindBuy, 1))
	b := create(t, s, newOrder(signals.KindSell, 2))
	later := newOrder(signals.KindBuy, 3)
	later.Date = day0.AddDate(0, 0, 1)
	c := create(t, s, later)
	if _, err := step(s, b, AwaitingApproval, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	day, err := ForDate(ctx, s.Reader(), day0.Add(20*time.Hour))
	if err != nil || len(day) != 2 || day[0].ID != a.ID || day[1].ID != b.ID {
		t.Errorf("ForDate: %v %+v", err, day)
	}
	if day[1].Price != nil || day[1].Type != SellAtOpen || day[0].Price.String() != "10.00" || day[0].Type != DayLimitBuy {
		t.Errorf("types and prices: %+v", day)
	}
	if !day[0].Date.Equal(day0) || day[0].Date.Location() != clock.Eastern {
		t.Errorf("date %s", day[0].Date)
	}
	got, err := InState(ctx, s.Reader(), Proposed, AwaitingApproval)
	if err != nil || len(got) != 3 || got[0].ID != a.ID || got[1].ID != b.ID || got[2].ID != c.ID {
		t.Errorf("InState: %v %+v", err, got)
	}
	if got, _ := InState(ctx, s.Reader(), AwaitingApproval); len(got) != 1 || got[0].ID != b.ID {
		t.Errorf("InState(awaiting): %+v", got)
	}
	if _, err := InState(ctx, s.Reader(), "lost"); !errors.Is(err, ErrInvalid) {
		t.Errorf("InState(unknown state): %v", err)
	}
	if byKey, err := ByKey(ctx, s.Reader(), c.Key); err != nil || byKey.ID != c.ID {
		t.Errorf("ByKey: %v", err)
	}
	if b.Protective() || !(Order{Kind: signals.KindStopSell}).Protective() {
		t.Error("Protective")
	}
}

// The schema holds the record's shape on its own, whatever the Go code does.
func TestSchemaRefusesUnrecordedChanges(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	o := create(t, s, newOrder(signals.KindBuy, 1))
	if _, err := step(s, o, AwaitingApproval, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := step(s, create(t, s, newOrder(signals.KindStopSell, 2)), Approved, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	stop, _ := ByKey(ctx, s.Reader(), newOrder(signals.KindStopSell, 2).Key())
	for _, to := range []State{Submitted, Accepted} {
		if stop, _ = step(s, stop, to, t0.Add(2*time.Minute)); stop.State != to {
			t.Fatal("setup")
		}
	}
	if _, err := step(s, stop, PartiallyFilled, t0.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}

	refused := map[string]string{
		"history update":          `UPDATE order_history SET reason = 'edited'`,
		"history delete":          `DELETE FROM order_history`,
		"fills update":            `UPDATE fills SET price = '1.00'`,
		"fills delete":            `DELETE FROM fills`,
		"order delete":            `DELETE FROM orders`,
		"quantity change":         `UPDATE orders SET quantity = '11' WHERE id = 1`,
		"key change":              `UPDATE orders SET idempotency_key = '` + strings.Repeat("c", 64) + `' WHERE id = 1`,
		"state change, no record": `UPDATE orders SET state = 'approved' WHERE id = 1`,
		"updated going back":      `UPDATE orders SET updated_at = '2026-03-02T20:00:00.000000000Z' WHERE id = 1`,
		"born approved": `INSERT INTO orders (idempotency_key, strategy, symbol, trading_date, side, kind, order_type, quantity, price, state, reason, strategy_hash, created_at, updated_at)
			VALUES ('` + strings.Repeat("d", 64) + `', 's', 'ABC', '2026-03-02', 'buy', 'buy', 'day_limit_buy', '1', '1.00', 'approved', 'r', '` + hash + `', '2026-03-02T21:00:00.000000000Z', '2026-03-02T21:00:00.000000000Z')`,
		"buy sent as a stop": `INSERT INTO orders (idempotency_key, strategy, symbol, trading_date, side, kind, order_type, quantity, price, state, reason, strategy_hash, created_at, updated_at)
			VALUES ('` + strings.Repeat("d", 64) + `', 's', 'ABC', '2026-03-02', 'buy', 'buy', 'stop', '1', '1.00', 'proposed', 'r', '` + hash + `', '2026-03-02T21:00:00.000000000Z', '2026-03-02T21:00:00.000000000Z')`,
		"sell at open with a price": `INSERT INTO orders (idempotency_key, strategy, symbol, trading_date, side, kind, order_type, quantity, price, state, reason, strategy_hash, created_at, updated_at)
			VALUES ('` + strings.Repeat("d", 64) + `', 's', 'ABC', '2026-03-02', 'sell', 'trim', 'sell_at_open', '1', '1.00', 'proposed', 'r', '` + hash + `', '2026-03-02T21:00:00.000000000Z', '2026-03-02T21:00:00.000000000Z')`,
		"float quantity": `INSERT INTO orders (idempotency_key, strategy, symbol, trading_date, side, kind, order_type, quantity, price, state, reason, strategy_hash, created_at, updated_at)
			VALUES ('` + strings.Repeat("d", 64) + `', 's', 'ABC', '2026-03-02', 'buy', 'buy', 'day_limit_buy', '1e3', '1.00', 'proposed', 'r', '` + hash + `', '2026-03-02T21:00:00.000000000Z', '2026-03-02T21:00:00.000000000Z')`,
		"history skipping a state": `INSERT INTO order_history (order_id, from_state, to_state, actor, reason, at)
			VALUES (1, 'proposed', 'approved', 'operator', 'r', '2026-03-02T22:00:00.000000000Z')`,
		"second creation row": `INSERT INTO order_history (order_id, from_state, to_state, actor, reason, at)
			VALUES (1, NULL, 'proposed', 'proposer', 'r', '2026-03-02T22:00:00.000000000Z')`,
		"history back in time": `INSERT INTO order_history (order_id, from_state, to_state, actor, reason, at)
			VALUES (1, 'awaiting_approval', 'approved', 'operator', 'r', '2026-03-02T20:00:00.000000000Z')`,
	}
	for name, stmt := range refused {
		if _, err := s.Writer().ExecContext(ctx, stmt); err == nil {
			t.Errorf("%s: the schema allowed it", name)
		}
	}
	// The one update the schema allows without history is the verdict.
	if _, err := s.Writer().ExecContext(ctx, `UPDATE orders SET risk_verdict = 'recheck: pass' WHERE id = 1`); err != nil {
		t.Errorf("verdict update: %v", err)
	}
}
