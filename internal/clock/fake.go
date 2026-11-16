package clock

import (
	"sort"
	"sync"
	"time"
)

// Fake is a Clock that only moves when a test moves it. It never sleeps:
// channels from After fire during the Set or Advance call that reaches their
// deadline. It is safe for concurrent use.
type Fake struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

var _ Clock = (*Fake)(nil)

// NewFake returns a Fake that reads now until it is moved.
func NewFake(now time.Time) *Fake {
	return &Fake{now: now}
}

// Now returns the fake's current time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After returns a channel that receives the fake's time once the fake has
// been moved d or more past the current time. A d of zero or less fires at
// once. The channel is buffered, so an unread one never blocks the fake.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.timers = append(f.timers, fakeTimer{at: f.now.Add(d), ch: ch})
	return ch
}

// Advance moves the fake forward by d and fires every timer now due, in
// deadline order. A negative d panics; use Set to move backwards.
func (f *Fake) Advance(d time.Duration) {
	if d < 0 {
		panic("clock: Fake.Advance with a negative duration")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setLocked(f.now.Add(d))
}

// Set moves the fake to t and fires every timer now due, in deadline order.
// Moving backwards is allowed, as a real clock can be stepped back; pending
// timers then wait for the fake to catch up again.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setLocked(t)
}

func (f *Fake) setLocked(t time.Time) {
	f.now = t
	sort.SliceStable(f.timers, func(i, j int) bool { return f.timers[i].at.Before(f.timers[j].at) })
	kept := f.timers[:0]
	for _, tm := range f.timers {
		if tm.at.After(t) {
			kept = append(kept, tm)
			continue
		}
		tm.ch <- t
	}
	clear(f.timers[len(kept):])
	f.timers = kept
}

// Pending reports how many After channels are still waiting, so a test can
// tell that the code under test has started waiting before it moves the fake.
func (f *Fake) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}
