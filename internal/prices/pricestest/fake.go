// Package pricestest gives tests a fake price source that never touches the
// network.
package pricestest

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/money"
	"github.com/alexherrero/sherwood/internal/prices"
)

// Source is a fake prices.Source holding bars set by the test. It is safe for
// concurrent use.
type Source struct {
	name string

	mu    sync.Mutex
	bars  map[string][]prices.Bar
	fails map[string]error
	calls []Call
}

// Call records one DailyBars request.
type Call struct {
	Symbol   string
	From, To string
}

// New returns an empty fake source with the given name.
func New(name string) *Source {
	return &Source{name: name, bars: map[string][]prices.Bar{}, fails: map[string]error{}}
}

// Name returns the name given to New.
func (s *Source) Name() string { return s.name }

// Add stores bars, replacing any bar already held for the same stock and date.
func (s *Source) Add(bars ...prices.Bar) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range bars {
		list := slices.DeleteFunc(s.bars[b.Symbol], func(o prices.Bar) bool { return o.Date.Equal(b.Date) })
		list = append(list, b)
		slices.SortFunc(list, func(a, b prices.Bar) int { return a.Date.Compare(b.Date) })
		s.bars[b.Symbol] = list
	}
}

// Fail makes every request for symbol return err.
func (s *Source) Fail(symbol string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fails[symbol] = err
}

// Calls returns the requests made so far.
func (s *Source) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// DailyBars returns the stored bars for symbol from from to to, inclusive. It
// returns prices.ErrNoData when there are none.
func (s *Source) DailyBars(ctx context.Context, symbol string, from, to time.Time) ([]prices.Bar, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	from, to = clock.DateOf(from), clock.DateOf(to)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{Symbol: symbol, From: from.Format(clock.DateLayout), To: to.Format(clock.DateLayout)})
	if err := s.fails[symbol]; err != nil {
		return nil, err
	}
	var out []prices.Bar
	for _, b := range s.bars[symbol] {
		if !b.Date.Before(from) && !b.Date.After(to) {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: fake %s has no bars for %s", prices.ErrNoData, s.name, symbol)
	}
	return out, nil
}

// Bar builds a bar from text, for tables of made-up prices. date is
// YYYY-MM-DD; it panics on bad input.
func Bar(symbol, date, open, high, low, closing string, volume int64) prices.Bar {
	d, err := time.ParseInLocation(clock.DateLayout, date, clock.Eastern)
	if err != nil {
		panic(err)
	}
	return prices.Bar{
		Symbol: symbol,
		Date:   d,
		Open:   money.MustParsePrice(open),
		High:   money.MustParsePrice(high),
		Low:    money.MustParsePrice(low),
		Close:  money.MustParsePrice(closing),
		Volume: volume,
	}
}
