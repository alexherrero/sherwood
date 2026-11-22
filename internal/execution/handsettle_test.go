package execution

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/broker/brokertest"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/signals"
)

// Settling by hand records a fill reconciliation can't. Every symbol, price
// and amount here is made up; the harness starts with 10000.00 cash.

var handTime = checkTime.Add(5 * time.Minute) // 10:50, after a check halted

func (h *harness) settleByHand(ref, filled, price string) (HandSettled, error) {
	h.clock.Set(handTime)
	return SettleByHand(context.Background(), h.store, clock.NYSE(), h.clock.Now(), ref,
		HandFill{Filled: q(filled), Price: money.MustParsePrice(price)})
}

// partBuy is a buy of 5 AAA at a limit of 10.10 that filled 2 at 10.00 when
// it was placed, and is resting at the broker for the rest.
func partBuy(t *testing.T, h *harness) orders.Order {
	t.Helper()
	h.hold(t, nil)
	buy := h.approved(t, signals.KindBuy, "AAA", "5", p("10.10"))
	h.fake.Answer(brokertest.Place, broker.Report{Ref: buy.Key, Status: broker.StatusPartiallyFilled, Filled: q("2"),
		FillPrice: p("10.00")}, nil)
	h.send(t)
	h.wantBook(t, map[string]string{"AAA": "2"}, "9980.00")
	return buy
}

func TestAPartFillThatGrewHaltsUntilSettledByHand(t *testing.T) {
	h := newHarness(t, passAll{})
	buy := partBuy(t, h)

	// The rest fills. The broker no longer lists the order and its lookup
	// gives one average price for all 5 shares, so the check halts.
	h.agree(map[string]string{"AAA": "5"})
	h.fake.Answer(brokertest.Lookup, broker.Report{Ref: buy.Key, Status: broker.StatusFilled, Filled: q("5"),
		FillPrice: p("10.06")}, nil)
	res := h.openCheck(t)
	if !strings.Contains(strings.Join(res.Differences, " "), "without a fill price") ||
		!h.halted(t).Has(halt.Reconciliation) {
		t.Fatalf("differences %q, halts %+v; want the grown fill to halt", res.Differences, h.halted(t).Active)
	}

	// The broker's app shows the other 3 at 10.10: (2 × 10.00 + 3 × 10.10) / 5 = 10.06.
	got, err := h.settleByHand(strconv.FormatInt(buy.ID, 10), "5", "10.10")
	if err != nil {
		t.Fatal(err)
	}
	if got.Order.State != orders.Filled || !got.Before.Equal(q("2")) || !got.Fill.Quantity.Equal(q("3")) ||
		got.Fill.Price.String() != "10.10" || !got.Fill.Date.Equal(session) {
		t.Errorf("settled %+v, want 3 more at 10.10 on the session and the order filled", got)
	}
	if o := h.get(t, buy.ID); o.State != orders.Filled || h.lastActor(t, o.ID) != orders.Operator {
		t.Errorf("the buy is %s by %s, want filled by the operator", o.State, h.lastActor(t, o.ID))
	}
	if fs := h.fills(t, buy.ID); len(fs) != 2 || fs[1].Value.String() != "30.30" ||
		!strings.HasPrefix(fs[1].Reason, "Settled by hand with settle-order") {
		t.Errorf("fills = %+v, want a second of 3 at 10.10", fs)
	}
	h.wantBook(t, map[string]string{"AAA": "5"}, "9949.70")
	if aaa, _ := got.Holdings.Find("AAA"); aaa.AverageCost.String() != "10.06" || got.Cash.Amount.String() != "9949.70" {
		t.Errorf("snapshot %+v, cash %s; want 5 AAA at 10.06 and 9949.70", got.Holdings, got.Cash.Amount)
	}
	if h.fake.Count(brokertest.Place) != 1 || h.fake.Count(brokertest.Lookup) != 1 {
		t.Errorf("calls %+v; settling by hand calls no broker", h.fake.Calls())
	}
	if !h.halted(t).Has(halt.Reconciliation) {
		t.Error("settling by hand reset the halt; that is the operator's to do")
	}

	// The next check finds nothing to settle and no difference.
	h.clock.Set(handTime.Add(10 * time.Minute))
	again, err := h.ex.CheckOpenOrders(context.Background())
	if err != nil || len(again.Differences) != 0 {
		t.Errorf("the next check: %+v, %v; want no difference", again, err)
	}
}

// An order still open at the broker with more shares filled is settled by
// hand to its new count, stays open, and the next check agrees with it.
func TestAPartFillStillOpenIsSettledAndStaysOpen(t *testing.T) {
	h := newHarness(t, passAll{})
	buy := partBuy(t, h)
	h.agree(map[string]string{"AAA": "4"})
	h.fake.Open = []broker.Report{{Ref: buy.Key, Status: broker.StatusPartiallyFilled, Filled: q("4")}}
	if res := h.openCheck(t); !strings.Contains(strings.Join(res.Differences, " "), "the broker reports 4 shares filled") {
		t.Fatalf("differences %q, want the grown fill", res.Differences)
	}
	got, err := h.settleByHand(buy.Key, "4", "10.10") // by key, at the limit itself
	if err != nil {
		t.Fatal(err)
	}
	if got.Order.State != orders.PartiallyFilled || !got.Fill.Quantity.Equal(q("2")) {
		t.Errorf("settled %+v, want 2 more and still partially filled", got)
	}
	h.wantBook(t, map[string]string{"AAA": "4"}, "9959.80")
	h.clock.Set(handTime.Add(10 * time.Minute))
	again, err := h.ex.CheckOpenOrders(context.Background())
	if err != nil || len(again.Differences) != 0 {
		t.Errorf("the next check: %+v, %v; want no difference", again, err)
	}
}

// New shares that filled at two prices are settled once per price, each with
// the running total.
func TestSettlingByHandTwiceForTwoPrices(t *testing.T) {
	h := newHarness(t, passAll{})
	buy := partBuy(t, h)
	ref := strconv.FormatInt(buy.ID, 10)
	if _, err := h.settleByHand(ref, "3", "10.02"); err != nil {
		t.Fatal(err)
	}
	got, err := h.settleByHand(ref, "5", "10.04")
	if err != nil {
		t.Fatal(err)
	}
	if got.Order.State != orders.Filled || !got.Before.Equal(q("3")) || !got.Fill.Quantity.Equal(q("2")) {
		t.Errorf("settled %+v, want 2 more after 3, and filled", got)
	}
	// 10000 − 20.00 − 10.02 − 20.08
	h.wantBook(t, map[string]string{"AAA": "5"}, "9949.90")
}

func TestSettlingByHandRefusesAndWritesNothing(t *testing.T) {
	cases := map[string]struct {
		ref    func(t *testing.T, h *harness) string
		filled string
		price  string
		want   string
	}{
		"no such order": {
			ref: func(*testing.T, *harness) string { return "999" }, filled: "5", price: "10.10",
			want: `no order has the number or key "999"`,
		},
		"a key that is no order's": {
			ref: func(*testing.T, *harness) string { return "made-up-key" }, filled: "5", price: "10.10",
			want: `no order has the number or key "made-up-key"`,
		},
		"a count already recorded": {
			filled: "2", price: "10.10",
			want: "sherwood has already recorded 2 shares filled, so a filled count of 2 adds nothing",
		},
		"a count below what is recorded": {
			filled: "1", price: "10.10", want: "a filled count of 1 adds nothing",
		},
		"an overfill": {
			filled: "6", price: "10.10", want: "a filled count of 6 is more than the order's 5 shares",
		},
		"a zero price": {
			filled: "5", price: "0.00", want: "a fill needs a price above zero",
		},
		"above the limit": {
			filled: "5", price: "10.11", want: "$10.11 is above the order's limit of $10.10",
		},
		"an order that can't take a fill": {
			ref: func(t *testing.T, h *harness) string {
				return strconv.FormatInt(h.order(t, signals.KindBuy, "BBB", "1", p("20.20"), session,
					orders.AwaitingApproval, orders.Declined).ID, 10)
			},
			filled: "1", price: "20.00", want: "it is declined, which can't take a fill",
		},
		"a sell the holdings can't take": {
			ref: func(t *testing.T, h *harness) string {
				return strconv.FormatInt(h.resting(t, "BBB", "3", "18.00").ID, 10)
			},
			filled: "3", price: "17.90", want: "which sherwood's holdings cannot take",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, passAll{})
			buy := partBuy(t, h)
			ref := strconv.FormatInt(buy.ID, 10)
			if c.ref != nil {
				ref = c.ref(t, h)
			}
			before := snapshotCount(t, h)
			_, err := h.settleByHand(ref, c.filled, c.price)
			if !errors.Is(err, ErrHandSettle) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("SettleByHand = %v, want ErrHandSettle naming %q", err, c.want)
			}
			if fs := h.fills(t, buy.ID); len(fs) != 1 {
				t.Errorf("fills = %+v, want only the first", fs)
			}
			if o := h.get(t, buy.ID); o.State != orders.PartiallyFilled {
				t.Errorf("the buy is %s, want still partially_filled", o.State)
			}
			if after := snapshotCount(t, h); after != before {
				t.Errorf("%d holdings snapshots, want still %d", after, before)
			}
			h.wantBook(t, map[string]string{"AAA": "2"}, "9980.00")
		})
	}
}

func snapshotCount(t *testing.T, h *harness) int {
	t.Helper()
	var n int
	if err := h.store.Reader().QueryRow(`SELECT (SELECT COUNT(*) FROM holding_snapshots) + (SELECT COUNT(*) FROM cash_snapshots)`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
