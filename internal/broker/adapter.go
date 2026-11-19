package broker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alexherrero/sherwood/internal/money"
)

// Adapter is the broker as sherwood sees it: one small interface with three
// implementations. The paper adapter (package paper) fills orders against
// each day's checked prices, the fake (package brokertest) returns scripted
// answers in tests, and the Robinhood adapter (live trading) passes each call
// to the broker helper.
//
// Every non-nil error means the outcome is unknown, and the caller must
// treat it as a broker error: halt trading and never retry the call. An
// answer the broker gives, such as a refusal, is a Report, not an error.
type Adapter interface {
	// Place sends an order. The answer is StatusAccepted or StatusRejected.
	Place(ctx context.Context, o Order) (Report, error)
	// Cancel calls off an open order by its reference. The answer is
	// StatusCancelled, or the order's status if it can no longer be called
	// off.
	Cancel(ctx context.Context, ref string) (Report, error)
	// Lookup reads one order's status by its reference.
	Lookup(ctx context.Context, ref string) (Report, error)
	// OpenOrders lists the orders still open at the broker.
	OpenOrders(ctx context.Context) ([]Report, error)
	// Holdings lists the shares held.
	Holdings(ctx context.Context) ([]Position, error)
	// Account reads the cash and the account's value.
	Account(ctx context.Context) (Account, error)
}

// Health is what an adapter that can tell whether it is answering offers. An
// adapter without it counts as healthy, as the in-process paper adapter is.
type Health interface {
	Healthy() bool
}

// CashReader is what an adapter that can read the cash on its own offers.
// Reconciliation compares only the cash, so it prefers this to Account: the
// paper adapter can read its cash even while a holding has no checked close
// to value it at.
type CashReader interface {
	Cash(ctx context.Context) (money.Money, error)
}

// Side is an order's side.
type Side string

// The sides.
const (
	Buy  Side = "buy"
	Sell Side = "sell"
)

// Type is how an order is sent.
type Type string

// The order types, the same three as the orders table.
const (
	// DayLimitBuy is a buy with a limit price, good for one session.
	DayLimitBuy Type = "day_limit_buy"
	// Stop is a standing stop order that rests until it fills or is called
	// off.
	Stop Type = "stop"
	// SellAtOpen is a sell made at the session's open.
	SellAtOpen Type = "sell_at_open"
)

// Order is an order as the broker is asked to take it.
type Order struct {
	// Ref is sherwood's idempotency key for the order, passed to the broker
	// as the client reference.
	Ref    string
	Symbol string
	Side   Side
	Type   Type
	// Quantity is the shares to buy or sell.
	Quantity money.Quantity
	// Price is the limit of a DayLimitBuy or the stop of a Stop; nil for a
	// SellAtOpen.
	Price *money.Price
	// Session is the trading date the order is for, midnight Eastern.
	Session time.Time
}

// ErrBadOrder wraps the reason Order.Check refuses an order.
var ErrBadOrder = errors.New("broker: malformed order")

// Check reports whether the order is well formed: a reference, a symbol, a
// side and type that go together, some shares, a price exactly when the type
// needs one, and a session.
func (o Order) Check() error {
	switch {
	case o.Ref == "":
		return fmt.Errorf("%w: no reference", ErrBadOrder)
	case o.Symbol == "":
		return fmt.Errorf("%w: no symbol", ErrBadOrder)
	case o.Quantity.IsZero():
		return fmt.Errorf("%w: no shares", ErrBadOrder)
	case o.Session.IsZero():
		return fmt.Errorf("%w: no session", ErrBadOrder)
	}
	switch o.Type {
	case DayLimitBuy:
		if o.Side != Buy {
			return fmt.Errorf("%w: a %s must be a buy", ErrBadOrder, o.Type)
		}
	case Stop, SellAtOpen:
		if o.Side != Sell {
			return fmt.Errorf("%w: a %s must be a sell", ErrBadOrder, o.Type)
		}
	default:
		return fmt.Errorf("%w: unknown type %q", ErrBadOrder, o.Type)
	}
	needsPrice := o.Type != SellAtOpen
	switch {
	case needsPrice && (o.Price == nil || o.Price.IsZero()):
		return fmt.Errorf("%w: a %s needs a price", ErrBadOrder, o.Type)
	case !needsPrice && o.Price != nil:
		return fmt.Errorf("%w: a %s takes no price", ErrBadOrder, o.Type)
	}
	return nil
}

// Status is an order's status at the broker.
type Status string

// The statuses a broker reports.
const (
	StatusAccepted        Status = "accepted"
	StatusRejected        Status = "rejected"
	StatusPartiallyFilled Status = "partially_filled"
	StatusFilled          Status = "filled"
	StatusCancelled       Status = "cancelled"
	StatusExpired         Status = "expired"
)

// Valid reports whether s is one of the statuses.
func (s Status) Valid() bool {
	switch s {
	case StatusAccepted, StatusRejected, StatusPartiallyFilled, StatusFilled, StatusCancelled, StatusExpired:
		return true
	}
	return false
}

// Report is the broker's answer about one order.
type Report struct {
	Ref    string
	Status Status
	// Filled is the shares filled so far.
	Filled money.Quantity
	// FillPrice is the average price of the shares filled so far, when the
	// broker says; nil otherwise. Reconciliation needs it to record a fill
	// it finds by lookup, and refuses to guess one.
	FillPrice *money.Price
	// Detail is the broker's reason, in plain words, for a refusal or a
	// status it could not change; empty otherwise.
	Detail string
}

// Position is a holding at the broker.
type Position struct {
	Symbol   string
	Quantity money.Quantity
	// AverageCost is the cost per share, as the broker keeps it.
	AverageCost money.Money
}

// Account is the account's cash and value.
type Account struct {
	Cash money.Money
	// Value is the cash plus the holdings at their latest prices.
	Value money.Money
}
