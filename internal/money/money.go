package money

import (
	"database/sql/driver"
	"log/slog"

	"github.com/shopspring/decimal"
)

// CentPlaces is the number of decimal places in a cent-rounded amount.
const CentPlaces = 2

// Money is an exact amount of dollars. It may be negative (a loss, a cash
// outflow) and it keeps every digit it is given: the cost of a fractional
// share can run past the cent, and RoundCents rounds it when a cent amount is
// needed. The zero value is zero dollars.
//
// Money is not comparable with ==, because 1.5 and 1.50 are the same amount
// held differently. Use Equal or Cmp.
type Money struct {
	_ [0]func()
	d decimal.Decimal
}

// ParseMoney parses a plain decimal such as "1234.56" or "-0.5" exactly.
func ParseMoney(s string) (Money, error) {
	d, err := parseDecimal(s)
	if err != nil {
		return Money{}, err
	}
	return Money{d: d}, nil
}

// MustParseMoney is ParseMoney for constants in code and tests. It panics on
// bad input.
func MustParseMoney(s string) Money {
	m, err := ParseMoney(s)
	if err != nil {
		panic(err)
	}
	return m
}

// Cents returns the amount of n cents.
func Cents(n int64) Money { return Money{d: decimal.New(n, -CentPlaces)} }

// FromDecimal returns an exact decimal as Money, for rule arithmetic that works on
// decimals directly.
func FromDecimal(d decimal.Decimal) Money { return Money{d: d} }

// Decimal returns the exact decimal value.
func (m Money) Decimal() decimal.Decimal { return m.d }

// Add returns m + o exactly.
func (m Money) Add(o Money) Money { return Money{d: m.d.Add(o.d)} }

// Sub returns m - o exactly.
func (m Money) Sub(o Money) Money { return Money{d: m.d.Sub(o.d)} }

// Neg returns -m.
func (m Money) Neg() Money { return Money{d: m.d.Neg()} }

// Abs returns the absolute value of m.
func (m Money) Abs() Money { return Money{d: m.d.Abs()} }

// RoundCents rounds m to the cent, halves away from zero, so 0.005 becomes
// 0.01 and -0.005 becomes -0.01.
func (m Money) RoundCents() Money { return Money{d: m.d.Round(CentPlaces)} }

// IsCents reports whether m is a whole number of cents.
func (m Money) IsCents() bool { return places(m.d) <= CentPlaces }

// Cmp returns -1, 0 or +1 as m is less than, equal to or greater than o.
func (m Money) Cmp(o Money) int { return m.d.Cmp(o.d) }

// Equal reports whether m and o are the same amount.
func (m Money) Equal(o Money) bool { return m.d.Equal(o.d) }

// Sign returns -1, 0 or +1 for a negative, zero or positive amount.
func (m Money) Sign() int { return m.d.Sign() }

// IsZero reports whether m is zero.
func (m Money) IsZero() bool { return m.d.IsZero() }

// IsNegative reports whether m is below zero.
func (m Money) IsNegative() bool { return m.d.IsNegative() }

// IsPositive reports whether m is above zero.
func (m Money) IsPositive() bool { return m.d.IsPositive() }

// String formats m with at least two decimal places and every further digit
// it holds: "12.50", "-3.00", "0.123456".
func (m Money) String() string { return fixedAtLeast(m.d, CentPlaces) }

// LogValue logs m as its exact text.
func (m Money) LogValue() slog.Value { return slog.StringValue(m.String()) }

// Value stores m as TEXT.
func (m Money) Value() (driver.Value, error) { return m.String(), nil }

// Scan reads m from TEXT. A REAL, INTEGER or NULL column value is refused.
func (m *Money) Scan(src any) error {
	s, err := scanText(src)
	if err != nil {
		return err
	}
	v, err := ParseMoney(s)
	if err != nil {
		return err
	}
	*m = v
	return nil
}

// MarshalText returns String.
func (m Money) MarshalText() ([]byte, error) { return []byte(m.String()), nil }

// UnmarshalText parses with ParseMoney.
func (m *Money) UnmarshalText(b []byte) error {
	v, err := ParseMoney(string(b))
	if err != nil {
		return err
	}
	*m = v
	return nil
}

// MarshalJSON writes m as a JSON string.
func (m Money) MarshalJSON() ([]byte, error) { return quote(m.String()), nil }

// UnmarshalJSON reads m from a JSON string. A JSON number or null is refused.
func (m *Money) UnmarshalJSON(b []byte) error {
	s, err := jsonText(b)
	if err != nil {
		return err
	}
	return m.UnmarshalText([]byte(s))
}
