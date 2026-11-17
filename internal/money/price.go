package money

import (
	"database/sql/driver"
	"fmt"
	"log/slog"

	"github.com/shopspring/decimal"
)

// Price is the price of one share, in dollars. It is never negative and is
// always a whole number of cents: that is the written rounding policy. Parsing
// refuses a sub-cent price rather than rounding it; RoundPrice rounds a
// computed one. The zero value is a price of zero.
//
// Price is not comparable with ==. Use Equal or Cmp.
type Price struct {
	_ [0]func()
	d decimal.Decimal
}

// ParsePrice parses a plain decimal such as "187.25". It fails on a negative
// value or one with more than two decimal places ("1.50" and "1.500" are fine,
// "1.505" is not).
func ParsePrice(s string) (Price, error) {
	d, err := parseDecimal(s)
	if err != nil {
		return Price{}, err
	}
	return checkPrice(d)
}

// MustParsePrice is ParsePrice for constants in code and tests. It panics on
// bad input.
func MustParsePrice(s string) Price {
	p, err := ParsePrice(s)
	if err != nil {
		panic(err)
	}
	return p
}

// PriceFromCents returns a price of n cents. It fails if n is negative.
func PriceFromCents(n int64) (Price, error) {
	return checkPrice(decimal.New(n, -CentPlaces))
}

// RoundPrice rounds a computed per-share amount to the cent, halves away from
// zero, the same as Money.RoundCents. It fails if the result is negative.
func RoundPrice(d decimal.Decimal) (Price, error) {
	return checkPrice(d.Round(CentPlaces))
}

func checkPrice(d decimal.Decimal) (Price, error) {
	if d.IsNegative() {
		return Price{}, fmt.Errorf("%w: price %s", ErrNegative, d.String())
	}
	if places(d) > CentPlaces {
		return Price{}, fmt.Errorf("%w: price %s is finer than a cent", ErrPrecision, d.String())
	}
	return Price{d: d}, nil
}

// Decimal returns the exact decimal value.
func (p Price) Decimal() decimal.Decimal { return p.d }

// Money returns the price as an amount of money (the cost of one share).
func (p Price) Money() Money { return Money{d: p.d} }

// Mul returns the cost of q shares at p, exactly. With fractional shares the
// result may run past the cent; round it with RoundCents where a cent amount
// is needed.
func (p Price) Mul(q Quantity) Money { return Money{d: p.d.Mul(q.d)} }

// Cmp returns -1, 0 or +1 as p is less than, equal to or greater than o.
func (p Price) Cmp(o Price) int { return p.d.Cmp(o.d) }

// Equal reports whether p and o are the same price.
func (p Price) Equal(o Price) bool { return p.d.Equal(o.d) }

// IsZero reports whether p is zero.
func (p Price) IsZero() bool { return p.d.IsZero() }

// String formats p with exactly two decimal places: "187.25", "3.00".
func (p Price) String() string { return p.d.StringFixed(CentPlaces) }

// LogValue logs p as its text.
func (p Price) LogValue() slog.Value { return slog.StringValue(p.String()) }

// Value stores p as TEXT.
func (p Price) Value() (driver.Value, error) { return p.String(), nil }

// Scan reads p from TEXT. A REAL, INTEGER or NULL column value is refused, as
// is a negative or sub-cent price.
func (p *Price) Scan(src any) error {
	s, err := scanText(src)
	if err != nil {
		return err
	}
	v, err := ParsePrice(s)
	if err != nil {
		return err
	}
	*p = v
	return nil
}

// MarshalText returns String.
func (p Price) MarshalText() ([]byte, error) { return []byte(p.String()), nil }

// UnmarshalText parses with ParsePrice.
func (p *Price) UnmarshalText(b []byte) error {
	v, err := ParsePrice(string(b))
	if err != nil {
		return err
	}
	*p = v
	return nil
}

// MarshalJSON writes p as a JSON string.
func (p Price) MarshalJSON() ([]byte, error) { return quote(p.String()), nil }

// UnmarshalJSON reads p from a JSON string. A JSON number or null is refused.
func (p *Price) UnmarshalJSON(b []byte) error {
	s, err := jsonText(b)
	if err != nil {
		return err
	}
	return p.UnmarshalText([]byte(s))
}
