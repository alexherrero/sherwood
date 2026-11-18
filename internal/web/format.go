package web

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/money"
)

// The views format every figure from the money types' exact decimal text.
// Nothing here goes through a float, so what the page shows is what is
// stored, rounded only where it says so.

// minus is the sign put before a negative amount: the real minus sign, which
// screen readers say as "minus".
const minus = "−"

// dollars writes an amount exactly as stored, with a dollar sign and thousands
// separators, such as "$1,234.5678" or "−$12.30". It does not round.
func dollars(m money.Money) string {
	return withSeparators(m.String(), "$")
}

// cents writes an amount rounded to the cent, halves away from zero, such as
// "$1,234.57".
func cents(m money.Money) string {
	return dollars(m.RoundCents())
}

// withSeparators turns plain decimal text such as "-1234.50" into
// "−<prefix>1,234.50".
func withSeparators(text, prefix string) string {
	sign := ""
	if rest, ok := strings.CutPrefix(text, "-"); ok {
		sign, text = minus, rest
	}
	whole, frac, hasFrac := strings.Cut(text, ".")
	var b strings.Builder
	b.WriteString(sign)
	b.WriteString(prefix)
	for i, d := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	if hasFrac {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	return b.String()
}

// delta is a gain or loss as the page shows it: the words carry the sign and
// the direction, and the class only reinforces them with color and border.
type delta struct {
	Text  string
	Class string
}

var hundred = decimal.NewFromInt(100)

// gainOf describes value minus cost, rounded to the cent, with its share of
// the cost to one decimal place when the cost is above zero. For example
// "+$42.10, up 3.4%", "−$5.00, down 1.2%" or "$0.00, no change".
func gainOf(value, cost money.Money) delta {
	g := value.Sub(cost).RoundCents()
	pct := ""
	if cost.IsPositive() {
		p := g.Decimal().Abs().Div(cost.Decimal()).Mul(hundred).Round(1)
		pct = " " + p.StringFixed(1) + "%"
	}
	switch {
	case g.IsPositive():
		return delta{Text: "+" + cents(g) + ", up" + pct, Class: "delta--up"}
	case g.IsNegative():
		return delta{Text: cents(g) + ", down" + pct, Class: "delta--down"}
	default:
		return delta{Text: cents(g) + ", no change", Class: "delta--flat"}
	}
}

// day writes a date as "Mon 16 Nov 2026", in US Eastern.
func day(t time.Time) string {
	return t.In(clock.Eastern).Format("Mon 2 Jan 2006")
}

// when writes an instant as "Mon 16 Nov 2026, 4:31 pm ET", in US Eastern,
// where the market and every job's schedule live.
func when(t time.Time) string {
	return t.In(clock.Eastern).Format("Mon 2 Jan 2006, 3:04 pm") + " ET"
}
