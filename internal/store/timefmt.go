package store

import (
	"fmt"
	"time"
)

// The database keeps times and dates as text in these fixed layouts. Every
// time is UTC with nine fractional digits, so the text is always the same
// width and sorting it as text sorts it in time order.
const (
	// TimeLayout is the layout of every stored instant.
	TimeLayout = "2006-01-02T15:04:05.000000000Z"
	// DateLayout is the layout of every stored trading date.
	DateLayout = "2006-01-02"
)

// FormatTime renders t, converted to UTC, in TimeLayout.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

// ParseTime reads a time written by FormatTime.
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(TimeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: parse time %q: %w", s, err)
	}
	return t, nil
}

// FormatDate renders the calendar date of t, in t's own location, in
// DateLayout. Pass a time already in the exchange's location so the date is
// the trading date.
func FormatDate(t time.Time) string { return t.Format(DateLayout) }
