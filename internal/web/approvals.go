package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/feed"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/risk"
	"github.com/alexherrero/sherwood/internal/signals"
)

// Approvals: one card per order waiting for you, and the two actions on it.
// Both actions are limited to the approval devices. Each answers with a
// redirect back to Approvals, whose ?result= value picks one of
// approvalResults to show; the value only picks fixed words.
const (
	approvalsPath = "/approvals"
	approvePath   = "/approvals/approve"
	declinePath   = "/approvals/decline"

	// orderField names the order a card's buttons act on.
	orderField = "order"
)

// The send window. The send-orders job in cmd/sherwood sends each session's
// approved orders at 09:35 ET, once, and an approved order it did not send
// expires at that evening's after-close. So an approval has to land before
// 09:35 on the order's trading day, and one after it is refused.
const (
	sendHour   = 9
	sendMinute = 35
)

// approvalResults are the words Approvals shows after an action.
var approvalResults = map[string]notice{
	"approved": {Class: "notice--info", Title: "Approved.",
		Body: "The risk check passed again. The order is sent at 9:35 am ET on its trading day, after one more check just before it goes."},
	"declined": {Class: "notice--info", Title: "Declined.",
		Body: "The order will not be sent. It stays in Orders with its history."},
	"blocked": {Class: "notice--warning", Title: "Not approved: the risk check refused it.",
		Body: "The check was run again when you approved, and the order would now break a limit, so it is blocked and will not be sent. Orders shows the reasons."},
	"late": {Class: "notice--warning", Title: "Too late to approve.",
		Body: "Orders are sent at 9:35 am ET on their trading day, and that time has passed. The order is declined at the evening's after-close."},
	"expired": {Class: "notice--warning", Title: "That order has expired.",
		Body: "It was not approved by the end of its trading day, so it was declined and cannot be approved now."},
	"gone": {Class: "notice--info", Title: "That order is no longer waiting.",
		Body: "It was already decided, so nothing was changed. Orders shows what happened to it."},
}

// approvalsData is what the Approvals view shows.
type approvalsData struct {
	// Result is the outcome of the last action, if any.
	Result *notice
	Cards  []approvalCard
}

// approvalCard is one order waiting for approval. Every figure is text
// already formatted from the money types.
type approvalCard struct {
	ID       int64
	Symbol   string
	Strategy string
	Action   string
	Badge    string
	Shares   string
	// Price says how it trades: the day limit, or at the open.
	Price string
	// Value is what the order is worth at its limit, or at the last close
	// for a sell; "" when there is no close to value it at.
	Value string
	// Session is the order's trading day, and Deadline when an approval has
	// to land by. Late is true once that time has passed.
	Session  string
	Deadline string
	Late     bool
	// Reason is the rule's reason.
	Reason string
	// Explanation is the research feed's explanation of the order's signal,
	// matched by its key, or nil when there is none from a fresh upload;
	// then the card shows the rule's reason alone.
	Explanation *feedItemView
	// Stored is the verdict stored when the order was proposed, and Now the
	// risk check run again for this page.
	Stored    []verdictLine
	Now       []verdictLine
	NowPassed bool
}

// verdictLine is one check of a verdict.
type verdictLine struct {
	Passed bool
	Text   string
}

// actions words each kind of order and picks its badge.
var actions = map[signals.Kind]struct{ words, badge string }{
	signals.KindBuy:      {"Buy", "badge--add"},
	signals.KindAdd:      {"Add to the holding", "badge--add"},
	signals.KindTrim:     {"Trim", "badge--trim"},
	signals.KindSell:     {"Sell", "badge--trim"},
	signals.KindStopSell: {"Protective stop", "badge--hold"},
}

func actionOf(k signals.Kind) (string, string) {
	if a, ok := actions[k]; ok {
		return a.words, a.badge
	}
	return string(k), "badge--hold"
}

// mountApprovals registers Approvals' two actions. The view itself is in the
// views table.
func (s *Server) mountApprovals() {
	s.HandleChange(approvePath, http.HandlerFunc(s.approve), ApprovalDevicesOnly)
	s.HandleChange(declinePath, http.HandlerFunc(s.decline), ApprovalDevicesOnly)
}

// loadApprovals reads every order waiting for approval and runs the risk
// check on each again, in one read transaction.
func loadApprovals(s *Server, r *http.Request) (any, error) {
	ctx := r.Context()
	tx, err := s.store.Reader().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	d := approvalsData{}
	if res, ok := approvalResults[r.URL.Query().Get("result")]; ok {
		d.Result = &res
	}
	waiting, err := orders.InState(ctx, tx, orders.AwaitingApproval)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	for _, o := range waiting {
		c, err := s.card(ctx, tx, o, now)
		if err != nil {
			return nil, err
		}
		d.Cards = append(d.Cards, c)
	}
	return d, nil
}

func (s *Server) card(ctx context.Context, tx *sql.Tx, o orders.Order, now time.Time) (approvalCard, error) {
	deadline := sendDeadline(o.Date)
	snap, err := s.snapshot(ctx, tx, o, now)
	if err != nil {
		return approvalCard{}, err
	}
	v := s.checker.Check(riskOrder(o), snap)
	explained, err := feed.Explanation(ctx, tx, o.Key)
	if err != nil {
		return approvalCard{}, err
	}
	if explained != nil && explained.Symbol != o.Symbol {
		explained = nil
	}
	words, badge := actionOf(o.Kind)
	c := approvalCard{
		ID: o.ID, Symbol: o.Symbol, Strategy: o.Strategy, Action: words, Badge: badge,
		Shares:      o.Quantity.String(),
		Session:     day(o.Date),
		Deadline:    when(deadline),
		Late:        !now.Before(deadline),
		Reason:      o.Reason,
		Explanation: freshItem(explained, now),
		Stored:      storedVerdict(o.Verdict),
		NowPassed:   v.Passed(),
	}
	switch {
	case o.Type == orders.DayLimitBuy && o.Price != nil:
		c.Price = "Day limit " + dollars(o.Price.Money())
		c.Value = cents(o.Price.Mul(o.Quantity)) + " at the limit"
	case o.Type == orders.Stop && o.Price != nil:
		c.Price = "Stop at " + dollars(o.Price.Money())
		c.Value = cents(o.Price.Mul(o.Quantity)) + " at the stop"
	default:
		c.Price = "At the open"
		if last, ok := snap.LastCloses[o.Symbol]; ok {
			c.Value = "About " + cents(last.Mul(o.Quantity)) + " at the last close of " + dollars(last.Money())
		}
	}
	for _, r := range v.Results {
		c.Now = append(c.Now, verdictLine{Passed: r.Passed, Text: r.Check + ". " + r.Detail})
	}
	if len(c.Now) == 0 {
		c.Now = []verdictLine{{Text: "The risk check ran no checks."}}
	}
	return c, nil
}

// storedVerdict splits a stored verdict, one check per line, each starting
// "Passed: " or "Refused: ".
func storedVerdict(text string) []verdictLine {
	var out []verdictLine
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "Passed: "); ok {
			out = append(out, verdictLine{Passed: true, Text: rest})
			continue
		}
		out = append(out, verdictLine{Text: strings.TrimPrefix(line, "Refused: ")})
	}
	return out
}

// sendDeadline is 09:35 ET on the order's trading day.
func sendDeadline(date time.Time) time.Time {
	d := clock.DateOf(date)
	return time.Date(d.Year(), d.Month(), d.Day(), sendHour, sendMinute, 0, 0, clock.Eastern)
}

func backToApprovals(w http.ResponseWriter, r *http.Request, result string) {
	http.Redirect(w, r, approvalsPath+"?result="+result, http.StatusSeeOther)
}

// errStop ends an action's transaction without a change; the outcome says
// why.
var errStop = errors.New("web: nothing to change")

// approve re-runs the risk check on a waiting order and approves it, or
// blocks it if the check now refuses. It refuses an order that is no longer
// waiting, and one whose send time has passed.
func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	device, _ := callerAddr(r) // the device check already passed, so this is set
	s.act(w, r, func(ctx context.Context, tx *sql.Tx, o orders.Order, now time.Time) (string, *orders.Change, error) {
		if !now.Before(sendDeadline(o.Date)) {
			return "late", nil, nil
		}
		snap, err := s.snapshot(ctx, tx, o, now)
		if err != nil {
			return "", nil, err
		}
		v := s.checker.Check(riskOrder(o), snap)
		if !v.Passed() {
			why := v.Reason()
			if why == "" {
				why = "The risk check did not pass the order."
			}
			return "blocked", &orders.Change{To: orders.Blocked, Actor: orders.Risk,
				Reason:  "Refused when you approved it on the approval device " + device.String() + ": " + why,
				Verdict: v.String()}, nil
		}
		return "approved", &orders.Change{To: orders.Approved, Actor: orders.Operator,
			Reason:  "You approved it on the approval device " + device.String() + ". The risk check passed again.",
			Verdict: v.String()}, nil
	})
}

// decline declines a waiting order.
func (s *Server) decline(w http.ResponseWriter, r *http.Request) {
	device, _ := callerAddr(r)
	s.act(w, r, func(context.Context, *sql.Tx, orders.Order, time.Time) (string, *orders.Change, error) {
		return "declined", &orders.Change{To: orders.Declined, Actor: orders.Operator,
			Reason: "You declined it on the approval device " + device.String() + "."}, nil
	})
}

// decide works out what an action does to a waiting order: the outcome to
// show, and the change to make, if any.
type decide func(ctx context.Context, tx *sql.Tx, o orders.Order, now time.Time) (string, *orders.Change, error)

// act runs one approval action in a single write transaction: it reads the
// order, checks it is still waiting, and makes the change decide asks for.
// An illegal move throws the stop switch, after the transaction has ended.
func (s *Server) act(w http.ResponseWriter, r *http.Request, fn decide) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PostForm.Get(orderField), 10, 64)
	if err != nil || id <= 0 {
		backToApprovals(w, r, "gone")
		return
	}
	var outcome string
	err = orders.Update(ctx, s.store, func(tx *sql.Tx) error {
		o, err := orders.Get(ctx, tx, id)
		if errors.Is(err, orders.ErrNotFound) {
			outcome = "gone"
			return errStop
		}
		if err != nil {
			return err
		}
		if o.State != orders.AwaitingApproval {
			if outcome, err = notWaiting(ctx, tx, o); err != nil {
				return err
			}
			return errStop
		}
		now := s.clock.Now()
		result, change, err := fn(ctx, tx, o, now)
		if err != nil {
			return err
		}
		outcome = result
		if change == nil {
			return errStop
		}
		change.At = now
		if change.At.Before(o.Updated) {
			change.At = o.Updated // a clock stepped back never refuses the move
		}
		_, err = orders.Move(ctx, tx, o.ID, *change)
		return err
	})
	if errors.Is(err, errStop) {
		err = nil
	}
	if err = s.stop.OrderError(ctx, err); err != nil {
		refuse(w, r, "the handler failed", http.StatusInternalServerError,
			"Sherwood could not record your choice, so nothing was changed. Try again.")
		return
	}
	backToApprovals(w, r, outcome)
}

// notWaiting picks the outcome for an order that is no longer waiting:
// "expired" if expiry declined it, "gone" otherwise.
func notWaiting(ctx context.Context, tx *sql.Tx, o orders.Order) (string, error) {
	if o.State != orders.Declined {
		return "gone", nil
	}
	hist, err := orders.History(ctx, tx, o.ID)
	if err != nil {
		return "", err
	}
	if n := len(hist); n > 0 && hist[n-1].Actor == orders.Expiry {
		return "expired", nil
	}
	return "gone", nil
}

// riskOrder is o as the risk check sees it.
func riskOrder(o orders.Order) risk.Order {
	ro := risk.Order{Strategy: o.Strategy, Symbol: o.Symbol, Kind: riskKind(o.Type), Quantity: o.Quantity,
		Session: o.Date}
	if o.Price != nil {
		ro.Price = *o.Price
	}
	return ro
}

func riskKind(t orders.Type) risk.Kind {
	switch t {
	case orders.DayLimitBuy:
		return risk.Buy
	case orders.Stop:
		return risk.ProtectiveSell
	default:
		return risk.Sell
	}
}

// snapshot is the risk check's preview for o's session, as things stand now:
// the current holdings and cash, valued at the latest checked closes before
// the session, and the session's other orders. A holding with no close, or
// no cash reading, leaves the value at zero, which refuses buys and ordinary
// sells: it fails closed.
func (s *Server) snapshot(ctx context.Context, tx *sql.Tx, o orders.Order, now time.Time) (risk.Snapshot, error) {
	prev, err := s.calendar.PreviousTradingDay(o.Date)
	if err != nil {
		return risk.Snapshot{}, fmt.Errorf("web: %w", err)
	}
	held := map[string]money.Quantity{}
	set, err := holdings.Current(ctx, tx)
	if err != nil && !errors.Is(err, holdings.ErrNone) {
		return risk.Snapshot{}, err
	}
	for _, h := range set.Holdings {
		held[h.Symbol] = h.Quantity
	}
	closes, err := prices.LatestCloses(ctx, tx, prev)
	if err != nil {
		return risk.Snapshot{}, err
	}
	var cash, value money.Money
	switch c, err := holdings.LatestCash(ctx, tx); {
	case errors.Is(err, holdings.ErrNone):
	case err != nil:
		return risk.Snapshot{}, err
	default:
		cash, value = c.Amount, c.Amount
		for _, h := range set.Holdings {
			last, ok := closes[h.Symbol]
			if !ok {
				value = money.Money{}
				break
			}
			value = value.Add(h.Value(last))
		}
	}
	session, err := orders.ForDate(ctx, tx, o.Date)
	if err != nil {
		return risk.Snapshot{}, err
	}
	day, err := dayOrders(ctx, tx, session, o.ID, closes)
	if err != nil {
		return risk.Snapshot{}, err
	}
	sw, err := halt.Active(ctx, tx)
	if err != nil {
		return risk.Snapshot{}, err
	}
	// The limits in force: the file's, tightened from the phone app.
	lim, err := limits.Effective(ctx, tx, s.strategy)
	if err != nil {
		return risk.Snapshot{}, err
	}
	// The watchlist in force: the file's, plus the phone's additions.
	watched, err := limits.Watchlist(ctx, tx, s.strategy)
	if err != nil {
		return risk.Snapshot{}, err
	}
	return risk.Snapshot{
		Now:                now,
		Calendar:           s.calendar,
		Preview:            true,
		Limits:             lim,
		Sectors:            s.strategy.Sectors,
		Watchlist:          watched,
		Held:               held,
		Cash:               cash,
		Value:              value,
		PreviousCloseValue: value,
		LastCloses:         closes,
		DayOrders:          day,
		SwitchOff:          !sw.On(),
		BrokerHealthy:      s.brokerHealthy(ctx),
	}, nil
}

// dayOrders is the session's other orders that count against its limits,
// by the execution layer's rule: approved, sent, filled or unknown orders,
// and cancelled or expired ones that filled something. A protective stop
// counts only once it has filled. Orders still waiting for approval do not
// count, so you choose among them; the check before sending sees every
// order approved by then.
func dayOrders(ctx context.Context, tx *sql.Tx, list []orders.Order, except int64,
	closes map[string]money.Price) ([]risk.DayOrder, error) {
	var out []risk.DayOrder
	for _, o := range list {
		if o.ID == except {
			continue
		}
		switch o.State {
		case orders.Approved, orders.Submitted, orders.Accepted, orders.PartiallyFilled, orders.Filled,
			orders.Unknown, orders.Cancelled, orders.Expired:
		default:
			continue
		}
		if o.Protective() || o.State == orders.Cancelled || o.State == orders.Expired {
			fills, err := orders.Fills(ctx, tx, o.ID)
			if err != nil {
				return nil, err
			}
			if len(fills) == 0 {
				continue
			}
		}
		d := risk.DayOrder{Symbol: o.Symbol, Kind: riskKind(o.Type)}
		switch {
		case o.Price != nil:
			d.Value = o.Price.Mul(o.Quantity)
		default:
			if c, ok := closes[o.Symbol]; ok {
				d.Value = c.Mul(o.Quantity)
			}
		}
		if d.Kind == risk.Buy {
			switch o.State {
			case orders.Approved, orders.Submitted, orders.Accepted, orders.PartiallyFilled, orders.Unknown:
				d.Open = true
			}
		}
		out = append(out, d)
	}
	return out, nil
}
