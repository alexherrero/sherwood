package clock

import (
	"maps"
	"slices"
	"testing"
	"time"
)

// The table must always cover the current year and the next, so it carries
// at least two consecutive years. Start-up's CheckCoverage is the matching
// check against the real date; this one needs no clock.
func TestTableCoversAtLeastTwoYears(t *testing.T) {
	first, last := NYSE().Years()
	if last < first+1 {
		t.Fatalf("the holiday table covers %d to %d; it must hold this year and next", first, last)
	}
	for _, e := range nyseDays {
		if !NYSE().Covers(day(t, e.Date)) {
			t.Errorf("%s is outside the table's years", e.Date)
		}
	}
}

// TestTableFollowsTheExchangeRules derives every covered year's closures
// and half days from the NYSE's standing rules and compares them with the
// table, so a new year typed in with a slip, or only half typed in, fails.
// The published list in calendar_test.go remains the authority; the rules
// catch the next year's mistakes before anyone writes that list.
func TestTableFollowsTheExchangeRules(t *testing.T) {
	first, last := NYSE().Years()
	got := map[string]bool{}
	for _, e := range nyseDays {
		got[e.Date+half(e.HalfDay)] = true
	}
	want := map[string]bool{}
	for y := first; y <= last; y++ {
		for _, d := range ruleDays(y) {
			want[d.Date+half(d.HalfDay)] = true
		}
	}
	for _, k := range slices.Sorted(maps.Keys(want)) {
		if !got[k] {
			t.Errorf("the rules give %s, missing from the table", k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(got)) {
		if !want[k] {
			t.Errorf("the table lists %s, which the rules do not give", k)
		}
	}
}

func half(h bool) string {
	if h {
		return " half day"
	}
	return " closed"
}

// ruleDays applies the NYSE's holiday rules (Rule 7.2) to one year. A holiday
// on a Saturday is observed on the Friday before, and one on a Sunday on the
// Monday after, except New Year's Day on a Saturday, which is not observed.
// The exchange closes at 1:00 p.m. on the day after Thanksgiving, and on 24
// December and 3 July when they fall Monday to Thursday.
func ruleDays(y int) []Day {
	date := func(m time.Month, d int) time.Time { return time.Date(y, m, d, 12, 0, 0, 0, Eastern) }
	nth := func(m time.Month, wd time.Weekday, n int) time.Time {
		d := date(m, 1)
		for d.Weekday() != wd {
			d = d.AddDate(0, 0, 1)
		}
		return d.AddDate(0, 0, 7*(n-1))
	}
	lastMonday := func(m time.Month) time.Time {
		d := date(m+1, 0)
		for d.Weekday() != time.Monday {
			d = d.AddDate(0, 0, -1)
		}
		return d
	}
	observed := func(d time.Time) (time.Time, bool) {
		switch d.Weekday() {
		case time.Saturday:
			return d.AddDate(0, 0, -1), true
		case time.Sunday:
			return d.AddDate(0, 0, 1), true
		}
		return d, true
	}
	var out []Day
	closed := func(d time.Time, ok bool) {
		if ok {
			out = append(out, Day{Date: d.Format(DateLayout)})
		}
	}
	halfDay := func(d time.Time) {
		if wd := d.Weekday(); wd >= time.Monday && wd <= time.Thursday {
			out = append(out, Day{Date: d.Format(DateLayout), HalfDay: true})
		}
	}

	if nyd := date(time.January, 1); nyd.Weekday() == time.Sunday {
		closed(nyd.AddDate(0, 0, 1), true)
	} else if nyd.Weekday() != time.Saturday {
		closed(nyd, true)
	}
	closed(nth(time.January, time.Monday, 3), true)  // Martin Luther King, Jr. Day
	closed(nth(time.February, time.Monday, 3), true) // Washington's Birthday
	closed(easter(y).AddDate(0, 0, -2), true)        // Good Friday
	closed(lastMonday(time.May), true)               // Memorial Day
	closed(observed(date(time.June, 19)))            // Juneteenth
	closed(observed(date(time.July, 4)))             // Independence Day
	closed(nth(time.September, time.Monday, 1), true)
	thanksgiving := nth(time.November, time.Thursday, 4)
	closed(thanksgiving, true)
	closed(observed(date(time.December, 25)))

	halfDay(date(time.July, 3))
	out = append(out, Day{Date: thanksgiving.AddDate(0, 0, 1).Format(DateLayout), HalfDay: true})
	halfDay(date(time.December, 24))
	return out
}

// easter returns Easter Sunday in the Gregorian calendar (the anonymous
// Gregorian algorithm, also called Meeus/Jones/Butcher).
func easter(y int) time.Time {
	a := y % 19
	b, c := y/100, y%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	dd := (h+l-7*m+114)%31 + 1
	return time.Date(y, time.Month(month), dd, 12, 0, 0, 0, Eastern)
}

func TestEaster(t *testing.T) {
	for y, want := range map[int]string{2024: "2024-03-31", 2025: "2025-04-20", 2026: "2026-04-05", 2027: "2027-03-28", 2028: "2028-04-16"} {
		if got := easter(y).Format(DateLayout); got != want {
			t.Errorf("easter(%d) = %s, want %s", y, got, want)
		}
	}
}

// The rules also give 2028's published calendar, which this release lists
// though the table does not hold it yet: no New Year's Day, and a half day
// on Monday 3 July.
func TestRulesMatchThePublished2028Calendar(t *testing.T) {
	var got []string
	for _, d := range ruleDays(2028) {
		got = append(got, d.Date+half(d.HalfDay))
	}
	want := []string{
		"2028-01-17 closed", "2028-02-21 closed", "2028-04-14 closed", "2028-05-29 closed",
		"2028-06-19 closed", "2028-07-04 closed", "2028-09-04 closed", "2028-11-23 closed",
		"2028-12-25 closed", "2028-07-03 half day", "2028-11-24 half day",
	}
	if !slices.Equal(got, want) {
		t.Errorf("ruleDays(2028) = %v\nwant %v", got, want)
	}
}
