package execution

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/alexherrero/sherwood/internal/limits"
	"github.com/alexherrero/sherwood/internal/risk"
	"github.com/alexherrero/sherwood/internal/signals"
)

// Every symbol here is made up.

// seen is a risk check that passes every order and keeps the snapshots it
// was given.
type seen struct {
	mu    sync.Mutex
	snaps []risk.Snapshot
}

func (s *seen) Check(o risk.Order, snap risk.Snapshot) risk.Verdict {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snaps = append(s.snaps, snap)
	return passAll{}.Check(o, snap)
}

func TestTheSendChecksAgainstTheWatchlistInForce(t *testing.T) {
	checker := &seen{}
	h := newHarness(t, checker)
	h.hold(t, nil)
	h.strategy.Sectors["NEWX"] = "sector-a"
	if _, err := limits.Watch(context.Background(), h.store, h.strategy, h.clock.Now(), "NEWX"); err != nil {
		t.Fatal(err)
	}
	h.approved(t, signals.KindBuy, "AAA", "1", p("10.10"))
	h.send(t)
	if len(checker.snaps) == 0 {
		t.Fatal("the send checked no order")
	}
	for _, snap := range checker.snaps {
		if !slices.Equal(snap.Watchlist, []string{"AAA", "BBB", "NEWX"}) {
			t.Errorf("the send checked against watchlist %v, want the file's plus NEWX", snap.Watchlist)
		}
	}
}
