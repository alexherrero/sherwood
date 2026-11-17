package money

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"

	"github.com/shopspring/decimal"
)

// Errors returned when a value cannot become one of the money types. Every
// error from this package wraps exactly one of them.
var (
	// ErrSyntax means the text is not a plain decimal such as "12.5" or "-3".
	ErrSyntax = errors.New("money: not a plain decimal")
	// ErrNotText means a database or JSON value was not text. A JSON number
	// and a SQLite REAL both land here, because either one may already have
	// passed through floating point.
	ErrNotText = errors.New("money: value is not text")
	// ErrPrecision means a value has more decimal places than its type allows.
	// Parsing never rounds silently; use the Round functions for that.
	ErrPrecision = errors.New("money: too many decimal places")
	// ErrNegative means a price or share count was below zero.
	ErrNegative = errors.New("money: negative value")
	// ErrDivideByZero means a share count was asked for at a price of zero.
	ErrDivideByZero = errors.New("money: price is zero")
	// ErrPolicy means a QuantityPolicy was left unset.
	ErrPolicy = errors.New("money: share policy not set")
)

// plainDecimal is the only text form accepted anywhere: an optional minus, at
// least one digit, and optionally a point followed by at least one digit. No
// exponent, no plus sign, no spaces, no NaN or Infinity.
var plainDecimal = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// maxTextLen bounds the text this package will parse, so a hostile value
// cannot make it build an enormous number.
const maxTextLen = 64

// parseDecimal parses s as a plain decimal.
func parseDecimal(s string) (decimal.Decimal, error) {
	if len(s) > maxTextLen || !plainDecimal.MatchString(s) {
		return decimal.Decimal{}, fmt.Errorf("%w: %q", ErrSyntax, truncate(s))
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%w: %q", ErrSyntax, truncate(s))
	}
	return d, nil
}

// truncate shortens text quoted in an error message.
func truncate(s string) string {
	if len(s) > maxTextLen {
		return s[:maxTextLen] + "..."
	}
	return s
}

// places is the number of digits after the point once trailing zeros are
// ignored, so "1.50" has one place.
func places(d decimal.Decimal) int32 {
	for p := int32(0); ; p++ {
		if d.Equal(d.Truncate(p)) {
			return p
		}
	}
}

// fixedAtLeast formats d with at least min decimal places and no trailing
// zeros beyond that.
func fixedAtLeast(d decimal.Decimal, minPlaces int32) string {
	return d.StringFixed(max(minPlaces, places(d)))
}

// scanText turns a database value into text. Only TEXT (string) and BLOB-shaped
// text ([]byte) are accepted; a SQLite REAL, an INTEGER or NULL is refused.
func scanText(src any) (string, error) {
	switch v := src.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	case nil:
		return "", fmt.Errorf("%w: got NULL (scan into sql.Null[T] for a nullable column)", ErrNotText)
	default:
		return "", fmt.Errorf("%w: got %T", ErrNotText, src)
	}
}

// jsonText unquotes a JSON string. Anything else, including a number and
// null, is refused. An optional value should be a pointer field, which
// encoding/json sets to nil on null without calling the type's decoder.
func jsonText(data []byte) (string, error) {
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return "", fmt.Errorf("%w: JSON value must be a string, got %s", ErrNotText, truncate(string(data)))
	}
	inner := data[1 : len(data)-1]
	// A plain decimal never needs escaping, so a backslash or quote inside
	// can only be a malformed or disguised value.
	if bytes.ContainsAny(inner, "\\\"") {
		return "", fmt.Errorf("%w: %s", ErrSyntax, truncate(string(data)))
	}
	return string(inner), nil
}

// quote wraps a plain decimal in JSON quotes. Plain decimals need no escaping.
func quote(s string) []byte {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	b = append(b, s...)
	return append(b, '"')
}
