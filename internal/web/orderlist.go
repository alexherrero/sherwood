package web

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/orders"
)

// ordersPath is the Orders view's address.
const ordersPath = "/orders"

// ordersDays is how many trading days Orders lists, newest first.
const ordersDays = 30

// ordersData is what the Orders view shows.
type ordersData struct {
	Days []orderDay
	// Limit is ordersDays; More is true when older days were left out.
	Limit int
	More  bool
}

// orderDay is one trading day's orders.
type orderDay struct {
	Date   string
	Orders []orderView
}

// orderView is one order with its whole history and its fills. Every figure
// is text already formatted from the money types.
type orderView struct {
	Symbol   string
	Strategy string
	Action   string
	Badge    string
	State    string
	StateBox string
	Shares   string
	Price    string
	Reason   string
	Verdict  []verdictLine
	History  []eventView
	Fills    []fillView
	// Filled is the shares filled so far and their value, when any filled.
	Filled string
}

type eventView struct {
	At     string
	Change string
	Reason string
}

type fillView struct {
	Date   string
	At     string
	Shares string
	Price  string
	Value  string
	Reason string
}

// states words each order state and picks its badge.
var states = map[orders.State]struct{ words, badge string }{
	orders.Proposed:         {"Proposed", "badge--hold"},
	orders.AwaitingApproval: {"Waiting for approval", "badge--hold"},
	orders.Approved:         {"Approved", "badge--passed"},
	orders.Declined:         {"Declined", "badge--hold"},
	orders.Blocked:          {"Blocked", "badge--blocked"},
	orders.Submitted:        {"Sent", "badge--hold"},
	orders.Accepted:         {"Accepted by the broker", "badge--passed"},
	orders.PartiallyFilled:  {"Partly filled", "badge--passed"},
	orders.Filled:           {"Filled", "badge--passed"},
	orders.Cancelled:        {"Cancelled", "badge--hold"},
	orders.Rejected:         {"Rejected by the broker", "badge--blocked"},
	orders.Expired:          {"Expired", "badge--hold"},
	orders.Unknown:          {"Unknown: being looked up", "badge--blocked"},
}

func stateOf(st orders.State) (string, string) {
	if w, ok := states[st]; ok {
		return w.words, w.badge
	}
	return string(st), "badge--hold"
}

// actors words who moved an order.
var actors = map[orders.Actor]string{
	orders.Proposer:       "the proposal",
	orders.Risk:           "the risk check",
	orders.Operator:       "you",
	orders.StopSwitch:     "the stop switch",
	orders.Execution:      "the send",
	orders.Broker:         "the broker",
	orders.Reconciliation: "reconciliation",
	orders.Expiry:         "expiry",
}

func actorOf(a orders.Actor) string {
	if w, ok := actors[a]; ok {
		return w
	}
	return string(a)
}

// loadOrders reads the newest trading days' orders, with each order's
// history and fills, in one read transaction.
func loadOrders(s *Server, r *http.Request) (any, error) {
	ctx := r.Context()
	tx, err := s.store.Reader().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	dates, more, err := orderDates(ctx, tx)
	if err != nil {
		return nil, err
	}
	d := ordersData{Limit: ordersDays, More: more}
	for _, date := range dates {
		list, err := orders.ForDate(ctx, tx, date)
		if err != nil {
			return nil, err
		}
		od := orderDay{Date: day(date)}
		for _, o := range list {
			v, err := orderRow(ctx, tx, o)
			if err != nil {
				return nil, err
			}
			od.Orders = append(od.Orders, v)
		}
		d.Days = append(d.Days, od)
	}
	return d, nil
}

// orderDates lists the newest trading days that have orders, and whether
// there are older ones.
func orderDates(ctx context.Context, tx *sql.Tx) ([]time.Time, bool, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT trading_date FROM orders ORDER BY trading_date DESC LIMIT ?`, ordersDays+1)
	if err != nil {
		return nil, false, fmt.Errorf("web: reading order dates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []time.Time
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, false, fmt.Errorf("web: reading order dates: %w", err)
		}
		t, err := time.ParseInLocation(clock.DateLayout, s, clock.Eastern)
		if err != nil {
			return nil, false, fmt.Errorf("web: reading order dates: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("web: reading order dates: %w", err)
	}
	if len(out) > ordersDays {
		return out[:ordersDays], true, nil
	}
	return out, false, nil
}

func orderRow(ctx context.Context, tx *sql.Tx, o orders.Order) (orderView, error) {
	action, badge := actionOf(o.Kind)
	state, box := stateOf(o.State)
	v := orderView{
		Symbol: o.Symbol, Strategy: o.Strategy, Action: action, Badge: badge, State: state, StateBox: box,
		Shares: o.Quantity.String(), Reason: o.Reason, Verdict: storedVerdict(o.Verdict),
	}
	switch {
	case o.Type == orders.DayLimitBuy && o.Price != nil:
		v.Price = "Day limit " + dollars(o.Price.Money())
	case o.Type == orders.Stop && o.Price != nil:
		v.Price = "Stop at " + dollars(o.Price.Money())
	default:
		v.Price = "At the open"
	}
	hist, err := orders.History(ctx, tx, o.ID)
	if err != nil {
		return orderView{}, err
	}
	for _, e := range hist {
		to, _ := stateOf(e.To)
		change := "Made by " + actorOf(e.Actor) + ": " + to
		if e.From != "" {
			from, _ := stateOf(e.From)
			change = from + " to " + to + ", by " + actorOf(e.Actor)
		}
		v.History = append(v.History, eventView{At: when(e.At), Change: change, Reason: e.Reason})
	}
	fills, err := orders.Fills(ctx, tx, o.ID)
	if err != nil {
		return orderView{}, err
	}
	for _, f := range fills {
		v.Fills = append(v.Fills, fillView{
			Date: day(f.Date), At: when(f.At), Shares: f.Quantity.String(),
			Price: dollars(f.Price.Money()), Value: cents(f.Value), Reason: f.Reason,
		})
	}
	if len(fills) > 0 {
		qty, value := fills[0].Quantity, fills[0].Value
		for _, f := range fills[1:] {
			qty, value = qty.Add(f.Quantity), value.Add(f.Value)
		}
		v.Filled = qty.String() + " of " + o.Quantity.String() + " shares, " + cents(value)
	}
	return v, nil
}
