package orders

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/signals"
	"github.com/alexherrero/sherwood/internal/store"
)

// Type is how an order is sent.
type Type string

// The order types.
const (
	// DayLimitBuy is a day buy order with a limit price.
	DayLimitBuy Type = "day_limit_buy"
	// Stop is a protective stop resting at the broker.
	Stop Type = "stop"
	// SellAtOpen is a sell made at the next open.
	SellAtOpen Type = "sell_at_open"
)

// TypeFor is the order type a signal kind is sent as: buys and adds are day
// limit buys, protective sells are stops, trims and ordinary exits sell at
// the open. The schema holds the same rule.
func TypeFor(k signals.Kind) Type {
	switch k {
	case signals.KindBuy, signals.KindAdd:
		return DayLimitBuy
	case signals.KindStopSell:
		return Stop
	default:
		return SellAtOpen
	}
}

// Actor is who or what changed an order.
type Actor string

// The actors.
const (
	// Proposer: the step that turns signals into orders.
	Proposer Actor = "proposer"
	// Risk: the risk check.
	Risk Actor = "risk"
	// Operator: you, from an approval device.
	Operator Actor = "operator"
	// StopSwitch: the stop switch.
	StopSwitch Actor = "stop_switch"
	// Execution: the execution layer sending orders.
	Execution Actor = "execution"
	// Broker: the broker adapter's answer or a fill it reported.
	Broker Actor = "broker"
	// Reconciliation: reconciliation settling an order by looking it up.
	Reconciliation Actor = "reconciliation"
	// Expiry: the end of a trading day.
	Expiry Actor = "expiry"
)

var actorText = regexp.MustCompile(`^[a-z][a-z_]{0,31}$`)

// Errors.
var (
	// ErrInvalid wraps a refused argument: a malformed order, change or fill.
	// Nothing is written.
	ErrInvalid = errors.New("orders: invalid")
	// ErrNotFound means no order has that id or key.
	ErrNotFound = errors.New("orders: no such order")
	// ErrOverfill means a fill would take an order past its quantity.
	ErrOverfill = errors.New("orders: fill exceeds the order's quantity")
	// ErrCorrupt means a stored order's history does not replay to its state.
	ErrCorrupt = errors.New("orders: stored history is inconsistent")
)

// Order is an order record.
type Order struct {
	ID int64
	// Key is signals.Key(Strategy, Symbol, Side, Kind, Date).
	Key      string
	Strategy string
	Symbol   string
	// Date is the trading date the order was made for, midnight Eastern.
	Date     time.Time
	Side     signals.Side
	Kind     signals.Kind
	Type     Type
	Quantity money.Quantity
	// Price is the limit of a DayLimitBuy or the stop of a Stop; nil for a
	// SellAtOpen.
	Price *money.Price
	State State
	// Reason is the rule's reason from the signal.
	Reason string
	// Verdict is the newest risk verdict, or "" before the first check.
	Verdict      string
	StrategyHash string
	Created      time.Time
	Updated      time.Time
}

// Protective reports whether the order is a protective sell.
func (o Order) Protective() bool { return o.Kind == signals.KindStopSell }

// New is what Create needs to make an order from a signal.
type New struct {
	Strategy string
	Symbol   string
	// Date is the trading date; only its Eastern calendar date is kept.
	Date     time.Time
	Kind     signals.Kind
	Quantity money.Quantity
	// Price is required for a buy or add (the limit) and a stop_sell (the
	// stop), and must be nil for any other sell.
	Price        *money.Price
	Reason       string
	StrategyHash string
}

// Key is the order's idempotency key.
func (n New) Key() string {
	return signals.Key(n.Strategy, n.Symbol, n.Kind.Side(), n.Kind, n.Date)
}

var (
	symbolText = regexp.MustCompile(`^[A-Z][A-Z0-9]*([.-][A-Z0-9]+)?$`)
	hashText   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func (n New) check() error {
	switch {
	case n.Strategy == "":
		return fmt.Errorf("%w: no strategy", ErrInvalid)
	case len(n.Symbol) > 10 || !symbolText.MatchString(n.Symbol):
		return fmt.Errorf("%w: %q is not a stock symbol", ErrInvalid, n.Symbol)
	case n.Date.IsZero():
		return fmt.Errorf("%w: no trading date", ErrInvalid)
	case !validKind(n.Kind):
		return fmt.Errorf("%w: unknown signal kind %q", ErrInvalid, n.Kind)
	case n.Quantity.IsZero():
		return fmt.Errorf("%w: no shares", ErrInvalid)
	case n.Reason == "":
		return fmt.Errorf("%w: no reason", ErrInvalid)
	case !hashText.MatchString(n.StrategyHash):
		return fmt.Errorf("%w: strategy hash %q is not hex SHA-256", ErrInvalid, n.StrategyHash)
	}
	needsPrice := TypeFor(n.Kind) != SellAtOpen
	if needsPrice && n.Price == nil {
		return fmt.Errorf("%w: a %s order needs a price", ErrInvalid, TypeFor(n.Kind))
	}
	if !needsPrice && n.Price != nil {
		return fmt.Errorf("%w: a %s order takes no price", ErrInvalid, SellAtOpen)
	}
	if n.Price != nil && n.Price.IsZero() {
		return fmt.Errorf("%w: a price of zero", ErrInvalid)
	}
	return nil
}

func validKind(k signals.Kind) bool {
	switch k {
	case signals.KindBuy, signals.KindAdd, signals.KindTrim, signals.KindSell, signals.KindStopSell:
		return true
	}
	return false
}

// Change is one move of an order's state.
type Change struct {
	To    State
	Actor Actor
	// Reason says why, in plain words. Required.
	Reason string
	// Verdict is the risk check's verdict that came with this change, or "".
	// When set it also becomes the order's Verdict.
	Verdict string
	// At is when the change happened, from the injected clock.
	At time.Time
}

func (c Change) check() error {
	switch {
	case !c.To.Valid():
		return fmt.Errorf("%w: unknown state %q", ErrInvalid, c.To)
	case !actorText.MatchString(string(c.Actor)):
		return fmt.Errorf("%w: actor %q", ErrInvalid, c.Actor)
	case c.Reason == "":
		return fmt.Errorf("%w: a change needs a reason", ErrInvalid)
	case c.At.IsZero():
		return fmt.Errorf("%w: a change needs a time", ErrInvalid)
	}
	return nil
}

// Event is one row of an order's history.
type Event struct {
	ID      int64
	OrderID int64
	// From is "" for the event that created the order.
	From    State
	To      State
	Actor   Actor
	Reason  string
	Verdict string
	At      time.Time
}

// Fill is one fill of an order.
type Fill struct {
	ID      int64
	OrderID int64
	// Date is the trading date the shares filled on, midnight Eastern.
	Date     time.Time
	Quantity money.Quantity
	Price    money.Price
	// Value is Quantity × Price, exactly.
	Value  money.Money
	Reason string
	At     time.Time
}

// NewFill is what RecordFill needs.
type NewFill struct {
	// Date is the trading date the shares filled on.
	Date     time.Time
	Quantity money.Quantity
	Price    money.Price
	// Reason says why it filled at that price; it is also the history
	// reason when the fill moves the order.
	Reason string
	// Actor moves the order: Broker for a reported fill, Reconciliation for
	// one found by lookup.
	Actor Actor
	At    time.Time
}

// Querier is what the readers need: a *sql.DB (such as store.Reader()) or a
// *sql.Tx.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Update runs fn in one transaction on the store's writer and commits it if
// fn returns nil. Don't call it while holding a transaction on the writer:
// that deadlocks.
func Update(ctx context.Context, s *store.Store, fn func(tx *sql.Tx) error) (err error) {
	tx, err := s.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("orders: starting the write: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("orders: committing: %w", err)
	}
	return nil
}

// Create makes an order in the proposed state, with its first history row,
// on tx (a transaction on the store's writer; the caller commits it). If an
// order with the same key already exists, Create writes nothing and returns
// that order as it stands, with created false: the first order made for a
// key is the one kept.
func Create(ctx context.Context, tx *sql.Tx, n New, actor Actor, at time.Time) (o Order, created bool, err error) {
	if err := n.check(); err != nil {
		return Order{}, false, err
	}
	if !actorText.MatchString(string(actor)) {
		return Order{}, false, fmt.Errorf("%w: actor %q", ErrInvalid, actor)
	}
	if at.IsZero() {
		return Order{}, false, fmt.Errorf("%w: no creation time", ErrInvalid)
	}
	key := n.Key()
	var price any
	if n.Price != nil {
		price = *n.Price
	}
	now := store.FormatTime(at)
	res, err := tx.ExecContext(ctx, `INSERT INTO orders
		(idempotency_key, strategy, symbol, trading_date, side, kind, order_type, quantity, price,
		 state, reason, strategy_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		key, n.Strategy, n.Symbol, clock.DateOf(n.Date).Format(clock.DateLayout),
		string(n.Kind.Side()), string(n.Kind), string(TypeFor(n.Kind)), n.Quantity, price,
		string(Proposed), n.Reason, n.StrategyHash, now, now)
	if err != nil {
		return Order{}, false, fmt.Errorf("orders: creating %s %s: %w", n.Kind, n.Symbol, err)
	}
	if rows, err := res.RowsAffected(); err != nil {
		return Order{}, false, fmt.Errorf("orders: creating %s %s: %w", n.Kind, n.Symbol, err)
	} else if rows == 0 {
		o, err := ByKey(ctx, tx, key)
		return o, false, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Order{}, false, fmt.Errorf("orders: creating %s %s: %w", n.Kind, n.Symbol, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO order_history
		(order_id, from_state, to_state, actor, reason, at) VALUES (?, NULL, ?, ?, ?, ?)`,
		id, string(Proposed), string(actor), n.Reason, now); err != nil {
		return Order{}, false, fmt.Errorf("orders: recording order %d's creation: %w", id, err)
	}
	o, err = Get(ctx, tx, id)
	return o, true, err
}

// Move changes an order's state on tx (a transaction on the store's writer;
// the caller commits it): it appends the history row and updates the order
// together. A move the state machine does not allow returns a
// *TransitionError wrapping ErrIllegalTransition and writes nothing; the
// caller should roll back and halt trading. Moves to Filled or
// PartiallyFilled are refused with ErrInvalid: use RecordFill, which writes
// the fill with them.
func Move(ctx context.Context, tx *sql.Tx, id int64, c Change) (Order, error) {
	if err := c.check(); err != nil {
		return Order{}, err
	}
	if c.To == Filled || c.To == PartiallyFilled {
		return Order{}, fmt.Errorf("%w: an order becomes %s only through RecordFill", ErrInvalid, c.To)
	}
	o, err := Get(ctx, tx, id)
	if err != nil {
		return Order{}, err
	}
	return move(ctx, tx, o, c)
}

func move(ctx context.Context, tx *sql.Tx, o Order, c Change) (Order, error) {
	if err := check(o.ID, o.Kind, o.State, c.To); err != nil {
		return Order{}, err
	}
	if c.At.Before(o.Updated) {
		return Order{}, fmt.Errorf("%w: change at %s is before the order's last change at %s",
			ErrInvalid, store.FormatTime(c.At), store.FormatTime(o.Updated))
	}
	at := store.FormatTime(c.At)
	var verdict any
	if c.Verdict != "" {
		verdict = c.Verdict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO order_history
		(order_id, from_state, to_state, actor, reason, risk_verdict, at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		o.ID, string(o.State), string(c.To), string(c.Actor), c.Reason, verdict, at); err != nil {
		return Order{}, fmt.Errorf("orders: recording order %d's move to %s: %w", o.ID, c.To, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE orders
		SET state = ?, risk_verdict = coalesce(?, risk_verdict), updated_at = ? WHERE id = ?`,
		string(c.To), verdict, at, o.ID); err != nil {
		return Order{}, fmt.Errorf("orders: moving order %d to %s: %w", o.ID, c.To, err)
	}
	o.State = c.To
	if c.Verdict != "" {
		o.Verdict = c.Verdict
	}
	o.Updated = c.At.UTC()
	return o, nil
}

// RecordFill appends a fill to an order on tx (a transaction on the store's
// writer; the caller commits it) and moves the order to Filled when its
// fills reach its quantity, or to PartiallyFilled when they don't yet (an
// order already partially filled stays so, with no new history row). It
// refuses (ErrOverfill) a fill that would take the order past its quantity,
// and returns a *TransitionError if the order's state can't take a fill,
// such as one that was never sent. Nothing is written when it refuses.
func RecordFill(ctx context.Context, tx *sql.Tx, id int64, f NewFill) (Order, Fill, error) {
	switch {
	case f.Date.IsZero():
		return Order{}, Fill{}, fmt.Errorf("%w: a fill needs a trading date", ErrInvalid)
	case f.Quantity.IsZero():
		return Order{}, Fill{}, fmt.Errorf("%w: a fill needs shares", ErrInvalid)
	case f.Price.IsZero():
		return Order{}, Fill{}, fmt.Errorf("%w: a fill needs a price", ErrInvalid)
	case f.Reason == "":
		return Order{}, Fill{}, fmt.Errorf("%w: a fill needs a reason", ErrInvalid)
	}
	o, err := Get(ctx, tx, id)
	if err != nil {
		return Order{}, Fill{}, err
	}
	prior, err := Fills(ctx, tx, id)
	if err != nil {
		return Order{}, Fill{}, err
	}
	total := f.Quantity
	for _, p := range prior {
		total = total.Add(p.Quantity)
	}
	cmp := total.Cmp(o.Quantity)
	to := PartiallyFilled
	if cmp >= 0 {
		to = Filled
	}
	c := Change{To: to, Actor: f.Actor, Reason: f.Reason, At: f.At}
	if err := c.check(); err != nil {
		return Order{}, Fill{}, err
	}
	// A state that can't take a fill is refused as an illegal move before
	// the quantity is looked at.
	if o.State != to {
		if err := check(o.ID, o.Kind, o.State, to); err != nil {
			return Order{}, Fill{}, err
		}
	}
	if cmp > 0 {
		return Order{}, Fill{}, fmt.Errorf("%w: order %d is for %s shares; this fill of %s brings it to %s",
			ErrOverfill, id, o.Quantity, f.Quantity, total)
	}
	if o.State != to {
		if o, err = move(ctx, tx, o, c); err != nil {
			return Order{}, Fill{}, err
		}
	}
	fill := Fill{
		OrderID:  id,
		Date:     clock.DateOf(f.Date),
		Quantity: f.Quantity,
		Price:    f.Price,
		Value:    f.Price.Mul(f.Quantity),
		Reason:   f.Reason,
		At:       f.At.UTC(),
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO fills
		(order_id, trading_date, quantity, price, value, reason, filled_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, fill.Date.Format(clock.DateLayout), fill.Quantity, fill.Price, fill.Value, fill.Reason,
		store.FormatTime(fill.At))
	if err != nil {
		return Order{}, Fill{}, fmt.Errorf("orders: recording a fill of order %d: %w", id, err)
	}
	if fill.ID, err = res.LastInsertId(); err != nil {
		return Order{}, Fill{}, fmt.Errorf("orders: recording a fill of order %d: %w", id, err)
	}
	return o, fill, nil
}

// Replay walks an order's history from its creation and returns the state it
// ends in, checking that each event starts where the last ended and that
// every move is allowed for an order of kind k. A stored order's state
// always equals Replay of its history.
func Replay(k signals.Kind, history []Event) (State, error) {
	var at State
	for i, e := range history {
		if e.From != at {
			return "", fmt.Errorf("%w: event %d starts at %q, but the order was %q", ErrCorrupt, i, e.From, at)
		}
		if i == 0 {
			if e.To != Proposed {
				return "", fmt.Errorf("%w: the first event goes to %q, not %q", ErrCorrupt, e.To, Proposed)
			}
		} else if err := check(e.OrderID, k, e.From, e.To); err != nil {
			return "", err
		}
		at = e.To
	}
	if at == "" {
		return "", fmt.Errorf("%w: no history", ErrCorrupt)
	}
	return at, nil
}
