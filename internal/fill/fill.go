// Package fill is the paper fill model: how an order would have filled
// against one trading day's prices. It is a pure function of the order and the
// day, so paper mode and the back-tester fill orders the same way. Paper
// trading's paper broker wraps it; the back-tester calls it directly.
//
// The model, from the design's broker adapter section:
//
//   - A buy limit order fills at the day's open if the open is at or below
//     its limit. Otherwise it fills at the limit if the day's low traded
//     through it, meaning the low was below the limit. A low that only touches
//     the limit is not a fill: a touch does not prove a seller met the order.
//   - A resting stop fills when the day's low reaches it (at or below the
//     stop), at the stop. If the stock opened at or below the stop, it fills
//     at the open instead, because the first trade was already past it.
//   - A sell at the open fills at the day's open. It is the model for a rule's
//     ordinary exit, which is sent before the session opens.
//
// Every price is an exact money.Price and every share count a money.Quantity
// checked against the caller's share policy. Nothing here rounds.
package fill

import (
	"errors"
	"fmt"

	"github.com/alexherrero/sherwood/internal/money"
)

// Kind is the kind of order being filled.
type Kind string

// The order kinds the model fills.
const (
	// BuyLimit is a day buy order with a limit price.
	BuyLimit Kind = "buy_limit"
	// SellStop is a protective stop resting at the broker.
	SellStop Kind = "sell_stop"
	// SellAtOpen is a sell made at the day's open.
	SellAtOpen Kind = "sell_at_open"
)

// Order is an order waiting to be worked against a day's prices.
type Order struct {
	Kind     Kind
	Quantity money.Quantity
	// Price is the limit of a BuyLimit or the stop of a SellStop. A
	// SellAtOpen ignores it.
	Price money.Price
}

// Day is the part of one trading day's prices the model reads.
type Day struct {
	Open, High, Low money.Price
}

// Fill is a filled order.
type Fill struct {
	Quantity money.Quantity
	Price    money.Price
	// Value is Quantity × Price, exactly.
	Value money.Money
	// Reason says why it filled at that price, such as "opened at 9.90, at
	// or below its limit of 10.00".
	Reason string
}

// Errors from Apply.
var (
	// ErrOrder means the order is not one the model can fill: an unknown
	// kind, no shares, or no price on a limit or stop.
	ErrOrder = errors.New("fill: bad order")
	// ErrDay means the day's prices are not consistent: the low is above the
	// open or the high is below it.
	ErrDay = errors.New("fill: bad day")
)

// Apply works the order against the day. It returns the fill and true if the
// order filled, or a reason and false if it did not. The share count is
// checked against policy and filled in full or not at all.
func Apply(o Order, d Day, policy money.QuantityPolicy) (Fill, bool, error) {
	if err := policy.Check(o.Quantity); err != nil {
		return Fill{}, false, fmt.Errorf("%w: %w", ErrOrder, err)
	}
	if o.Quantity.IsZero() {
		return Fill{}, false, fmt.Errorf("%w: no shares", ErrOrder)
	}
	if d.Low.Cmp(d.Open) > 0 || d.High.Cmp(d.Open) < 0 || d.Low.Cmp(d.High) > 0 {
		return Fill{}, false, fmt.Errorf("%w: open %s, high %s, low %s", ErrDay, d.Open, d.High, d.Low)
	}
	if (o.Kind == BuyLimit || o.Kind == SellStop) && o.Price.IsZero() {
		return Fill{}, false, fmt.Errorf("%w: %s has no price", ErrOrder, o.Kind)
	}
	var (
		price  money.Price
		reason string
		filled bool
	)
	switch o.Kind {
	case BuyLimit:
		price, reason, filled = buyLimit(o.Price, d)
	case SellStop:
		price, reason, filled = sellStop(o.Price, d)
	case SellAtOpen:
		price, reason, filled = d.Open, fmt.Sprintf("sold at the open of %s", d.Open), true
	default:
		return Fill{}, false, fmt.Errorf("%w: unknown kind %q", ErrOrder, o.Kind)
	}
	if !filled {
		return Fill{Reason: reason}, false, nil
	}
	return Fill{Quantity: o.Quantity, Price: price, Value: price.Mul(o.Quantity), Reason: reason}, true, nil
}

// buyLimit fills at the open when the open is at or below the limit, else at
// the limit when the low went below it.
func buyLimit(limit money.Price, d Day) (money.Price, string, bool) {
	switch {
	case d.Open.Cmp(limit) <= 0:
		return d.Open, fmt.Sprintf("opened at %s, at or below its limit of %s", d.Open, limit), true
	case d.Low.Cmp(limit) < 0:
		return limit, fmt.Sprintf("traded down to %s, through its limit of %s", d.Low, limit), true
	default:
		return money.Price{}, fmt.Sprintf("never traded below its limit of %s (low %s)", limit, d.Low), false
	}
}

// sellStop fills at the open when the stock opened at or below the stop, else
// at the stop when the low reached it.
func sellStop(stop money.Price, d Day) (money.Price, string, bool) {
	switch {
	case d.Open.Cmp(stop) <= 0:
		return d.Open, fmt.Sprintf("opened at %s, at or below its stop of %s", d.Open, stop), true
	case d.Low.Cmp(stop) <= 0:
		return stop, fmt.Sprintf("traded down to %s, reaching its stop of %s", d.Low, stop), true
	default:
		return money.Price{}, fmt.Sprintf("stayed above its stop of %s (low %s)", stop, d.Low), false
	}
}
