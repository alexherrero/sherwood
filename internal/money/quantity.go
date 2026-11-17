package money

import (
	"database/sql/driver"
	"fmt"
	"log/slog"

	"github.com/shopspring/decimal"
)

// FractionalPlaces is the finest share count sherwood handles when fractional
// shares are on: a millionth of a share.
const FractionalPlaces = 6

// Quantity is a number of shares. It is never negative (a sale is an order
// side, not a negative count) and never finer than FractionalPlaces. Whether
// it must be whole is decided by a QuantityPolicy, which the caller passes in
// from the strategy file. The zero value is zero shares.
//
// Quantity is not comparable with ==. Use Equal or Cmp.
type Quantity struct {
	_ [0]func()
	d decimal.Decimal
}

// QuantityPolicy is the written rounding policy for share counts: whole shares,
// unless fractional shares are turned on in the strategy file. The zero value
// is deliberately invalid, so every caller has to choose.
type QuantityPolicy int

// The two share policies.
const (
	// WholeShares allows only whole share counts.
	WholeShares QuantityPolicy = iota + 1
	// FractionalShares allows share counts to FractionalPlaces decimal places.
	FractionalShares
)

// PolicyFor returns FractionalShares when fractional is true and WholeShares
// otherwise. It maps the strategy file's setting onto a policy.
func PolicyFor(fractional bool) QuantityPolicy {
	if fractional {
		return FractionalShares
	}
	return WholeShares
}

// Places returns how many decimal places a share count may have under p.
func (p QuantityPolicy) Places() (int32, error) {
	switch p {
	case WholeShares:
		return 0, nil
	case FractionalShares:
		return FractionalPlaces, nil
	default:
		return 0, fmt.Errorf("%w: %d", ErrPolicy, int(p))
	}
}

// String names the policy.
func (p QuantityPolicy) String() string {
	switch p {
	case WholeShares:
		return "whole shares"
	case FractionalShares:
		return "fractional shares"
	default:
		return fmt.Sprintf("QuantityPolicy(%d)", int(p))
	}
}

// Parse parses a share count that must already fit p. It never rounds: "2.5"
// fails under WholeShares.
func (p QuantityPolicy) Parse(s string) (Quantity, error) {
	q, err := ParseQuantity(s)
	if err != nil {
		return Quantity{}, err
	}
	if err := p.Check(q); err != nil {
		return Quantity{}, err
	}
	return q, nil
}

// Check reports whether q fits p.
func (p QuantityPolicy) Check(q Quantity) error {
	n, err := p.Places()
	if err != nil {
		return err
	}
	if places(q.d) > n {
		return fmt.Errorf("%w: %s is not allowed with %s", ErrPrecision, q.String(), p)
	}
	return nil
}

// Round rounds a computed share count down (toward zero) to what p allows, so
// sizing never buys more than was worked out. It fails if d is negative.
func (p QuantityPolicy) Round(d decimal.Decimal) (Quantity, error) {
	n, err := p.Places()
	if err != nil {
		return Quantity{}, err
	}
	if d.IsNegative() {
		return Quantity{}, fmt.Errorf("%w: share count %s", ErrNegative, d.String())
	}
	return Quantity{d: d.Truncate(n)}, nil
}

// SharesFor returns the most shares p allows that budget buys at price: the
// quotient rounded down, worked out exactly. A zero or negative budget gives
// zero shares; a zero price is an error.
func (p QuantityPolicy) SharesFor(budget Money, price Price) (Quantity, error) {
	n, err := p.Places()
	if err != nil {
		return Quantity{}, err
	}
	if price.IsZero() {
		return Quantity{}, ErrDivideByZero
	}
	if !budget.IsPositive() {
		return Quantity{}, nil
	}
	q, _ := budget.d.QuoRem(price.d, n)
	return Quantity{d: q}, nil
}

// ParseQuantity parses a share count with no policy applied beyond the type's
// own limits: not negative and at most FractionalPlaces decimal places. Use
// QuantityPolicy.Parse where the strategy's policy is known.
func ParseQuantity(s string) (Quantity, error) {
	d, err := parseDecimal(s)
	if err != nil {
		return Quantity{}, err
	}
	return checkQuantity(d)
}

// MustParseQuantity is ParseQuantity for constants in code and tests. It
// panics on bad input.
func MustParseQuantity(s string) Quantity {
	q, err := ParseQuantity(s)
	if err != nil {
		panic(err)
	}
	return q
}

// Shares returns a whole number of shares. It fails if n is negative.
func Shares(n int64) (Quantity, error) { return checkQuantity(decimal.NewFromInt(n)) }

func checkQuantity(d decimal.Decimal) (Quantity, error) {
	if d.IsNegative() {
		return Quantity{}, fmt.Errorf("%w: share count %s", ErrNegative, d.String())
	}
	if places(d) > FractionalPlaces {
		return Quantity{}, fmt.Errorf("%w: share count %s is finer than %d places", ErrPrecision, d.String(), FractionalPlaces)
	}
	return Quantity{d: d}, nil
}

// Decimal returns the exact decimal value.
func (q Quantity) Decimal() decimal.Decimal { return q.d }

// Add returns q + o exactly.
func (q Quantity) Add(o Quantity) Quantity { return Quantity{d: q.d.Add(o.d)} }

// Sub returns q - o exactly. It fails if o is greater than q, because a share
// count cannot go below zero.
func (q Quantity) Sub(o Quantity) (Quantity, error) {
	d := q.d.Sub(o.d)
	if d.IsNegative() {
		return Quantity{}, fmt.Errorf("%w: %s - %s", ErrNegative, q.String(), o.String())
	}
	return Quantity{d: d}, nil
}

// Cmp returns -1, 0 or +1 as q is less than, equal to or greater than o.
func (q Quantity) Cmp(o Quantity) int { return q.d.Cmp(o.d) }

// Equal reports whether q and o are the same count.
func (q Quantity) Equal(o Quantity) bool { return q.d.Equal(o.d) }

// IsZero reports whether q is zero.
func (q Quantity) IsZero() bool { return q.d.IsZero() }

// IsWhole reports whether q is a whole number of shares.
func (q Quantity) IsWhole() bool { return q.d.IsInteger() }

// String formats q with no trailing zeros: "10", "0.25".
func (q Quantity) String() string { return fixedAtLeast(q.d, 0) }

// LogValue logs q as its text.
func (q Quantity) LogValue() slog.Value { return slog.StringValue(q.String()) }

// Value stores q as TEXT.
func (q Quantity) Value() (driver.Value, error) { return q.String(), nil }

// Scan reads q from TEXT. A REAL, INTEGER or NULL column value is refused, as
// is a negative count or one finer than FractionalPlaces. The strategy's
// policy is not applied here; call QuantityPolicy.Check where it matters.
func (q *Quantity) Scan(src any) error {
	s, err := scanText(src)
	if err != nil {
		return err
	}
	v, err := ParseQuantity(s)
	if err != nil {
		return err
	}
	*q = v
	return nil
}

// MarshalText returns String.
func (q Quantity) MarshalText() ([]byte, error) { return []byte(q.String()), nil }

// UnmarshalText parses with ParseQuantity.
func (q *Quantity) UnmarshalText(b []byte) error {
	v, err := ParseQuantity(string(b))
	if err != nil {
		return err
	}
	*q = v
	return nil
}

// MarshalJSON writes q as a JSON string.
func (q Quantity) MarshalJSON() ([]byte, error) { return quote(q.String()), nil }

// UnmarshalJSON reads q from a JSON string. A JSON number or null is refused.
func (q *Quantity) UnmarshalJSON(b []byte) error {
	s, err := jsonText(b)
	if err != nil {
		return err
	}
	return q.UnmarshalText([]byte(s))
}
