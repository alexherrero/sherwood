// Package paper is the paper broker: the broker adapter for paper mode. It
// never touches a network or a real account.
//
// Its book is sherwood's own record. An order the execution layer sends is
// accepted and rests as an `accepted` row in the orders table; the paper
// broker holds nothing in memory, so a restart loses nothing. Holdings and
// cash are the newest snapshots in the holdings tables.
//
// Orders fill once a day, after the close, when Settle works the open orders
// against that date's checked bars with the fill model in package fill:
//
//   - Sells at the open go first: a trim or rule exit sent for the date fills
//     at the date's open.
//   - Then resting stops: a stop placed for the date or earlier fills at the
//     open if the stock opened at or below it (a gap down), else at the stop
//     if the day's low reached it, else it keeps resting.
//   - Then day limit buys sent for the date: at the open if the open was at
//     or below the limit, else at the limit if the low went below it (a low
//     that only touches the limit is not a fill), else not at all.
//
// A day order (a buy or a sell at the open) that has not filled by then
// expires with its session; a resting stop stays until it fills or is
// called off.
//
// Each fill is written to the fills table and moves its order, and the
// holdings and cash after the day's fills are written as one new snapshot
// with source `fills`, stamped at the session's close. All of a date's
// settlement is one transaction, and an order that has filled is no longer
// open, so settling a date twice changes nothing the second time.
//
// Paper fills are approximate. Prices are daily, so the paper broker cannot
// see the order of moves within a day; a stop raised at 09:35 is worked
// against that day's open as though it had rested since the open.
package paper

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/alexherrero/sherwood/internal/broker"
	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/fill"
	"github.com/alexherrero/sherwood/internal/holdings"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/orders"
	"github.com/alexherrero/sherwood/internal/prices"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// Config is what the paper broker needs.
type Config struct {
	Store    *store.Store
	Clock    clock.Clock
	Calendar *clock.Calendar
	// Policy is the strategy file's share policy, whole or fractional.
	Policy money.QuantityPolicy
}

// Broker is the paper broker.
type Broker struct {
	store    *store.Store
	clock    clock.Clock
	calendar *clock.Calendar
	policy   money.QuantityPolicy
}

var _ broker.Adapter = (*Broker)(nil)

// ErrSettle wraps every reason Settle refuses to settle a date. Nothing is
// written when it refuses.
var ErrSettle = errors.New("paper: cannot settle")

// New builds the paper broker, refusing a missing part.
func New(cfg Config) (*Broker, error) {
	switch {
	case cfg.Store == nil:
		return nil, errors.New("paper: no store")
	case cfg.Clock == nil:
		return nil, errors.New("paper: no clock")
	case cfg.Calendar == nil:
		return nil, errors.New("paper: no calendar")
	}
	if _, err := cfg.Policy.Places(); err != nil {
		return nil, fmt.Errorf("paper: %w", err)
	}
	return &Broker{store: cfg.Store, clock: cfg.Clock, calendar: cfg.Calendar, policy: cfg.Policy}, nil
}

// Place accepts a well-formed order for a trading date, which then rests in
// the orders table until it fills or is called off. A malformed order, a
// share count the policy refuses or a date the market is shut is rejected.
func (b *Broker) Place(_ context.Context, o broker.Order) (broker.Report, error) {
	r := broker.Report{Ref: o.Ref, Status: broker.StatusAccepted}
	if why := b.refusal(o); why != "" {
		r.Status, r.Detail = broker.StatusRejected, why
	}
	return r, nil
}

// refusal is why the paper broker refuses an order, or "" if it takes it. A
// refusal is the broker's answer, not a failed call.
func (b *Broker) refusal(o broker.Order) string {
	if err := o.Check(); err != nil {
		return err.Error()
	}
	if err := b.policy.Check(o.Quantity); err != nil {
		return err.Error()
	}
	if open, err := b.calendar.IsTradingDay(o.Session); err != nil || !open {
		return "the market is not open on " + clock.DateOf(o.Session).Format(clock.DateLayout)
	}
	return ""
}

// Cancel calls off an open order. An order that has already filled, been
// called off or expired reports that status instead.
func (b *Broker) Cancel(ctx context.Context, ref string) (broker.Report, error) {
	r, err := b.Lookup(ctx, ref)
	if err != nil {
		return broker.Report{}, err
	}
	if r.Status == broker.StatusAccepted || r.Status == broker.StatusPartiallyFilled {
		r.Status = broker.StatusCancelled
	}
	return r, nil
}

// Lookup reports an order's status from the orders table. An order that was
// sent but whose answer was never recorded (submitted or unknown) was
// accepted: the paper broker accepts every well-formed order.
func (b *Broker) Lookup(ctx context.Context, ref string) (broker.Report, error) {
	o, err := orders.ByKey(ctx, b.store.Reader(), ref)
	if err != nil {
		return broker.Report{}, fmt.Errorf("paper: looking up an order: %w", err)
	}
	return b.report(ctx, b.store.Reader(), o)
}

func (b *Broker) report(ctx context.Context, q orders.Querier, o orders.Order) (broker.Report, error) {
	r := broker.Report{Ref: o.Key}
	switch o.State {
	case orders.Submitted, orders.Accepted, orders.Unknown:
		r.Status = broker.StatusAccepted
	case orders.PartiallyFilled:
		r.Status = broker.StatusPartiallyFilled
	case orders.Filled:
		r.Status = broker.StatusFilled
	case orders.Cancelled:
		r.Status = broker.StatusCancelled
	case orders.Expired:
		r.Status = broker.StatusExpired
	case orders.Rejected:
		r.Status = broker.StatusRejected
	default:
		return broker.Report{}, fmt.Errorf("paper: order %d was never sent (it is %s)", o.ID, o.State)
	}
	filled, err := filledSoFar(ctx, q, o.ID)
	if err != nil {
		return broker.Report{}, err
	}
	r.Filled = filled
	return r, nil
}

// openStates are the states of an order resting at the paper broker.
var openStates = []orders.State{orders.Submitted, orders.Accepted, orders.PartiallyFilled, orders.Unknown}

// OpenOrders lists the orders resting at the paper broker.
func (b *Broker) OpenOrders(ctx context.Context) ([]broker.Report, error) {
	q := b.store.Reader()
	os, err := orders.InState(ctx, q, openStates...)
	if err != nil {
		return nil, fmt.Errorf("paper: listing open orders: %w", err)
	}
	out := make([]broker.Report, 0, len(os))
	for _, o := range os {
		r, err := b.report(ctx, q, o)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// Holdings lists the newest holdings snapshot, or nothing if none has been
// recorded.
func (b *Broker) Holdings(ctx context.Context) ([]broker.Position, error) {
	set, err := holdings.Current(ctx, b.store.Reader())
	switch {
	case errors.Is(err, holdings.ErrNone):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("paper: reading holdings: %w", err)
	}
	out := make([]broker.Position, len(set.Holdings))
	for i, h := range set.Holdings {
		out[i] = broker.Position{Symbol: h.Symbol, Quantity: h.Quantity, AverageCost: h.AverageCost}
	}
	return out, nil
}

var _ broker.CashReader = (*Broker)(nil)

// Cash reads the newest cash reading, or zero if none has been recorded.
func (b *Broker) Cash(ctx context.Context) (money.Money, error) {
	cash, err := holdings.LatestCash(ctx, b.store.Reader())
	switch {
	case errors.Is(err, holdings.ErrNone):
		return money.Money{}, nil
	case err != nil:
		return money.Money{}, fmt.Errorf("paper: reading cash: %w", err)
	}
	return cash.Amount, nil
}

// Account reads the newest cash reading (zero if none) and values the
// holdings at their latest checked closes. A holding with no checked close is
// an error, because the value would be wrong.
func (b *Broker) Account(ctx context.Context) (broker.Account, error) {
	q := b.store.Reader()
	var a broker.Account
	cash, err := holdings.LatestCash(ctx, q)
	switch {
	case errors.Is(err, holdings.ErrNone):
	case err != nil:
		return a, fmt.Errorf("paper: reading cash: %w", err)
	default:
		a.Cash = cash.Amount
	}
	set, err := holdings.Current(ctx, q)
	if err != nil && !errors.Is(err, holdings.ErrNone) {
		return a, fmt.Errorf("paper: reading holdings: %w", err)
	}
	closes, err := prices.LatestCloses(ctx, q, b.clock.Now().In(clock.Eastern))
	if err != nil {
		return a, fmt.Errorf("paper: %w", err)
	}
	a.Value = a.Cash
	for _, h := range set.Holdings {
		c, ok := closes[h.Symbol]
		if !ok {
			return broker.Account{}, fmt.Errorf("paper: no checked close for %s, so the account cannot be valued", h.Symbol)
		}
		a.Value = a.Value.Add(h.Value(c))
	}
	return a, nil
}

func filledSoFar(ctx context.Context, q orders.Querier, id int64) (money.Quantity, error) {
	fs, err := orders.Fills(ctx, q, id)
	if err != nil {
		return money.Quantity{}, fmt.Errorf("paper: reading fills: %w", err)
	}
	var total money.Quantity
	for _, f := range fs {
		total = total.Add(f.Quantity)
	}
	return total, nil
}

// Filled is one fill Settle made.
type Filled struct {
	Order orders.Order // as it stands after the fill
	Fill  orders.Fill
}

// Worked is an order Settle worked against the date's bar without a fill, or
// left alone, with the reason.
type Worked struct {
	Order  orders.Order
	Reason string
}

// Settlement is what Settle did for one date.
type Settlement struct {
	Date time.Time
	// Fills are the fills made, in the order they were worked.
	Fills []Filled
	// Unfilled are open orders worked against the date's bar that did not
	// fill.
	Unfilled []Worked
	// Skipped are open orders made for the date or earlier that could not be
	// worked: no checked bar for the stock, or a day order for an earlier
	// date that was never settled.
	Skipped []Worked
	// Expired are the day orders for the date or earlier that did not fill:
	// a day order is good only until its session's close. Resting stops
	// never expire.
	Expired []orders.Order
	// Holdings and Cash are the snapshot written after the fills, or nil
	// when nothing filled.
	Holdings *holdings.Set
	Cash     *holdings.Cash
}

// workOrder is the order Settle works types in: sells at the open, then
// resting stops, then buys.
func workOrder(t orders.Type) int {
	switch t {
	case orders.SellAtOpen:
		return 0
	case orders.Stop:
		return 1
	default:
		return 2
	}
}

// Settle works every open order against one trading date's checked bars,
// records the fills, and writes the holdings and cash after them. bars are
// the date's usable bars (prices.UsableBars); a stock without one is skipped.
//
// It refuses (ErrSettle) a date that is not a trading day, a sell of more
// shares than are held, and holdings or cash already recorded after the
// date's close, and writes nothing then.
func (b *Broker) Settle(ctx context.Context, date time.Time, bars []prices.Bar) (*Settlement, error) {
	date = clock.DateOf(date)
	session, err := b.calendar.Session(date)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrSettle, date.Format(clock.DateLayout), err)
	}
	day := make(map[string]prices.Bar, len(bars))
	for _, bar := range bars {
		if !clock.DateOf(bar.Date).Equal(date) {
			return nil, fmt.Errorf("%w: a bar for %s is from %s, not %s", ErrSettle, bar.Symbol,
				bar.DateString(), date.Format(clock.DateLayout))
		}
		day[bar.Symbol] = bar
	}
	res := &Settlement{Date: date}
	now := b.clock.Now()
	err = orders.Update(ctx, b.store, func(tx *sql.Tx) error {
		*res = Settlement{Date: date}
		open, err := orders.InState(ctx, tx, orders.Accepted, orders.PartiallyFilled)
		if err != nil {
			return err
		}
		slices.SortStableFunc(open, func(x, y orders.Order) int {
			return cmp.Or(cmp.Compare(workOrder(x.Type), workOrder(y.Type)), cmp.Compare(x.ID, y.ID))
		})
		book, err := holdings.ReadBook(ctx, tx)
		if err != nil {
			return err
		}
		for _, o := range open {
			if err := b.work(ctx, tx, o, date, day, book, res, now); err != nil {
				return err
			}
		}
		if err := expire(ctx, tx, open, date, res, now); err != nil {
			return err
		}
		if len(res.Fills) == 0 {
			return nil
		}
		return write(ctx, tx, book, session.Close, res)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// expire moves each day order for the date or earlier that is still open
// after the date's fills to expired.
func expire(ctx context.Context, tx *sql.Tx, open []orders.Order, date time.Time, res *Settlement, now time.Time) error {
	filled := map[int64]bool{}
	for _, f := range res.Fills {
		filled[f.Order.ID] = true
	}
	for _, o := range open {
		if o.Type == orders.Stop || filled[o.ID] || clock.DateOf(o.Date).After(date) {
			continue
		}
		at := now
		if at.Before(o.Updated) {
			at = o.Updated
		}
		o, err := orders.Move(ctx, tx, o.ID, orders.Change{To: orders.Expired, Actor: orders.Broker, At: at,
			Reason: "A day order that did not fill by the close of " + clock.DateOf(o.Date).Format(clock.DateLayout) + "."})
		if err != nil {
			return err
		}
		res.Expired = append(res.Expired, o)
	}
	return nil
}

// work settles one open order against the date, if it is due.
func (b *Broker) work(ctx context.Context, tx *sql.Tx, o orders.Order, date time.Time,
	day map[string]prices.Bar, book *holdings.Book, res *Settlement, now time.Time) error {
	made := clock.DateOf(o.Date)
	switch {
	case made.After(date):
		return nil // made for a later session
	case o.Type != orders.Stop && made.Before(date):
		res.Skipped = append(res.Skipped, Worked{o, fmt.Sprintf(
			"a day order for %s, which was never settled; it expires unfilled",
			made.Format(clock.DateLayout))})
		return nil
	}
	bar, ok := day[o.Symbol]
	if !ok {
		res.Skipped = append(res.Skipped, Worked{o, "no checked price for " + o.Symbol + " on " +
			date.Format(clock.DateLayout)})
		return nil
	}
	done, err := filledSoFar(ctx, tx, o.ID)
	if err != nil {
		return err
	}
	left, err := o.Quantity.Sub(done)
	if err != nil || left.IsZero() {
		return fmt.Errorf("%w: order %d is open with nothing left to fill", ErrSettle, o.ID)
	}
	fo := fill.Order{Quantity: left}
	if o.Price != nil {
		fo.Price = *o.Price
	}
	switch o.Type {
	case orders.DayLimitBuy:
		fo.Kind = fill.BuyLimit
	case orders.Stop:
		fo.Kind = fill.SellStop
	default:
		fo.Kind = fill.SellAtOpen
	}
	f, filled, err := fill.Apply(fo, fill.Day{Open: bar.Open, High: bar.High, Low: bar.Low}, b.policy)
	if err != nil {
		return fmt.Errorf("%w: order %d: %w", ErrSettle, o.ID, err)
	}
	if !filled {
		res.Unfilled = append(res.Unfilled, Worked{o, f.Reason})
		return nil
	}
	if o.Side == signals.SideBuy {
		book.Buy(o.Symbol, f.Quantity, f.Price, date)
	} else if err := book.Sell(o.Symbol, f.Quantity, f.Price); err != nil {
		return fmt.Errorf("%w: order %d: %w", ErrSettle, o.ID, err)
	}
	after, rec, err := orders.RecordFill(ctx, tx, o.ID, orders.NewFill{
		Date: date, Quantity: f.Quantity, Price: f.Price, Reason: f.Reason, Actor: orders.Broker, At: now,
	})
	if err != nil {
		return err
	}
	res.Fills = append(res.Fills, Filled{Order: after, Fill: rec})
	return nil
}

// write records the book as the holdings and cash at the session's close.
func write(ctx context.Context, tx *sql.Tx, bk *holdings.Book, at time.Time, res *Settlement) error {
	set, cash, err := bk.Write(ctx, tx, holdings.SourceFills, at)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSettle, err)
	}
	res.Holdings, res.Cash = &set, &cash
	return nil
}
