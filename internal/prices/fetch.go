package prices

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/store"
)

// ErrTooEarly means prices were asked for before that date's session closed.
var ErrTooEarly = errors.New("prices: the session has not closed yet")

// Outcome is one stock's cross-check result on one trading date.
type Outcome string

// The three outcomes, as stored in price_checks.outcome.
const (
	// Agreed: both sources gave the date's bar and they agree within the
	// tolerance.
	Agreed Outcome = "agreed"
	// Disagreed: they differ by more than the tolerance. The stock gets no
	// signals that day, and a held one is flagged for a halt.
	Disagreed Outcome = "disagreed"
	// Missing: a source gave no usable bar for the date. The whole day is
	// then not usable.
	Missing Outcome = "missing"
)

// Check is one stock's cross-check on one trading date.
type Check struct {
	Symbol  string
	Held    bool
	Outcome Outcome
	// Halt is set exactly when a held stock's sources disagreed.
	Halt bool
	// Detail says what differed or what was missing; empty when agreed.
	Detail string
}

// Day is the cross-check of one trading date.
type Day struct {
	// Date is midnight Eastern at the start of the trading date.
	Date          time.Time
	PrimarySource string
	CheckSource   string
	// Tolerance is the strategy file's price_cross_check_tolerance, as
	// written.
	Tolerance string
	// Usable is true only when both sources gave the date's bar for every
	// stock checked.
	Usable bool
	// Detail says why the day is not usable; empty when it is.
	Detail string
	// Checks holds one entry per stock, sorted by symbol.
	Checks []Check
}

// DateString is the trading date as YYYY-MM-DD.
func (d *Day) DateString() string { return d.Date.In(clock.Eastern).Format(clock.DateLayout) }

// Halt reports whether any held stock's sources disagreed, which must halt
// trading for review.
func (d *Day) Halt() bool { return len(d.HaltSymbols()) > 0 }

// HaltSymbols lists the held stocks whose sources disagreed, sorted.
func (d *Day) HaltSymbols() []string {
	var out []string
	for _, c := range d.Checks {
		if c.Halt {
			out = append(out, c.Symbol)
		}
	}
	return out
}

// Excluded reports whether the stock may get no signals on this date: the day
// is not usable, the stock was not checked, or its sources did not agree.
func (d *Day) Excluded(symbol string) bool {
	if !d.Usable {
		return true
	}
	for _, c := range d.Checks {
		if c.Symbol == symbol {
			return c.Outcome != Agreed
		}
	}
	return true
}

// Fetcher fetches and cross-checks one trading date's bars and records them.
type Fetcher struct {
	// Primary's bars are the ones the rules use.
	Primary Source
	// Check is compared with Primary on the trading date's bar only.
	Check    Source
	Clock    clock.Clock
	Calendar *clock.Calendar
	Store    *store.Store
	Log      *slog.Logger
	// HistoryDays is how many calendar days before the trading date to fetch
	// from the primary source as well, so the stored history is split-adjusted
	// as of the trading date. Zero fetches only the date itself. It costs no
	// extra requests: each stock is one request to each source either way.
	HistoryDays int
}

// Stocks names the stocks to check on one date.
type Stocks struct {
	// Held are the stocks held that day. A held stock whose sources disagree
	// is flagged for a halt.
	Held []string
	// Watched are the other stocks the rules look at: the watchlist and each
	// strategy's stocks. A stock in both lists counts as held.
	Watched []string
}

// FetchDay fetches date's bars for every held and watched stock from both
// sources, cross-checks them, records the bars and the outcome, and returns
// the outcome. A source failing is recorded, not returned: it makes the stock
// missing and the day not usable. FetchDay returns an error only when date is
// not a closed trading session, the stock list is malformed, the context
// ends, or the database write fails; then nothing is recorded.
//
// Running FetchDay again for the same date replaces that date's outcome.
func (f *Fetcher) FetchDay(ctx context.Context, date time.Time, stocks Stocks, tolerance config.Decimal) (*Day, error) {
	date = clock.DateOf(date)
	session, err := f.Calendar.Session(date)
	if err != nil {
		return nil, fmt.Errorf("prices: %s: %w", date.Format(clock.DateLayout), err)
	}
	now := f.Clock.Now()
	if now.Before(session.Close) {
		return nil, fmt.Errorf("%w: %s closes at %s", ErrTooEarly, date.Format(clock.DateLayout), session.Close.Format(time.Kitchen))
	}
	tol, err := toleranceOf(tolerance)
	if err != nil {
		return nil, err
	}
	held, symbols, err := stockList(stocks)
	if err != nil {
		return nil, err
	}
	pName, cName := f.Primary.Name(), f.Check.Name()
	if pName == cName {
		return nil, fmt.Errorf("prices: the primary and cross-check sources are both %q", pName)
	}

	day := &Day{Date: date, PrimarySource: pName, CheckSource: cName, Tolerance: tolerance.String(), Usable: true}
	var keep []storedBar
	var missing []string
	for _, sym := range symbols {
		pBars, pBar, pErr := f.fetch(ctx, f.Primary, sym, date.AddDate(0, 0, -f.HistoryDays), date)
		_, cBar, cErr := f.fetch(ctx, f.Check, sym, date, date)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, b := range pBars {
			keep = append(keep, storedBar{source: pName, Bar: b})
		}
		if cBar != nil {
			keep = append(keep, storedBar{source: cName, Bar: *cBar})
		}
		c := Check{Symbol: sym, Held: held[sym]}
		switch {
		case pErr != nil || cErr != nil:
			c.Outcome = Missing
			var parts []string
			if pErr != nil {
				parts = append(parts, pName+": "+pErr.Error())
			}
			if cErr != nil {
				parts = append(parts, cName+": "+cErr.Error())
			}
			c.Detail = strings.Join(parts, "; ")
			missing = append(missing, sym)
		default:
			if diff := compare(*pBar, *cBar, tol, pName, cName); diff != "" {
				c.Outcome = Disagreed
				c.Detail = diff
				c.Halt = c.Held
			} else {
				c.Outcome = Agreed
			}
		}
		day.Checks = append(day.Checks, c)
	}
	if len(missing) > 0 {
		day.Usable = false
		day.Detail = "no bar for " + strings.Join(missing, ", ") + " from at least one source"
	}

	if err := writeDay(ctx, f.Store, day, keep, now); err != nil {
		return nil, err
	}
	f.logDay(ctx, day)
	return day, nil
}

// fetch asks one source for one stock's bars and checks what came back. It
// returns the bars to store and the trading date's own bar. Any problem is an
// error: the stock is then missing from that source.
func (f *Fetcher) fetch(ctx context.Context, src Source, symbol string, from, date time.Time) ([]Bar, *Bar, error) {
	bars, err := src.DailyBars(ctx, symbol, from, date)
	if err != nil {
		return nil, nil, err
	}
	if err := f.checkBars(symbol, bars, from, date); err != nil {
		return nil, nil, err
	}
	for i := range bars {
		if bars[i].Date.Equal(date) {
			return bars, &bars[i], nil
		}
	}
	// The history is still worth keeping; the date's bar is what is missing.
	return bars, nil, fmt.Errorf("%w: no bar for %s", ErrNoData, date.Format(clock.DateLayout))
}

// checkBars refuses a response that breaks the Source contract: a bar for
// another stock, outside the range, on a day the exchange was closed, out of
// order, or inconsistent in itself.
func (f *Fetcher) checkBars(symbol string, bars []Bar, from, to time.Time) error {
	from, to = clock.DateOf(from), clock.DateOf(to)
	for i, b := range bars {
		d := clock.DateOf(b.Date)
		switch {
		case b.Symbol != symbol:
			return fmt.Errorf("%w: a bar for %q in the answer for %s", ErrBadResponse, b.Symbol, symbol)
		case !b.Date.Equal(d):
			return fmt.Errorf("%w: bar %d is not dated at midnight Eastern", ErrBadResponse, i)
		case d.Before(from) || d.After(to):
			return fmt.Errorf("%w: bar dated %s is outside the range asked for", ErrBadResponse, b.DateString())
		case i > 0 && !d.After(bars[i-1].Date):
			return fmt.Errorf("%w: bars are not in date order", ErrBadResponse)
		}
		open, err := f.Calendar.IsTradingDay(d)
		if err != nil {
			return fmt.Errorf("prices: bar dated %s: %w", b.DateString(), err)
		}
		if !open {
			return fmt.Errorf("%w: bar dated %s, when the exchange was closed", ErrBadResponse, b.DateString())
		}
		if err := b.Check(); err != nil {
			return err
		}
	}
	return nil
}

// compare returns what differs between the two sources' bars by more than
// the tolerance, as a share of the primary's value, or "" if nothing does.
// Volume is not compared: sources count it differently.
func compare(p, c Bar, tol decimal.Decimal, pName, cName string) string {
	type field struct {
		name string
		p, c decimal.Decimal
	}
	fields := []field{
		{"open", p.Open.Decimal(), c.Open.Decimal()},
		{"high", p.High.Decimal(), c.High.Decimal()},
		{"low", p.Low.Decimal(), c.Low.Decimal()},
		{"close", p.Close.Decimal(), c.Close.Decimal()},
	}
	var diffs []string
	for _, fl := range fields {
		// |p - c| > tol × p, kept as a product so nothing is divided.
		if fl.p.Sub(fl.c).Abs().Cmp(tol.Mul(fl.p)) > 0 {
			diffs = append(diffs, fmt.Sprintf("%s %s %s, %s %s", fl.name, pName, fl.p.StringFixed(2), cName, fl.c.StringFixed(2)))
		}
	}
	if len(diffs) == 0 {
		return ""
	}
	return "sources differ by more than " + tol.String() + ": " + strings.Join(diffs, "; ")
}

func toleranceOf(t config.Decimal) (decimal.Decimal, error) {
	if t.IsZero() {
		return decimal.Decimal{}, errors.New("prices: price_cross_check_tolerance is not set")
	}
	d, err := decimal.NewFromString(t.String())
	if err != nil || d.IsNegative() {
		return decimal.Decimal{}, fmt.Errorf("prices: price_cross_check_tolerance %q is not a decimal of zero or more", t.String())
	}
	return d, nil
}

// stockList merges the held and watched stocks into one sorted list with no
// repeats, and says which are held.
func stockList(s Stocks) (map[string]bool, []string, error) {
	held := make(map[string]bool, len(s.Held))
	seen := make(map[string]bool, len(s.Held)+len(s.Watched))
	var all []string
	for _, list := range [][]string{s.Held, s.Watched} {
		for _, sym := range list {
			if err := CheckSymbol(sym); err != nil {
				return nil, nil, err
			}
			if !seen[sym] {
				seen[sym] = true
				all = append(all, sym)
			}
		}
	}
	for _, sym := range s.Held {
		held[sym] = true
	}
	slices.Sort(all)
	return held, all, nil
}

func (f *Fetcher) logDay(ctx context.Context, d *Day) {
	if f.Log == nil {
		return
	}
	date := d.DateString()
	for _, c := range d.Checks {
		switch c.Outcome {
		case Missing:
			f.Log.WarnContext(ctx, "price bar missing", "trading_date", date, "symbol", c.Symbol, "reason", c.Detail)
		case Disagreed:
			f.Log.WarnContext(ctx, "price sources disagree", "trading_date", date, "symbol", c.Symbol, "halt_cause", haltText(c), "reason", c.Detail)
		}
	}
	if d.Usable {
		f.Log.InfoContext(ctx, "prices checked", "trading_date", date, "price_source", d.PrimarySource)
	} else {
		f.Log.WarnContext(ctx, "prices missing for the day; no signals", "trading_date", date, "reason", d.Detail)
	}
}

func haltText(c Check) string {
	if c.Halt {
		return "held stock's price sources disagree"
	}
	return ""
}
