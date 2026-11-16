package clock

import (
	"errors"
	"fmt"
	"time"
)

// Session hours in Eastern time. A half day closes early at 1:00 p.m.
const (
	openHour, openMinute = 9, 30
	regularCloseHour     = 16
	halfDayCloseHour     = 13
)

// DateLayout is how a trading date is written in the holiday table and in
// error messages.
const DateLayout = "2006-01-02"

// ErrNotCovered means the date falls in a year the holiday table does not
// cover. The calendar never guesses: a caller must stop rather than treat an
// unknown year as all regular sessions.
var ErrNotCovered = errors.New("clock: date outside the exchange calendar")

// ErrClosed means the exchange has no session on the date: a weekend or a
// full-day holiday.
var ErrClosed = errors.New("clock: exchange closed")

// Day is one entry in a holiday table: a weekday the exchange is closed all
// day, or, when HalfDay is set, a day it closes early.
type Day struct {
	Date    string // YYYY-MM-DD
	Name    string
	HalfDay bool
}

// Session is one trading date's session.
type Session struct {
	Date    time.Time // midnight Eastern at the start of the date
	Open    time.Time
	Close   time.Time
	HalfDay bool
	Name    string // the half day's name; empty on a regular day
}

type civil struct {
	y int
	m time.Month
	d int
}

func civilOf(t time.Time) civil {
	y, m, d := t.In(Eastern).Date()
	return civil{y, m, d}
}

func (c civil) at(hour, minute int) time.Time {
	return time.Date(c.y, c.m, c.d, hour, minute, 0, 0, Eastern)
}

func (c civil) String() string { return c.at(0, 0).Format(DateLayout) }

// Calendar answers which dates are trading days and when their sessions run,
// in Eastern time, for the years its table covers. A Calendar is immutable and
// safe for concurrent use. Every method reads the Eastern calendar date of the
// time it is given, so the time's own location does not matter.
type Calendar struct {
	first, last int
	days        map[civil]Day
}

// NewCalendar builds a calendar covering the years first to last inclusive
// from a table of closures and half days. It refuses a table with a bad or
// duplicate date, an entry on a weekend or outside the years, or one with no
// name. Most callers want NYSE; tests may build their own.
func NewCalendar(first, last int, table []Day) (*Calendar, error) {
	if first > last {
		return nil, fmt.Errorf("clock: calendar years %d to %d are backwards", first, last)
	}
	c := &Calendar{first: first, last: last, days: make(map[civil]Day, len(table))}
	for _, e := range table {
		t, err := time.ParseInLocation(DateLayout, e.Date, Eastern)
		if err != nil {
			return nil, fmt.Errorf("clock: holiday table: bad date %q: %w", e.Date, err)
		}
		k := civilOf(t)
		switch {
		case e.Name == "":
			return nil, fmt.Errorf("clock: holiday table: %s has no name", e.Date)
		case k.y < first || k.y > last:
			return nil, fmt.Errorf("clock: holiday table: %s is outside %d to %d", e.Date, first, last)
		case isWeekend(t):
			return nil, fmt.Errorf("clock: holiday table: %s is a %s", e.Date, t.Weekday())
		}
		if _, dup := c.days[k]; dup {
			return nil, fmt.Errorf("clock: holiday table: %s is listed twice", e.Date)
		}
		c.days[k] = e
	}
	return c, nil
}

func isWeekend(t time.Time) bool {
	wd := t.Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// Years returns the first and last years the calendar covers.
func (c *Calendar) Years() (first, last int) { return c.first, c.last }

// Covers reports whether t's Eastern date falls in a covered year.
func (c *Calendar) Covers(t time.Time) bool {
	return c.coversYear(civilOf(t).y)
}

func (c *Calendar) coversYear(y int) bool { return y >= c.first && y <= c.last }

// day classifies a covered date. reason is set when the exchange is closed.
func (c *Calendar) day(k civil) (e Day, listed bool, reason string, err error) {
	if !c.coversYear(k.y) {
		return Day{}, false, "", fmt.Errorf("%w: %s (covers %d to %d)", ErrNotCovered, k, c.first, c.last)
	}
	date := k.at(0, 0)
	if isWeekend(date) {
		return Day{}, false, date.Weekday().String(), nil
	}
	e, listed = c.days[k]
	if listed && !e.HalfDay {
		return e, true, e.Name, nil
	}
	return e, listed, "", nil
}

// IsTradingDay reports whether the exchange has a session, full or half, on
// t's Eastern date.
func (c *Calendar) IsTradingDay(t time.Time) (bool, error) {
	_, _, reason, err := c.day(civilOf(t))
	if err != nil {
		return false, err
	}
	return reason == "", nil
}

// IsHalfDay reports whether t's Eastern date is an early-close half day.
func (c *Calendar) IsHalfDay(t time.Time) (bool, error) {
	e, listed, _, err := c.day(civilOf(t))
	if err != nil {
		return false, err
	}
	return listed && e.HalfDay, nil
}

// Session returns the session on t's Eastern date: open at 9:30 a.m. and
// close at 4:00 p.m., or 1:00 p.m. on a half day. It returns an error wrapping
// ErrClosed on a weekend or holiday, naming which.
func (c *Calendar) Session(t time.Time) (Session, error) {
	k := civilOf(t)
	e, listed, reason, err := c.day(k)
	if err != nil {
		return Session{}, err
	}
	if reason != "" {
		return Session{}, fmt.Errorf("%w on %s: %s", ErrClosed, k, reason)
	}
	s := Session{Date: k.at(0, 0), Open: k.at(openHour, openMinute), Close: k.at(regularCloseHour, 0)}
	if listed && e.HalfDay {
		s.Close = k.at(halfDayCloseHour, 0)
		s.HalfDay = true
		s.Name = e.Name
	}
	return s, nil
}

// NextTradingDay returns midnight Eastern at the start of the first trading
// date after t's Eastern date. It fails with ErrNotCovered rather than step
// into a year the table does not cover.
func (c *Calendar) NextTradingDay(t time.Time) (time.Time, error) {
	return c.step(t, 1)
}

// PreviousTradingDay returns midnight Eastern at the start of the last trading
// date before t's Eastern date. It fails with ErrNotCovered rather than step
// into a year the table does not cover.
func (c *Calendar) PreviousTradingDay(t time.Time) (time.Time, error) {
	return c.step(t, -1)
}

func (c *Calendar) step(t time.Time, dir int) (time.Time, error) {
	k := civilOf(t)
	// The longest run of closed days is a few days; a year is a safe bound.
	for range 366 {
		// time.Date normalises the day overflow into the next month or year.
		k = civilOf(time.Date(k.y, k.m, k.d+dir, 12, 0, 0, 0, Eastern))
		_, _, reason, err := c.day(k)
		if err != nil {
			return time.Time{}, err
		}
		if reason == "" {
			return k.at(0, 0), nil
		}
	}
	return time.Time{}, fmt.Errorf("clock: no trading day within a year of %s", civilOf(t))
}

// CheckCoverage is the start-up check on the holiday table. It fails when
// now's Eastern year is not covered, and from 1 December when next year is
// not covered either, so the yearly update is due before it is needed.
// Start-up passes the real clock's time; tests pass fixed times.
func (c *Calendar) CheckCoverage(now time.Time) error {
	k := civilOf(now)
	if !c.coversYear(k.y) {
		return fmt.Errorf("%w: the holiday table covers %d to %d but it is now %d; add this year's NYSE holidays", ErrNotCovered, c.first, c.last, k.y)
	}
	if k.m == time.December && !c.coversYear(k.y+1) {
		return fmt.Errorf("%w: the holiday table ends with %d; add %d's NYSE holidays and half days before the new year", ErrNotCovered, c.last, k.y+1)
	}
	return nil
}
