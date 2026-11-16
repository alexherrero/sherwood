package clock

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// day returns noon Eastern on the given date.
func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation(DateLayout, s, Eastern)
	if err != nil {
		t.Fatal(err)
	}
	return d.Add(12 * time.Hour)
}

func utc(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// The full-day closures as published by NYSE Group on 23 December 2025,
// typed in again here so a slip in the table cannot pass its own test.
var publishedClosures = map[string]string{
	"2026-01-01": "New Year's Day",
	"2026-01-19": "Martin Luther King, Jr. Day",
	"2026-02-16": "Washington's Birthday",
	"2026-04-03": "Good Friday",
	"2026-05-25": "Memorial Day",
	"2026-06-19": "Juneteenth",
	"2026-07-03": "Independence Day",
	"2026-09-07": "Labor Day",
	"2026-11-26": "Thanksgiving Day",
	"2026-12-25": "Christmas Day",

	"2027-01-01": "New Year's Day",
	"2027-01-18": "Martin Luther King, Jr. Day",
	"2027-02-15": "Washington's Birthday",
	"2027-03-26": "Good Friday",
	"2027-05-31": "Memorial Day",
	"2027-06-18": "Juneteenth",
	"2027-07-05": "Independence Day",
	"2027-09-06": "Labor Day",
	"2027-11-25": "Thanksgiving Day",
	"2027-12-24": "Christmas Day",
}

// The published 1:00 p.m. early closes.
var publishedHalfDays = []string{"2026-11-27", "2026-12-24", "2027-11-26"}

func TestEasternIsNewYork(t *testing.T) {
	if Eastern.String() != "America/New_York" {
		t.Fatalf("Eastern = %q", Eastern)
	}
}

func TestPublishedClosures(t *testing.T) {
	cal := NYSE()
	for date, name := range publishedClosures {
		d := day(t, date)
		ok, err := cal.IsTradingDay(d)
		if err != nil || ok {
			t.Errorf("%s: IsTradingDay = %v, %v; want false (%s)", date, ok, err, name)
		}
		half, err := cal.IsHalfDay(d)
		if err != nil || half {
			t.Errorf("%s: IsHalfDay = %v, %v; want false", date, half, err)
		}
		_, err = cal.Session(d)
		if !errors.Is(err, ErrClosed) || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: Session error = %v; want ErrClosed naming %q", date, err, name)
		}
	}
}

func TestPublishedHalfDays(t *testing.T) {
	cal := NYSE()
	for _, date := range publishedHalfDays {
		d := day(t, date)
		ok, err := cal.IsTradingDay(d)
		if err != nil || !ok {
			t.Errorf("%s: IsTradingDay = %v, %v; want true", date, ok, err)
		}
		half, err := cal.IsHalfDay(d)
		if err != nil || !half {
			t.Errorf("%s: IsHalfDay = %v, %v; want true", date, half, err)
		}
		s, err := cal.Session(d)
		if err != nil {
			t.Fatalf("%s: Session: %v", date, err)
		}
		// All three half days are in standard time (UTC-5).
		wantOpen := utc(t, date+"T14:30:00Z")
		wantClose := utc(t, date+"T18:00:00Z")
		if !s.HalfDay || s.Name == "" || !s.Open.Equal(wantOpen) || !s.Close.Equal(wantClose) {
			t.Errorf("%s: Session = %+v; want half day %v to %v", date, s, wantOpen, wantClose)
		}
	}
}

// Every weekday not published as a closure or half day is a full session,
// and the totals match the exchange's year: 251 sessions in each.
func TestEveryOtherWeekdayIsARegularSession(t *testing.T) {
	cal := NYSE()
	half := map[string]bool{}
	for _, d := range publishedHalfDays {
		half[d] = true
	}
	sessions := map[int]int{}
	for d := day(t, "2026-01-01"); d.Year() <= 2027; d = d.AddDate(0, 0, 1) {
		date := d.Format(DateLayout)
		ok, err := cal.IsTradingDay(d)
		if err != nil {
			t.Fatalf("%s: %v", date, err)
		}
		_, closed := publishedClosures[date]
		weekend := d.Weekday() == time.Saturday || d.Weekday() == time.Sunday
		if want := !closed && !weekend; ok != want {
			t.Errorf("%s (%s): IsTradingDay = %v, want %v", date, d.Weekday(), ok, want)
			continue
		}
		if !ok {
			continue
		}
		sessions[d.Year()]++
		isHalf, _ := cal.IsHalfDay(d)
		if isHalf != half[date] {
			t.Errorf("%s: IsHalfDay = %v, want %v", date, isHalf, half[date])
		}
		s, err := cal.Session(d)
		if err != nil {
			t.Fatalf("%s: Session: %v", date, err)
		}
		wantLen := 390 * time.Minute
		if half[date] {
			wantLen = 210 * time.Minute
		}
		if got := s.Close.Sub(s.Open); got != wantLen {
			t.Errorf("%s: session lasts %v, want %v", date, got, wantLen)
		}
	}
	for _, y := range []int{2026, 2027} {
		if sessions[y] != 251 {
			t.Errorf("%d has %d sessions, want 251", y, sessions[y])
		}
	}
}

func TestDaysThatLookLikeHolidaysButTrade(t *testing.T) {
	cal := NYSE()
	for _, date := range []string{
		"2026-07-02", // before Independence Day (observed Friday): no early close
		"2027-07-02", // before Independence Day (observed Monday)
		"2027-12-23", // before Christmas (observed Friday)
		"2027-12-31", // New Year's Day 2028 is a Saturday and is not observed
	} {
		s, err := cal.Session(day(t, date))
		if err != nil || s.HalfDay {
			t.Errorf("%s: Session = %+v, %v; want a regular session", date, s, err)
		}
	}
}

func TestWeekendsAreClosed(t *testing.T) {
	cal := NYSE()
	for _, date := range []string{"2026-01-03", "2026-01-04", "2026-07-04", "2027-12-25", "2027-12-26"} {
		d := day(t, date)
		ok, err := cal.IsTradingDay(d)
		if err != nil || ok {
			t.Errorf("%s: IsTradingDay = %v, %v; want false", date, ok, err)
		}
		_, err = cal.Session(d)
		if !errors.Is(err, ErrClosed) || !strings.Contains(err.Error(), d.Weekday().String()) {
			t.Errorf("%s: Session error = %v; want ErrClosed naming %s", date, err, d.Weekday())
		}
	}
}

// Sessions are 9:30 to 4:00 Eastern on both sides of each daylight-saving
// change, which moves them an hour in UTC.
func TestSessionsAcrossDaylightSaving(t *testing.T) {
	cal := NYSE()
	cases := []struct {
		date, open, close string
	}{
		{"2026-03-06", "2026-03-06T14:30:00Z", "2026-03-06T21:00:00Z"}, // Friday before clocks go forward on 8 March
		{"2026-03-09", "2026-03-09T13:30:00Z", "2026-03-09T20:00:00Z"},
		{"2026-10-30", "2026-10-30T13:30:00Z", "2026-10-30T20:00:00Z"}, // Friday before clocks go back on 1 November
		{"2026-11-02", "2026-11-02T14:30:00Z", "2026-11-02T21:00:00Z"},
		{"2027-03-12", "2027-03-12T14:30:00Z", "2027-03-12T21:00:00Z"}, // clocks go forward on 14 March
		{"2027-03-15", "2027-03-15T13:30:00Z", "2027-03-15T20:00:00Z"},
		{"2027-11-05", "2027-11-05T13:30:00Z", "2027-11-05T20:00:00Z"}, // clocks go back on 7 November
		{"2027-11-08", "2027-11-08T14:30:00Z", "2027-11-08T21:00:00Z"},
	}
	for _, c := range cases {
		s, err := cal.Session(day(t, c.date))
		if err != nil {
			t.Fatalf("%s: %v", c.date, err)
		}
		if !s.Open.Equal(utc(t, c.open)) || !s.Close.Equal(utc(t, c.close)) {
			t.Errorf("%s: session %v to %v, want %s to %s", c.date, s.Open.UTC(), s.Close.UTC(), c.open, c.close)
		}
		if s.Open.Location() != Eastern || s.Open.Hour() != 9 || s.Open.Minute() != 30 {
			t.Errorf("%s: open %v is not 9:30 Eastern", c.date, s.Open)
		}
	}

	// The Monday after clocks go forward starts at 04:00 UTC, not 05:00.
	next, err := cal.NextTradingDay(day(t, "2026-03-06"))
	if err != nil || !next.Equal(utc(t, "2026-03-09T04:00:00Z")) {
		t.Errorf("NextTradingDay(2026-03-06) = %v, %v; want 2026-03-09 midnight EDT", next.UTC(), err)
	}
}

// The trading date is the Eastern date, whatever zone the time is in, and
// the boundary moves with daylight saving.
func TestDatesAreEasternDates(t *testing.T) {
	cases := []struct{ at, date string }{
		{"2026-03-08T04:59:59Z", "2026-03-07"}, // 23:59:59 EST
		{"2026-03-08T05:00:00Z", "2026-03-08"}, // midnight EST, clocks go forward at 2 a.m.
		{"2026-11-01T03:59:59Z", "2026-10-31"}, // 23:59:59 EDT
		{"2026-11-01T04:00:00Z", "2026-11-01"}, // midnight EDT, clocks go back at 2 a.m.
		{"2026-11-02T04:59:59Z", "2026-11-01"}, // 23:59:59 EST on the 25-hour day
	}
	for _, c := range cases {
		if got := DateOf(utc(t, c.at)).Format(DateLayout); got != c.date {
			t.Errorf("DateOf(%s) = %s, want %s", c.at, got, c.date)
		}
	}

	cal := NYSE()
	// 22:00 on 2 July Eastern is still the 2nd, a trading day; two hours
	// later it is the observed Independence Day.
	if ok, err := cal.IsTradingDay(utc(t, "2026-07-03T02:00:00Z")); err != nil || !ok {
		t.Errorf("2026-07-03T02:00Z (2 July Eastern): IsTradingDay = %v, %v; want true", ok, err)
	}
	if ok, err := cal.IsTradingDay(utc(t, "2026-07-03T04:00:00Z")); err != nil || ok {
		t.Errorf("2026-07-03T04:00Z (3 July Eastern): IsTradingDay = %v, %v; want false", ok, err)
	}
}

func TestNextAndPreviousTradingDay(t *testing.T) {
	cal := NYSE()
	cases := []struct {
		name     string
		step     func(time.Time) (time.Time, error)
		from, to string
	}{
		{"over Good Friday", cal.NextTradingDay, "2026-04-02", "2026-04-06"},
		{"from a half day over Christmas", cal.NextTradingDay, "2026-12-24", "2026-12-28"},
		{"over New Year's Day into 2027", cal.NextTradingDay, "2026-12-31", "2027-01-04"},
		{"from a Saturday", cal.NextTradingDay, "2026-01-03", "2026-01-05"},
		{"from a holiday", cal.NextTradingDay, "2026-11-26", "2026-11-27"},
		{"over the July weekend", cal.NextTradingDay, "2027-07-02", "2027-07-06"},
		{"back over New Year's Day into 2026", cal.PreviousTradingDay, "2027-01-04", "2026-12-31"},
		{"back over Independence Day", cal.PreviousTradingDay, "2027-07-06", "2027-07-02"},
		{"back over Juneteenth", cal.PreviousTradingDay, "2027-06-21", "2027-06-17"},
		{"back from a Sunday", cal.PreviousTradingDay, "2026-03-08", "2026-03-06"},
	}
	for _, c := range cases {
		got, err := c.step(day(t, c.from))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got.Format(DateLayout) != c.to || got.Location() != Eastern || got.Hour() != 0 {
			t.Errorf("%s: from %s got %v, want midnight Eastern on %s", c.name, c.from, got, c.to)
		}
	}
}

func TestUncoveredYearsAreRefused(t *testing.T) {
	cal := NYSE()
	for _, date := range []string{"2025-12-31", "2028-01-03"} {
		d := day(t, date)
		if _, err := cal.IsTradingDay(d); !errors.Is(err, ErrNotCovered) {
			t.Errorf("%s: IsTradingDay error = %v, want ErrNotCovered", date, err)
		}
		if _, err := cal.IsHalfDay(d); !errors.Is(err, ErrNotCovered) {
			t.Errorf("%s: IsHalfDay error = %v, want ErrNotCovered", date, err)
		}
		if _, err := cal.Session(d); !errors.Is(err, ErrNotCovered) {
			t.Errorf("%s: Session error = %v, want ErrNotCovered", date, err)
		}
		if cal.Covers(d) {
			t.Errorf("%s: Covers = true", date)
		}
	}
	// Stepping off either end of the table fails instead of guessing.
	if _, err := cal.NextTradingDay(day(t, "2027-12-31")); !errors.Is(err, ErrNotCovered) {
		t.Errorf("NextTradingDay(2027-12-31) error = %v, want ErrNotCovered", err)
	}
	if _, err := cal.PreviousTradingDay(day(t, "2026-01-02")); !errors.Is(err, ErrNotCovered) {
		t.Errorf("PreviousTradingDay(2026-01-02) error = %v, want ErrNotCovered", err)
	}
}

func TestCheckCoverage(t *testing.T) {
	cal := NYSE()
	cases := []struct {
		at string
		ok bool
	}{
		{"2026-01-01T05:00:00Z", true},  // first moment of 2026 Eastern
		{"2026-12-15T15:00:00Z", true},  // December 2026: 2027 is in the table
		{"2027-11-30T15:00:00Z", true},  // the last day before the warning
		{"2027-12-01T04:59:59Z", true},  // still 30 November Eastern
		{"2027-12-01T05:00:00Z", false}, // 1 December Eastern and 2028 is missing
		{"2028-01-03T15:00:00Z", false}, // a year the table does not cover
		{"2026-01-01T04:59:59Z", false}, // still 2025 Eastern
	}
	for _, c := range cases {
		err := cal.CheckCoverage(utc(t, c.at))
		if c.ok && err != nil {
			t.Errorf("%s: CheckCoverage = %v, want nil", c.at, err)
		}
		if !c.ok && !errors.Is(err, ErrNotCovered) {
			t.Errorf("%s: CheckCoverage = %v, want ErrNotCovered", c.at, err)
		}
	}
}

func TestNewCalendarRefusesBadTables(t *testing.T) {
	cases := []struct {
		name  string
		table []Day
	}{
		{"bad date", []Day{{Date: "2026-13-01", Name: "x"}}},
		{"weekend", []Day{{Date: "2026-07-04", Name: "Independence Day"}}},
		{"duplicate", []Day{{Date: "2026-01-01", Name: "a"}, {Date: "2026-01-01", Name: "b", HalfDay: true}}},
		{"outside the years", []Day{{Date: "2028-01-03", Name: "x"}}},
		{"no name", []Day{{Date: "2026-01-02"}}},
	}
	for _, c := range cases {
		if _, err := NewCalendar(2026, 2027, c.table); err == nil {
			t.Errorf("%s: NewCalendar accepted the table", c.name)
		}
	}
	if _, err := NewCalendar(2027, 2026, nil); err == nil {
		t.Error("NewCalendar accepted backwards years")
	}
}
