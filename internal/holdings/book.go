package holdings

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alexherrero/sherwood/internal/money"
)

// Errors a Book returns for a sell it cannot apply.
var (
	// ErrNotHeld means a sell of a stock that is not held.
	ErrNotHeld = errors.New("holdings: a sell of a stock that is not held")
	// ErrOversold means a sell of more shares than are held.
	ErrOversold = errors.New("holdings: a sell of more shares than are held")
)

// costPlaces is how many places an average cost is kept to after an add.
const costPlaces = 6

// Book is the holdings and cash as fills change them: read from the newest
// snapshot, changed fill by fill, and written back as one new snapshot. The
// paper broker's settlement and the live fills that reconciliation records
// both use it, so a fill changes the records the same way in either mode.
type Book struct {
	held map[string]Holding
	cash money.Money
}

// ReadBook reads the newest holdings and cash, inside the caller's
// transaction. With nothing recorded, the book is empty.
func ReadBook(ctx context.Context, q Querier) (*Book, error) {
	bk := &Book{held: map[string]Holding{}}
	set, err := Current(ctx, q)
	if err != nil && !errors.Is(err, ErrNone) {
		return nil, err
	}
	for _, h := range set.Holdings {
		bk.held[h.Symbol] = h
	}
	cash, err := LatestCash(ctx, q)
	switch {
	case errors.Is(err, ErrNone):
	case err != nil:
		return nil, err
	default:
		bk.cash = cash.Amount
	}
	return bk, nil
}

// Buy applies a buy of qty shares of symbol at price, filled on date: the
// cash falls by the fill's value, and the holding grows. An add's average
// cost is (old cost + fill value) / new quantity, kept to 6 places, and the
// holding keeps its Opened date.
func (b *Book) Buy(symbol string, qty money.Quantity, price money.Price, date time.Time) {
	value := price.Mul(qty)
	b.cash = b.cash.Sub(value)
	h, held := b.held[symbol]
	if !held {
		b.held[symbol] = Holding{Symbol: symbol, Quantity: qty, AverageCost: price.Money(), Opened: date}
		return
	}
	total := h.Quantity.Add(qty)
	cost := h.Cost().Add(value).Decimal().DivRound(total.Decimal(), costPlaces)
	h.Quantity, h.AverageCost = total, money.FromDecimal(cost)
	b.held[symbol] = h
}

// Sell applies a sell of qty shares of symbol at price: the cash rises by
// the fill's value, and the holding shrinks, or goes once no shares are
// left. It refuses (ErrNotHeld, ErrOversold) a sell it cannot apply, and
// changes nothing then.
func (b *Book) Sell(symbol string, qty money.Quantity, price money.Price) error {
	h, held := b.held[symbol]
	if !held {
		return fmt.Errorf("%w: %s shares of %s", ErrNotHeld, qty, symbol)
	}
	left, err := h.Quantity.Sub(qty)
	if err != nil {
		return fmt.Errorf("%w: %s shares of %s, but only %s are held", ErrOversold, qty, symbol, h.Quantity)
	}
	b.cash = b.cash.Add(price.Mul(qty))
	if left.IsZero() {
		delete(b.held, symbol)
		return nil
	}
	h.Quantity = left
	b.held[symbol] = h
	return nil
}

// Write records the book as a new holdings snapshot and cash reading, both
// with source src as of at, inside the caller's transaction.
func (b *Book) Write(ctx context.Context, tx Execer, src Source, at time.Time) (Set, Cash, error) {
	hs := make([]Holding, 0, len(b.held))
	for _, h := range b.held {
		hs = append(hs, h)
	}
	set, err := Replace(ctx, tx, src, at, hs)
	if err != nil {
		return Set{}, Cash{}, err
	}
	cash, err := RecordCash(ctx, tx, src, at, b.cash)
	if err != nil {
		return Set{}, Cash{}, err
	}
	return set, cash, nil
}
