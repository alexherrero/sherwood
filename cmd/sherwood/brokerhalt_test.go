package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/notify"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
	"github.com/alexherrero/sherwood/internal/store"
)

// A broker helper that exits before it is ready throws the stop switch
// through the same hook run gives the supervisor.
func TestABrokerHelperFailureHaltsTrading(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "sherwood.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	logger, logs := loggingtest.New(t)
	clk := clock.NewFake(now) // never advanced, so the helper is not restarted
	alerts := &notifytest.Fake{}
	sw, err := halt.New(halt.Config{Store: db, Clock: clk, Notifier: alerts, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}

	sockDir, err := os.MkdirTemp("", "bh") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	failed := make(chan struct{}, 1)
	hook := haltOnBrokerFailure(ctx, sw)
	sup, err := newSupervisor(
		env{clock: clk, stderr: &strings.Builder{}, helperCommand: []string{"sh", "-c", "exit 3"}},
		options{brokerSocket: filepath.Join(sockDir, "b.sock")}, logger,
		func(err error) {
			hook(err)
			failed <- struct{}{}
		})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	<-failed
	cancel()
	if err := <-done; err == nil {
		t.Error("Run returned nil after cancellation")
	}

	st, err := halt.Active(context.Background(), db.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if !st.On() || len(st.Active) != 1 || st.Active[0].Cause != halt.BrokerError {
		t.Fatalf("switch = %+v, want one broker halt", st)
	}
	if ev := st.Active[0].Evidence; !strings.HasPrefix(ev, "The broker helper failed: broker helper did not start") ||
		!strings.Contains(ev, "exit status 3") {
		t.Errorf("evidence %q", ev)
	}
	if sent := alerts.Sent(); len(sent) != 1 || sent[0] != notify.TradingHalted() {
		t.Errorf("alerts %v, want one TradingHalted", sent)
	}
	if !strings.Contains(logs.String(), `"msg":"broker helper failed"`) {
		t.Errorf("the failure was not logged:\n%s", logs)
	}
}

// stepClock is the supervisor's clock for a test that restarts the helper:
// each restart wait is handed to the test, which lets it end.
type stepClock struct {
	clock.Clock
	waits chan chan time.Time
}

func (c stepClock) After(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.waits <- ch
	return ch
}

// A helper that crashes and is restarted again and again, each time with a
// different exit status, gives one halt and one push, not one per restart.
func TestARestartingBrokerHelperHaltsOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "sherwood.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	logger, logs := loggingtest.New(t)
	clk := clock.NewFake(now)
	alerts := &notifytest.Fake{}
	sw, err := halt.New(halt.Config{Store: db, Clock: clk, Notifier: alerts, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}

	sockDir, err := os.MkdirTemp("", "bh") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	// Each start exits with the next status: 1, 2, 3, 4.
	count := filepath.Join(sockDir, "starts")
	script := `n=$(cat "$0" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$0"; exit $n`
	failed := make(chan struct{}, 1)
	hook := haltOnBrokerFailure(ctx, sw)
	steps := stepClock{Clock: clk, waits: make(chan chan time.Time)}
	sup, err := newSupervisor(
		env{clock: steps, stderr: &strings.Builder{}, helperCommand: []string{"sh", "-c", script, count}},
		options{brokerSocket: filepath.Join(sockDir, "b.sock")}, logger,
		func(err error) {
			hook(err)
			failed <- struct{}{}
		})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	const restarts = 4
	for i := range restarts {
		<-failed
		wait := <-steps.waits
		if i < restarts-1 {
			wait <- now // restart now
		}
	}
	cancel()
	if err := <-done; err == nil {
		t.Error("Run returned nil after cancellation")
	}

	st, err := halt.Active(context.Background(), db.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Active) != 1 || st.Active[0].Cause != halt.BrokerError || !strings.Contains(st.Active[0].Evidence, "exit status 1") {
		t.Fatalf("switch = %+v, want one broker halt, from the first failure", st.Active)
	}
	if sent := alerts.Sent(); len(sent) != 1 || sent[0] != notify.TradingHalted() {
		t.Errorf("alerts %v, want one TradingHalted", sent)
	}
	// Every failure is still logged.
	if n := strings.Count(logs.String(), `"msg":"broker helper failed"`); n != restarts {
		t.Errorf("%d failures logged, want %d:\n%s", n, restarts, logs)
	}
	for _, status := range []string{"exit status 2", "exit status 3", "exit status 4"} {
		if !strings.Contains(logs.String(), status) {
			t.Errorf("the log lacks %q, so the restarts did not fail differently", status)
		}
	}
}
