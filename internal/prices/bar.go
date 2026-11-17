package prices

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/money"
)

// Errors a Source may return. Callers treat any error from a source as a
// missing bar; these let a test or a log line say which kind it was.
var (
	// ErrNoData means the source has no bars for the stock in the range, or
	// does not know the symbol.
	ErrNoData = errors.New("prices: no data from source")
	// ErrBadResponse means the source answered with something that is not the
	// response shape this package reads.
	ErrBadResponse = errors.New("prices: unexpected response from source")
)

// Bar is one stock's prices for one trading date.
type Bar struct {
	Symbol string
	// Date is midnight Eastern at the start of the trading date.
	Date                   time.Time
	Open, High, Low, Close money.Price
	// Volume is the number of shares traded, split-adjusted like the prices.
	Volume int64
}

// DateString is the bar's trading date as YYYY-MM-DD.
func (b Bar) DateString() string { return b.Date.In(clock.Eastern).Format(clock.DateLayout) }

// Check reports whether the bar is internally consistent: the high is the
// highest price, the low the lowest, and the volume is not negative.
func (b Bar) Check() error {
	switch {
	case b.High.Cmp(b.Low) < 0:
		return fmt.Errorf("%w: %s %s high %s is below low %s", ErrBadResponse, b.Symbol, b.DateString(), b.High, b.Low)
	case b.High.Cmp(b.Open) < 0 || b.High.Cmp(b.Close) < 0:
		return fmt.Errorf("%w: %s %s high %s is below open or close", ErrBadResponse, b.Symbol, b.DateString(), b.High)
	case b.Low.Cmp(b.Open) > 0 || b.Low.Cmp(b.Close) > 0:
		return fmt.Errorf("%w: %s %s low %s is above open or close", ErrBadResponse, b.Symbol, b.DateString(), b.Low)
	case b.Volume < 0:
		return fmt.Errorf("%w: %s %s volume is negative", ErrBadResponse, b.Symbol, b.DateString())
	}
	return nil
}

// Source is one end-of-day price source.
type Source interface {
	// Name is the source's short name, lower-case letters, digits and
	// underscores, as stored with each bar.
	Name() string
	// DailyBars returns the stock's bars for every trading date the source
	// has from from to to, both inclusive, oldest first. Prices are
	// split-adjusted as of the last bar returned. from and to are read as
	// Eastern dates.
	DailyBars(ctx context.Context, symbol string, from, to time.Time) ([]Bar, error)
}

// symbolText is the accepted form of a stock symbol, the same rule the
// strategy file uses: capital letters, digits, and a dot or dash for a share
// class.
var symbolText = regexp.MustCompile(`^[A-Z][A-Z0-9]*([.-][A-Z0-9]+)?$`)

const maxSymbolLen = 10

// CheckSymbol reports whether s is a well-formed stock symbol.
func CheckSymbol(s string) error {
	if len(s) > maxSymbolLen || !symbolText.MatchString(s) {
		return fmt.Errorf("prices: %q is not a stock symbol", s)
	}
	return nil
}

// sourceTicker is the symbol as the sources write it: lower case, with a
// share-class dot written as a dash (BRK.B becomes brk-b).
func sourceTicker(symbol string) string {
	return strings.ToLower(strings.ReplaceAll(symbol, ".", "-"))
}

// plainDecimal is the only number text accepted from a source: digits and an
// optional fraction. No sign, exponent, NaN or infinity.
var plainDecimal = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

const maxNumberLen = 32

// parseNumber reads a non-negative decimal from a source's text.
func parseNumber(field, s string) (decimal.Decimal, error) {
	s = strings.TrimSpace(s)
	if len(s) > maxNumberLen || !plainDecimal.MatchString(s) {
		return decimal.Decimal{}, fmt.Errorf("%w: %s is not a plain decimal", ErrBadResponse, field)
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%w: %s is not a plain decimal", ErrBadResponse, field)
	}
	return d, nil
}

// adjustDivisions is the number of decimal places kept when dividing a price
// by a split factor, before rounding to the cent.
const adjustDivisions = 10

// toPrice divides a source's price by the split factor still to come and
// rounds it to the cent, halves away from zero, as money.RoundPrice does.
func toPrice(field string, d, factor decimal.Decimal) (money.Price, error) {
	if !factor.Equal(decimal.NewFromInt(1)) {
		d = d.DivRound(factor, adjustDivisions)
	}
	p, err := money.RoundPrice(d)
	if err != nil {
		return money.Price{}, fmt.Errorf("%w: %s: %w", ErrBadResponse, field, err)
	}
	return p, nil
}

// toVolume multiplies a source's volume by the split factor still to come and
// rounds down to a whole number of shares.
func toVolume(d, factor decimal.Decimal) (int64, error) {
	v := d.Mul(factor).Floor()
	if !v.IsInteger() || v.Cmp(decimal.NewFromInt(1<<62)) > 0 {
		return 0, fmt.Errorf("%w: volume is out of range", ErrBadResponse)
	}
	return v.IntPart(), nil
}

// parseDate reads a YYYY-MM-DD trading date as midnight Eastern.
func parseDate(s string) (time.Time, error) {
	t, err := time.ParseInLocation(clock.DateLayout, s, clock.Eastern)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: date %q", ErrBadResponse, s)
	}
	return t, nil
}

// rawBar is one bar as a source wrote it, before split adjustment.
type rawBar struct {
	date                   time.Time
	open, high, low, close decimal.Decimal
	volume                 decimal.Decimal
	// split is the split factor that took effect on this date: 4 for a
	// four-for-one split, 0.1 for a one-for-ten reverse split, 1 otherwise.
	split decimal.Decimal
}

// splitAdjust turns raw bars, oldest first, into bars split-adjusted as of the
// last one: each bar's prices are divided, and its volume multiplied, by the
// product of the split factors of every later bar.
func splitAdjust(symbol string, raw []rawBar) ([]Bar, error) {
	bars := make([]Bar, len(raw))
	factor := decimal.NewFromInt(1)
	for i := len(raw) - 1; i >= 0; i-- {
		r := raw[i]
		b := Bar{Symbol: symbol, Date: r.date}
		var err error
		if b.Open, err = toPrice("open", r.open, factor); err != nil {
			return nil, err
		}
		if b.High, err = toPrice("high", r.high, factor); err != nil {
			return nil, err
		}
		if b.Low, err = toPrice("low", r.low, factor); err != nil {
			return nil, err
		}
		if b.Close, err = toPrice("close", r.close, factor); err != nil {
			return nil, err
		}
		if b.Volume, err = toVolume(r.volume, factor); err != nil {
			return nil, err
		}
		bars[i] = b
		// Bars before this one are on the basis before this date's split.
		factor = factor.Mul(r.split)
	}
	return bars, nil
}
