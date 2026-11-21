package broker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// harness runs a supervisor in the background and collects what it reports.
type harness struct {
	sup    *Supervisor
	clock  *fakeClock
	errs   chan error
	ready  chan struct{}
	stderr *syncBuffer
	cancel context.CancelFunc
	done   chan struct{} // closed when Run returns
	runErr error         // what Run returned; read after done is closed
}

func startSupervisor(t *testing.T, cfg SupervisorConfig) *harness {
	t.Helper()
	h := &harness{
		clock:  newFakeClock(),
		errs:   make(chan error, 16),
		ready:  make(chan struct{}, 16),
		stderr: &syncBuffer{},
		done:   make(chan struct{}),
	}
	cfg.Clock = h.clock
	cfg.Stderr = h.stderr
	cfg.OnError = func(err error) { h.errs <- err }
	cfg.OnReady = func() { h.ready <- struct{}{} }
	if cfg.SocketPath == "" {
		cfg.SocketPath = shortSocketPath(t)
	}
	sup, err := NewSupervisor(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.sup = sup
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() {
		h.runErr = sup.Run(ctx)
		close(h.done)
	}()
	t.Cleanup(func() {
		cancel()
		<-h.done
		if t.Failed() {
			t.Logf("helper output:\n%s", h.stderr.String())
		}
	})
	return h
}

// stop ends Run and checks it returned because of the cancel, with no error
// reported on the way out.
func (h *harness) stop(t *testing.T) {
	t.Helper()
	h.cancel()
	receive(t, h.done, "Run to return")
	if !errors.Is(h.runErr, context.Canceled) {
		t.Errorf("Run returned %v, want context.Canceled", h.runErr)
	}
	select {
	case err := <-h.errs:
		t.Errorf("shutdown reported an error: %v", err)
	default:
	}
}

// TestContractWithTheRealHelper starts the Python helper from brokerhelper/,
// pings it, kills it, and checks the supervisor reports the exit and brings a
// new helper up.
func TestContractWithTheRealHelper(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH, so the broker helper contract test cannot run; install Python 3 to run it")
	}
	src, err := filepath.Abs(filepath.Join("..", "..", "brokerhelper", "src"))
	if err != nil {
		t.Fatal(err)
	}
	sock := shortSocketPath(t)
	h := startSupervisor(t, SupervisorConfig{
		Command:     []string{python, "-B", "-m", "brokerhelper"},
		Env:         append(os.Environ(), "PYTHONPATH="+src, "PYTHONDONTWRITEBYTECODE=1"),
		SocketPath:  sock,
		CallTimeout: 5 * time.Second,
	})
	ctx := context.Background()
	client := h.sup.Client()

	receive(t, h.ready, "the helper to be ready")
	if !h.sup.Healthy() {
		t.Error("a ready helper is not healthy")
	}
	first, err := client.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if first.Version == "" || first.PID <= 0 {
		t.Fatalf("Ping result = %+v", first)
	}

	var he *HelperError
	if err := client.Call(ctx, "no_such_op", nil, nil); !errors.As(err, &he) || he.Code != CodeUnknownOp {
		t.Fatalf("unknown op: err = %v, want a HelperError with code %s", err, CodeUnknownOp)
	}
	if err := client.Call(ctx, "ping", map[string]string{"x": "1"}, nil); !errors.As(err, &he) || he.Code != CodeBadRequest {
		t.Fatalf("ping with args: err = %v, want a HelperError with code %s", err, CodeBadRequest)
	}

	// Kill the helper: the supervisor must report it, not swallow it.
	if err := syscall.Kill(first.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	reported := receive(t, h.errs, "the helper's exit to be reported")
	var exit *ExitError
	if !errors.As(reported, &exit) {
		t.Fatalf("reported %v, want an ExitError", reported)
	}
	if !strings.Contains(reported.Error(), "killed") {
		t.Errorf("reported %q, want it to say the helper was killed", reported)
	}
	if h.sup.Healthy() {
		t.Error("a killed helper is still healthy")
	}

	// The supervisor is now waiting to restart. Calls fail meanwhile.
	wait := receive(t, h.clock.waits, "the restart wait")
	if wait.d != DefaultRestartDelay {
		t.Errorf("restart delay = %s, want %s", wait.d, DefaultRestartDelay)
	}
	if _, err := client.Ping(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Ping while the helper is down: err = %v, want ErrUnavailable", err)
	}

	// Let the restart happen; a new helper answers.
	wait.ch <- time.Time{}
	receive(t, h.ready, "the restarted helper to be ready")
	if !h.sup.Healthy() {
		t.Error("the restarted helper is not healthy")
	}
	second, err := client.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping after restart: %v", err)
	}
	if second.PID == first.PID {
		t.Errorf("restarted helper has the old pid %d", first.PID)
	}

	// Shutting down stops the helper with SIGTERM; it removes its socket.
	h.stop(t)
	if _, err := os.Stat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket still there after shutdown: %v", err)
	}
}

func TestStartFailureIsReportedAndRetriedAfterTheDelay(t *testing.T) {
	h := startSupervisor(t, SupervisorConfig{
		Command:      []string{filepath.Join(t.TempDir(), "no-such-helper")},
		RestartDelay: 3 * time.Second,
	})
	err := receive(t, h.errs, "the start failure")
	if !errors.Is(err, ErrStart) {
		t.Fatalf("reported %v, want ErrStart", err)
	}
	wait := receive(t, h.clock.waits, "the restart wait")
	if wait.d != 3*time.Second {
		t.Errorf("restart delay = %s, want 3s", wait.d)
	}
	wait.ch <- time.Time{}
	if err := receive(t, h.errs, "the second start failure"); !errors.Is(err, ErrStart) {
		t.Fatalf("reported %v, want ErrStart", err)
	}
	receive(t, h.clock.waits, "the second restart wait")
	h.stop(t)
}

func TestExitBeforeReadyIsAStartFailure(t *testing.T) {
	h := startSupervisor(t, SupervisorConfig{
		Command: []string{"sh", "-c", "echo cannot start >&2; exit 3"},
	})
	err := receive(t, h.errs, "the start failure")
	var exit *ExitError
	if !errors.Is(err, ErrStart) || !errors.As(err, &exit) {
		t.Fatalf("reported %v, want ErrStart wrapping an ExitError", err)
	}
	if !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("reported %q, want the exit status", err)
	}
	receive(t, h.clock.waits, "the restart wait")
	if !strings.Contains(h.stderr.String(), "cannot start") {
		t.Errorf("helper stderr was not passed on: %q", h.stderr.String())
	}
	h.stop(t)
}

func TestNotReadyInTimeIsAStartFailure(t *testing.T) {
	h := startSupervisor(t, SupervisorConfig{
		Command:      []string{"sh", "-c", "exec sleep 60"},
		StartTimeout: 50 * time.Millisecond,
	})
	err := receive(t, h.errs, "the start timeout")
	if !errors.Is(err, ErrStart) || !strings.Contains(err.Error(), "not ready within") {
		t.Fatalf("reported %v, want a start timeout", err)
	}
	if h.sup.Healthy() {
		t.Error("a helper that never said it was ready is healthy")
	}
	receive(t, h.clock.waits, "the restart wait")
	h.stop(t)
}

func TestCleanExitIsStillReported(t *testing.T) {
	h := startSupervisor(t, SupervisorConfig{
		Command: []string{"sh", "-c", "echo ready; echo chatter; exit 0"},
	})
	receive(t, h.ready, "the helper to be ready")
	err := receive(t, h.errs, "the exit")
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Err != nil {
		t.Fatalf("reported %v, want an ExitError for a clean exit", err)
	}
	if h.sup.Healthy() {
		t.Error("a helper that exited is still healthy")
	}
	receive(t, h.clock.waits, "the restart wait")
	if !strings.Contains(h.stderr.String(), "chatter") {
		t.Errorf("helper stdout was not passed on: %q", h.stderr.String())
	}
	h.stop(t)
}

func TestShutdownStopsARunningHelperWithoutReporting(t *testing.T) {
	h := startSupervisor(t, SupervisorConfig{
		Command: []string{"sh", "-c", "echo ready; exec sleep 60"},
	})
	receive(t, h.ready, "the helper to be ready")
	if !h.sup.Healthy() {
		t.Error("a ready helper is not healthy")
	}
	h.stop(t)
	if h.sup.Healthy() {
		t.Error("the helper is still healthy after shutdown")
	}
}

func TestNewSupervisorChecksItsConfig(t *testing.T) {
	ok := SupervisorConfig{Command: []string{"x"}, SocketPath: "s", OnError: func(error) {}}
	if _, err := NewSupervisor(ok); err != nil {
		t.Fatalf("valid config refused: %v", err)
	}
	for name, mutate := range map[string]func(*SupervisorConfig){
		"no command": func(c *SupervisorConfig) { c.Command = nil },
		"no socket":  func(c *SupervisorConfig) { c.SocketPath = "" },
		"no OnError": func(c *SupervisorConfig) { c.OnError = nil },
	} {
		cfg := ok
		mutate(&cfg)
		if _, err := NewSupervisor(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReadyWatcher(t *testing.T) {
	var out syncBuffer
	w := newReadyWatcher(&out)
	for _, chunk := range []string{"hel", "lo\nrea", "dy", "\nbye\n", "ready\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-w.ch:
	default:
		t.Fatal("ready line split across writes was not seen")
	}
	if got := out.String(); got != "hello\nbye\n" {
		t.Errorf("passed on %q, want the non-ready lines", got)
	}
}
