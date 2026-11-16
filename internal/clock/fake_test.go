package clock

import (
	"sync"
	"testing"
	"time"
)

var start = time.Date(2026, 3, 6, 9, 0, 0, 0, Eastern)

func fired(ch <-chan time.Time) (time.Time, bool) {
	select {
	case t := <-ch:
		return t, true
	default:
		return time.Time{}, false
	}
}

func TestFakeNowMovesOnlyWhenTold(t *testing.T) {
	f := NewFake(start)
	if got := f.Now(); !got.Equal(start) {
		t.Fatalf("Now = %v, want %v", got, start)
	}
	if got := f.Now(); !got.Equal(start) {
		t.Fatalf("Now moved on its own: %v", got)
	}
	f.Advance(90 * time.Minute)
	if want := start.Add(90 * time.Minute); !f.Now().Equal(want) {
		t.Fatalf("after Advance, Now = %v, want %v", f.Now(), want)
	}
	back := start.Add(-time.Hour)
	f.Set(back)
	if !f.Now().Equal(back) {
		t.Fatalf("after Set, Now = %v, want %v", f.Now(), back)
	}
}

func TestFakeAfterFiresAtItsDeadline(t *testing.T) {
	f := NewFake(start)
	ch := f.After(10 * time.Minute)
	if f.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1", f.Pending())
	}
	f.Advance(10*time.Minute - time.Nanosecond)
	if _, ok := fired(ch); ok {
		t.Fatal("fired before its deadline")
	}
	f.Advance(time.Nanosecond)
	got, ok := fired(ch)
	if !ok {
		t.Fatal("did not fire at its deadline")
	}
	if want := start.Add(10 * time.Minute); !got.Equal(want) {
		t.Fatalf("received %v, want %v", got, want)
	}
	if f.Pending() != 0 {
		t.Fatalf("Pending = %d after firing, want 0", f.Pending())
	}
}

func TestFakeAfterZeroFiresAtOnce(t *testing.T) {
	f := NewFake(start)
	for _, d := range []time.Duration{0, -time.Second} {
		got, ok := fired(f.After(d))
		if !ok || !got.Equal(start) {
			t.Fatalf("After(%v): fired=%v at %v, want immediate at %v", d, ok, got, start)
		}
	}
	if f.Pending() != 0 {
		t.Fatalf("Pending = %d, want 0", f.Pending())
	}
}

func TestFakeFiresTimersInDeadlineOrder(t *testing.T) {
	f := NewFake(start)
	late := f.After(3 * time.Hour)
	early := f.After(time.Hour)
	never := f.After(5 * time.Hour)

	f.Advance(4 * time.Hour)
	if _, ok := fired(early); !ok {
		t.Fatal("the one-hour timer did not fire")
	}
	if _, ok := fired(late); !ok {
		t.Fatal("the three-hour timer did not fire")
	}
	if _, ok := fired(never); ok {
		t.Fatal("the five-hour timer fired early")
	}
	if f.Pending() != 1 {
		t.Fatalf("Pending = %d, want 1", f.Pending())
	}
}

func TestFakeSetBackwardsDoesNotFire(t *testing.T) {
	f := NewFake(start)
	ch := f.After(time.Hour)
	f.Set(start.Add(-time.Hour))
	if _, ok := fired(ch); ok {
		t.Fatal("fired after the clock moved backwards")
	}
	f.Set(start.Add(time.Hour))
	if _, ok := fired(ch); !ok {
		t.Fatal("did not fire once the clock caught up")
	}
}

func TestFakeAdvanceNegativePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Advance(-1s) did not panic")
		}
	}()
	NewFake(start).Advance(-time.Second)
}

// TestFakeConcurrentUse is for the race detector: readers and waiters on
// several goroutines while the test moves the clock.
func TestFakeConcurrentUse(t *testing.T) {
	f := NewFake(start)
	chans := make([]<-chan time.Time, 8)
	for i := range chans {
		chans[i] = f.After(time.Duration(i+1) * time.Minute)
	}
	var wg sync.WaitGroup
	for _, ch := range chans {
		wg.Go(func() {
			_ = f.Now()
			<-ch
			_ = f.Pending()
		})
	}
	for range chans {
		f.Advance(time.Minute)
	}
	wg.Wait()
}

func TestRealAndFakeAreClocks(t *testing.T) {
	var _ Clock = Real{}
	var _ Clock = NewFake(start)
}
