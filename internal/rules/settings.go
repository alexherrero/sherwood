package rules

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/money"
)

// SettingType is the kind of value a rule setting holds.
type SettingType string

// The setting types.
const (
	// WholeNumber is a count such as a number of days or a rank, written as
	// digits only, between the setting's Min and Max.
	WholeNumber SettingType = "whole number"
	// Fraction is a share between 0 and 1, both excluded, written as a plain
	// decimal such as 0.05 (5%).
	Fraction SettingType = "fraction"
	// Amount is a dollar amount above 0 with at most two decimal places,
	// written as a plain decimal such as 250 or 99.50.
	Amount SettingType = "dollar amount"
)

// Setting describes one setting of a rule type: its name in the strategy
// file, its type and its range. The reference page is built from these.
type Setting struct {
	Name string
	Type SettingType
	// Min and Max bound a WholeNumber setting, both included.
	Min, Max int
	// Doc says in a few words what the setting does.
	Doc string
}

// Range describes the values the setting accepts.
func (s Setting) Range() string {
	switch s.Type {
	case WholeNumber:
		return fmt.Sprintf("a whole number from %d to %d", s.Min, s.Max)
	case Fraction:
		return "a fraction above 0 and below 1, such as 0.05 for 5%"
	case Amount:
		return "a dollar amount above 0 with at most two decimal places"
	}
	return string(s.Type)
}

// ErrSettings is wrapped by every SettingsError.
var ErrSettings = errors.New("rules: invalid settings")

// SettingsError lists every problem with one rule entry's settings.
type SettingsError struct {
	// Type is the rule type whose settings are wrong.
	Type string
	// Problems holds one plain sentence per problem, each naming its setting.
	Problems []string
}

// Error joins the problems into one message.
func (e *SettingsError) Error() string {
	return fmt.Sprintf("%s settings are invalid: %s", e.Type, strings.Join(e.Problems, "; "))
}

// Unwrap makes errors.Is(err, ErrSettings) true.
func (e *SettingsError) Unwrap() error { return ErrSettings }

// The accepted texts. Exponents, signs, spaces, NaN and infinities are all
// refused before anything is parsed.
var (
	wholeText    = regexp.MustCompile(`^[0-9]{1,6}$`)
	fractionText = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
)

// maxText bounds the length of a decimal setting's text.
const maxText = 64

// values holds a rule entry's settings once each has been parsed.
type values struct {
	whole    map[string]int
	fraction map[string]decimal.Decimal
	amount   map[string]money.Money
	problems []string
}

func (v *values) addf(format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf(format, args...))
}

// err returns the problems found for ruleType, or nil.
func (v *values) err(ruleType string) error {
	if len(v.problems) == 0 {
		return nil
	}
	return &SettingsError{Type: ruleType, Problems: v.problems}
}

// parseSettings reads every setting in specs from in. Unknown names are
// refused, every setting is required, and every value must be in range. The
// problems are listed in a fixed order: unknown names sorted, then the
// settings in the order specs gives.
func parseSettings(specs []Setting, in map[string]config.Scalar) *values {
	v := &values{
		whole:    map[string]int{},
		fraction: map[string]decimal.Decimal{},
		amount:   map[string]money.Money{},
	}
	known := make([]string, len(specs))
	for i, s := range specs {
		known[i] = s.Name
	}
	var unknown []string
	for name := range in {
		if !slices.Contains(known, name) {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		v.addf("unknown setting %q (known settings: %s)", name, strings.Join(known, ", "))
	}
	for _, s := range specs {
		sc, ok := in[s.Name]
		if !ok {
			v.addf("%s is missing; it must be %s", s.Name, s.Range())
			continue
		}
		text := sc.String()
		bad := func() { v.addf("%s is %q, but it must be %s", s.Name, text, s.Range()) }
		switch s.Type {
		case WholeNumber:
			if !wholeText.MatchString(text) {
				bad()
				continue
			}
			n, err := strconv.Atoi(text)
			if err != nil || n < s.Min || n > s.Max {
				bad()
				continue
			}
			v.whole[s.Name] = n
		case Fraction:
			if len(text) > maxText || !fractionText.MatchString(text) {
				bad()
				continue
			}
			d, err := decimal.NewFromString(text)
			if err != nil || !d.IsPositive() || d.Cmp(decimal.NewFromInt(1)) >= 0 {
				bad()
				continue
			}
			v.fraction[s.Name] = d
		case Amount:
			m, err := money.ParseMoney(text)
			if err != nil || !m.IsPositive() || !m.IsCents() {
				bad()
				continue
			}
			v.amount[s.Name] = m
		}
	}
	return v
}

// percent writes a fraction as a percentage with no trailing zeros: 0.05
// gives "5%", 0.125 gives "12.5%".
func percent(f decimal.Decimal) string {
	return f.Shift(2).String() + "%"
}
