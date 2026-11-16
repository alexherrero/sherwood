package clock

// The NYSE holiday table. It lists every weekday the exchange is closed all
// day, and every early-close half day, for each year from nyseFirstYear to
// nyseLastYear. Any weekday in those years that is not listed is a regular
// session.
//
// Source: NYSE Group, "NYSE Group Announces 2026, 2027 and 2028 Holiday and
// Early Closings Calendar", 23 December 2025 (also published at
// https://www.nyse.com/markets/hours-calendars).
//
// Notes from that release:
//   - New Year's Day 2028 falls on a Saturday and is not observed, so Friday
//     31 December 2027 is a regular session.
//   - Juneteenth 2027 (a Saturday) is observed on Friday 18 June; Independence
//     Day 2027 (a Sunday) on Monday 5 July; Christmas 2027 (a Saturday) on
//     Friday 24 December.
//
// Updating it each year: the exchange publishes the next years' calendar each
// December. Add the new year's closures and half days below, move
// nyseLastYear on, and check every date against the exchange's own page.
// Calendar.CheckCoverage refuses from 1 December when next year is missing,
// so the update cannot be forgotten.
const (
	nyseFirstYear = 2026
	nyseLastYear  = 2027
)

var nyseDays = []Day{
	// 2026
	{Date: "2026-01-01", Name: "New Year's Day"},
	{Date: "2026-01-19", Name: "Martin Luther King, Jr. Day"},
	{Date: "2026-02-16", Name: "Washington's Birthday"},
	{Date: "2026-04-03", Name: "Good Friday"},
	{Date: "2026-05-25", Name: "Memorial Day"},
	{Date: "2026-06-19", Name: "Juneteenth National Independence Day"},
	{Date: "2026-07-03", Name: "Independence Day (observed)"},
	{Date: "2026-09-07", Name: "Labor Day"},
	{Date: "2026-11-26", Name: "Thanksgiving Day"},
	{Date: "2026-11-27", Name: "Day after Thanksgiving", HalfDay: true},
	{Date: "2026-12-24", Name: "Christmas Eve", HalfDay: true},
	{Date: "2026-12-25", Name: "Christmas Day"},

	// 2027
	{Date: "2027-01-01", Name: "New Year's Day"},
	{Date: "2027-01-18", Name: "Martin Luther King, Jr. Day"},
	{Date: "2027-02-15", Name: "Washington's Birthday"},
	{Date: "2027-03-26", Name: "Good Friday"},
	{Date: "2027-05-31", Name: "Memorial Day"},
	{Date: "2027-06-18", Name: "Juneteenth National Independence Day (observed)"},
	{Date: "2027-07-05", Name: "Independence Day (observed)"},
	{Date: "2027-09-06", Name: "Labor Day"},
	{Date: "2027-11-25", Name: "Thanksgiving Day"},
	{Date: "2027-11-26", Name: "Day after Thanksgiving", HalfDay: true},
	{Date: "2027-12-24", Name: "Christmas Day (observed)"},
}

var nyse = mustCalendar(NewCalendar(nyseFirstYear, nyseLastYear, nyseDays))

// NYSE returns the New York Stock Exchange calendar built from the table
// above. The calendar is shared and never changes.
func NYSE() *Calendar { return nyse }

func mustCalendar(c *Calendar, err error) *Calendar {
	if err != nil {
		panic(err)
	}
	return c
}
