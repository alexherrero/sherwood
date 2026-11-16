package clock

import (
	"time"
	// The container image carries no system zone files, so the zone database
	// is compiled into the binary.
	_ "time/tzdata"
)

// Clock is the only way the rest of sherwood learns the time. Rules, the risk
// check and the scheduler are given a Clock instead of calling time.Now, so a
// test can hand them a Fake and decide exactly what time it is.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// After returns a channel that receives the time once d has passed.
	After(d time.Duration) <-chan time.Time
}

// Real is the wall clock. It is the one Clock the running application uses;
// tests use a Fake.
type Real struct{}

// Now returns the wall-clock time.
func (Real) Now() time.Time { return time.Now() }

// After waits for d on the wall clock.
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

var _ Clock = Real{}

// Eastern is US Eastern time (America/New_York), the exchange's own zone.
// Trading dates, sessions and job schedules are all reckoned in it.
var Eastern = mustLoad("America/New_York")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		// Unreachable: time/tzdata embeds the zone database.
		panic("clock: load " + name + ": " + err.Error())
	}
	return loc
}

// DateOf returns midnight in Eastern time at the start of t's Eastern
// calendar date. Two times on the same trading date give equal results.
func DateOf(t time.Time) time.Time {
	y, m, d := t.In(Eastern).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, Eastern)
}
