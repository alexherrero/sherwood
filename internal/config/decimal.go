package config

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/alexherrero/sherwood/internal/money"
)

// decimalText is the only form a decimal may take in the strategy file: an
// optional minus sign, digits, and an optional fraction. Exponents, NaN,
// infinities and hexadecimal are refused.
var decimalText = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// Decimal is an exact decimal number read from the strategy file, kept as the
// text that was written. It never passes through a float.
//
// It holds the file's ratios: shares of the portfolio, distances, margins and
// tolerances. Dollar amounts are read as money.Money instead.
type Decimal struct {
	text string
}

// ParseDecimal reads a decimal from text such as "0.10" or "250".
func ParseDecimal(s string) (Decimal, error) {
	if !decimalText.MatchString(s) {
		return Decimal{}, fmt.Errorf("%q is not a plain decimal number", s)
	}
	return Decimal{text: s}, nil
}

// MustDecimal is ParseDecimal for constants in code and tests; it panics on
// bad input.
func MustDecimal(s string) Decimal {
	d, err := ParseDecimal(s)
	if err != nil {
		panic(err)
	}
	return d
}

// String returns the decimal as it was written in the file.
func (d Decimal) String() string { return d.text }

// IsZero reports whether d is the zero Decimal, meaning it was never set.
func (d Decimal) IsZero() bool { return d.text == "" }

// Rat returns the decimal as an exact rational number. The result is a new
// value the caller may change.
func (d Decimal) Rat() *big.Rat {
	r, ok := new(big.Rat).SetString(d.text)
	if !ok {
		return new(big.Rat)
	}
	return r
}

// Places returns the number of digits after the decimal point as written.
func (d Decimal) Places() int {
	if i := strings.IndexByte(d.text, '.'); i >= 0 {
		return len(d.text) - i - 1
	}
	return 0
}

// UnmarshalYAML reads a decimal from a YAML scalar, quoted or not, using the
// scalar's text so that 0.10 stays exactly 0.10.
func (d *Decimal) UnmarshalYAML(node *yaml.Node) error {
	text, err := scalarText(node)
	if err != nil {
		return err
	}
	if !decimalText.MatchString(text) {
		return fmt.Errorf("line %d: expected a plain decimal number such as 0.05 or 250", node.Line)
	}
	d.text = text
	return nil
}

// amountText is a dollar amount read from the strategy file: the exact
// money.Money and the text it was written as, so problems can quote the file.
type amountText struct {
	m    money.Money
	text string
}

// UnmarshalYAML reads an amount from a YAML scalar, quoted or not, parsing
// the scalar's text with money.ParseMoney so that it never passes through a
// float.
func (a *amountText) UnmarshalYAML(node *yaml.Node) error {
	text, err := scalarText(node)
	if err != nil {
		return err
	}
	m, err := money.ParseMoney(text)
	if err != nil {
		return fmt.Errorf("line %d: expected a plain decimal number such as 100.00", node.Line)
	}
	a.m, a.text = m, text
	return nil
}

// Scalar is one rule setting as written in the strategy file. The rule
// library (a later part) reads each rule type's settings from this text with
// its own schema; keeping the text means numbers never become floats here.
type Scalar struct {
	text string
}

// NewScalar returns a Scalar holding s, for tests and code that builds a
// strategy in memory.
func NewScalar(s string) Scalar { return Scalar{text: s} }

// String returns the setting's text as written.
func (s Scalar) String() string { return s.text }

// UnmarshalYAML reads a setting from a YAML scalar. Lists and mappings are
// refused.
func (s *Scalar) UnmarshalYAML(node *yaml.Node) error {
	text, err := scalarText(node)
	if err != nil {
		return err
	}
	s.text = text
	return nil
}

// wholeText is the only form a whole number may take in the strategy file.
var wholeText = regexp.MustCompile(`^-?[0-9]{1,9}$`)

// wholeNumber reads a whole number from a YAML scalar. The YAML library would
// otherwise truncate 3.5 to 3 when decoding into an int, quietly changing a
// limit.
type wholeNumber int

// UnmarshalYAML refuses anything but plain digits.
func (w *wholeNumber) UnmarshalYAML(node *yaml.Node) error {
	text, err := scalarText(node)
	if err != nil {
		return err
	}
	if !wholeText.MatchString(text) {
		return fmt.Errorf("line %d: expected a whole number such as 3", node.Line)
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return fmt.Errorf("line %d: expected a whole number such as 3", node.Line)
	}
	*w = wholeNumber(n)
	return nil
}
