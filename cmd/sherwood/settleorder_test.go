package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/execution"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// Every symbol, price and amount here is made up.

// partFilled writes a buy of 5 AAA at a limit of 10.10, accepted by the
// broker with 2 filled at 10.00, and the holdings and cash that go with it.
func partFilled(t *testing.T, f files) orders.Order {
	t.Helper()
	ctx := context.Background()
	at := covered.Add(-time.Hour)
	db, err := store.Open(ctx, f.database, at)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	limit := money.MustParsePrice("10.10")
	day := clock.DateOf(covered.In(clock.Eastern))
	var o orders.Order
	err = orders.Update(ctx, db, func(tx *sql.Tx) error {
		var err error
		if o, _, err = orders.Create(ctx, tx, orders.New{Strategy: "made-up", Symbol: "AAA", Date: day,
			Kind: signals.KindBuy, Quantity: money.MustParseQuantity("5"), Price: &limit,
			Reason: "test: made-up reason", StrategyHash: strings.Repeat("ab", 32)}, orders.Proposer, at); err != nil {
			return err
		}
		for _, to := range []orders.State{orders.AwaitingApproval, orders.Approved, orders.Submitted, orders.Accepted} {
			if o, err = orders.Move(ctx, tx, o.ID, orders.Change{To: to, Actor: orders.Operator, Reason: "test", At: at}); err != nil {
				return err
			}
		}
		o, _, err = orders.RecordFill(ctx, tx, o.ID, orders.NewFill{Date: day, Quantity: money.MustParseQuantity("2"),
			Price: money.MustParsePrice("10.00"), Reason: "test", Actor: orders.Broker, At: at})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := holdings.Record(ctx, db, holdings.Snapshot{Source: holdings.SourceFills, AsOf: at,
		Holdings: []holdings.Holding{{Symbol: "AAA", Quantity: money.MustParseQuantity("2"),
			AverageCost: money.MustParseMoney("10.00"), Opened: day}},
		Cash: money.MustParseMoney("9980.00")}); err != nil {
		t.Fatal(err)
	}
	return o
}

func runSettle(f files, args ...string) (string, error) {
	var stdout bytes.Buffer
	e := env{
		args:   append([]string{settleCommand}, args...),
		getenv: func(string) string { return "" },
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		clock:  clock.NewFake(covered),
		// A helper that would fail the run if it were ever started.
		helperCommand: []string{"/nonexistent/helper"},
	}
	err := run(context.Background(), e)
	return stdout.String(), err
}

func TestSettleOrderRecordsTheRestOfAPartFill(t *testing.T) {
	for name, order := range map[string]func(ref string, f files) []string{
		"order first": func(ref string, f files) []string {
			return []string{ref, "-filled", "5", "-price", "$10.10", "-settings", f.settings}
		},
		"order last": func(ref string, f files) []string {
			return []string{"-settings", f.settings, "-filled", "5", "-price", "10.10", ref}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := writeExamples(t, nil, nil)
			o := partFilled(t, f)
			out, err := runSettle(f, order(strconv.FormatInt(o.ID, 10), f)...)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"Recorded 3 more shares of order 1 (made-up buy AAA for 2026-11-16) at $10.10, by the operator, on 2026-11-16.",
				"It now has 5 of 5 shares filled (2 before) and is filled.",
				"AAA 5 shares at an average cost of 10.06; cash 9949.70.",
				"Nothing was sent to the broker and no halt was reset.",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("report lacks %q:\n%s", want, out)
				}
			}
			db := openStore(t, f)
			fs, err := orders.Fills(context.Background(), db.Reader(), o.ID)
			if err != nil || len(fs) != 2 {
				t.Errorf("fills = %+v, %v; want 2", fs, err)
			}
		})
	}
}

func TestSettleOrderRefusesAndWritesNothing(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"no order":        {[]string{"-filled", "5", "-price", "10.10"}, "name the order"},
		"no filled count": {[]string{"1", "-price", "10.10"}, "-filled is required"},
		"no price":        {[]string{"1", "-filled", "5"}, "-price is required"},
		"a sub-cent price": {[]string{"1", "-filled", "5", "-price", "10.005"},
			`-price "10.005" must be dollars and whole cents`},
		"a negative price":   {[]string{"1", "-filled", "5", "-price", "-1.00"}, "must be dollars and whole cents"},
		"a bad count":        {[]string{"1", "-filled", "five", "-price", "10.10"}, `-filled "five"`},
		"a stray argument":   {[]string{"-filled", "5", "-price", "10.10", "1", "now"}, `unexpected argument "now"`},
		"an overfill":        {[]string{"1", "-filled", "6", "-price", "10.10"}, "more than the order's 5 shares"},
		"above the limit":    {[]string{"1", "-filled", "5", "-price", "10.20"}, "above the order's limit"},
		"another order":      {[]string{"2", "-filled", "5", "-price", "10.10"}, `no order has the number or key "2"`},
		"nothing new filled": {[]string{"1", "-filled", "2", "-price", "10.10"}, "adds nothing"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := writeExamples(t, nil, nil)
			o := partFilled(t, f)
			out, err := runSettle(f, append(c.args, "-settings", f.settings)...)
			if !errors.Is(err, execution.ErrHandSettle) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want a refusal naming %q", err, c.want)
			}
			if out != "" {
				t.Errorf("a refusal printed a report: %s", out)
			}
			db := openStore(t, f)
			if fs, _ := orders.Fills(context.Background(), db.Reader(), o.ID); len(fs) != 1 {
				t.Errorf("fills = %+v, want only the first", fs)
			}
			if c := rowCounts(t, db); c["holding_snapshots"] != 1 || c["cash_snapshots"] != 1 {
				t.Errorf("rows = %v, want one snapshot of each", c)
			}
		})
	}
}

// TestSettleOrderCallsNoBroker checks the command's source: it has no path
// to the broker helper or an adapter.
func TestSettleOrderCallsNoBroker(t *testing.T) {
	src, err := os.ReadFile("settleorder.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"internal/broker", "helperCommand", "Supervisor", "robinhood"} {
		if strings.Contains(string(src), banned) {
			t.Errorf("settleorder.go names %q; settling by hand calls no broker", banned)
		}
	}
}
