package broker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Clock is the one piece of timing the supervisor needs: waiting before a
// restart. Tests pass a fake so they never sleep.
type Clock interface {
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Defaults for SupervisorConfig fields left at zero.
const (
	DefaultStartTimeout  = 10 * time.Second
	DefaultRestartDelay  = time.Second
	DefaultShutdownGrace = 5 * time.Second
)

// readyLine is what the helper prints on stdout once it is listening.
const readyLine = "ready"

// SupervisorConfig says how to start the helper and where to report.
type SupervisorConfig struct {
	// Command is the program and arguments that start the helper, for example
	// {"python3", "-m", "brokerhelper"}. The supervisor appends
	// "--socket", SocketPath.
	Command []string
	// Dir is the helper's working directory; empty means this process's.
	Dir string
	// Env is the helper's environment; nil means this process's.
	Env []string
	// SocketPath is the Unix socket the helper listens on.
	SocketPath string

	// CallTimeout bounds each call made through Client. Zero means
	// DefaultCallTimeout.
	CallTimeout time.Duration
	// StartTimeout is how long the helper has to say it is ready. Zero means
	// DefaultStartTimeout.
	StartTimeout time.Duration
	// RestartDelay is the wait between a failure and the next start. Zero
	// means DefaultRestartDelay.
	RestartDelay time.Duration
	// ShutdownGrace is how long the helper has to exit after SIGTERM before it
	// is killed. Zero means DefaultShutdownGrace.
	ShutdownGrace time.Duration
	// Clock times the restart delay. Nil means the real clock.
	Clock Clock

	// Stderr receives the helper's standard error and any stdout lines other
	// than its ready line. Nil means os.Stderr.
	Stderr io.Writer

	// OnError is called, from the supervisor's goroutine, with every helper
	// failure: a start that failed or timed out, or a helper that exited. It
	// is required, so no failure is dropped. A later part wires it to halt
	// trading.
	OnError func(error)
	// OnReady, if set, is called each time a newly started helper is ready to
	// take calls.
	OnReady func()
}

// ExitError reports a helper that exited while it was supposed to be running.
type ExitError struct {
	Err error // what the process's Wait returned; nil for a clean exit
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return "broker helper exited"
	}
	return "broker helper exited: " + e.Err.Error()
}

func (e *ExitError) Unwrap() error { return e.Err }

// ErrStart reports a helper that could not be started or did not become ready.
var ErrStart = errors.New("broker helper did not start")

// Supervisor starts the broker helper, restarts it whenever it exits, and
// reports each failure to OnError.
type Supervisor struct {
	cfg    SupervisorConfig
	client *Client
}

// NewSupervisor checks cfg and returns a supervisor. It starts nothing; call
// Run.
func NewSupervisor(cfg SupervisorConfig) (*Supervisor, error) {
	if len(cfg.Command) == 0 {
		return nil, errors.New("broker: supervisor needs a command")
	}
	if cfg.SocketPath == "" {
		return nil, errors.New("broker: supervisor needs a socket path")
	}
	if cfg.OnError == nil {
		return nil, errors.New("broker: supervisor needs an OnError function")
	}
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = DefaultStartTimeout
	}
	if cfg.RestartDelay <= 0 {
		cfg.RestartDelay = DefaultRestartDelay
	}
	if cfg.ShutdownGrace <= 0 {
		cfg.ShutdownGrace = DefaultShutdownGrace
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	// The helper's stdout and stderr are copied on separate goroutines.
	cfg.Stderr = &lockedWriter{w: cfg.Stderr}
	return &Supervisor{cfg: cfg, client: NewClient(cfg.SocketPath, cfg.CallTimeout)}, nil
}

// Client returns the client for the supervised helper. It keeps working across
// restarts; while the helper is down its calls fail with ErrUnavailable.
func (s *Supervisor) Client() *Client { return s.client }

// Run starts the helper and keeps it running until ctx ends. Each time the
// helper fails to start or exits, Run reports the error to OnError, waits
// RestartDelay and starts it again. When ctx ends, Run stops the helper with
// SIGTERM (then SIGKILL after ShutdownGrace) and returns ctx's error.
func (s *Supervisor) Run(ctx context.Context) error {
	for {
		err := s.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.cfg.OnError(err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.cfg.Clock.After(s.cfg.RestartDelay):
		}
	}
}

// runOnce starts one helper process and returns when it has exited. The
// returned error is always non-nil.
func (s *Supervisor) runOnce(ctx context.Context) error {
	args := append(append([]string(nil), s.cfg.Command[1:]...), "--socket", s.cfg.SocketPath)
	// The command comes from the application's own settings, not from input.
	cmd := exec.CommandContext(ctx, s.cfg.Command[0], args...) //nolint:gosec // G204: trusted command
	cmd.Dir = s.cfg.Dir
	cmd.Env = s.cfg.Env
	cmd.Stderr = s.cfg.Stderr
	ready := newReadyWatcher(s.cfg.Stderr)
	cmd.Stdout = ready
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = s.cfg.ShutdownGrace

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: %w", ErrStart, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	startTimer, cancelStart := context.WithTimeout(ctx, s.cfg.StartTimeout)
	defer cancelStart()
	select {
	case <-ready.ch:
	case err := <-exited:
		return fmt.Errorf("%w: exited before it was ready: %w", ErrStart, &ExitError{Err: err})
	case <-startTimer.Done():
		if ctx.Err() == nil {
			_ = cmd.Process.Kill()
		}
		stopped := &ExitError{Err: <-exited}
		return fmt.Errorf("%w: not ready within %s, so it was stopped: %w", ErrStart, s.cfg.StartTimeout, stopped)
	}

	if s.cfg.OnReady != nil {
		s.cfg.OnReady()
	}
	return &ExitError{Err: <-exited}
}

// readyWatcher is the helper's stdout. It closes ch when the helper prints its
// ready line and passes every other line on to out.
type readyWatcher struct {
	ch   chan struct{}
	once sync.Once
	out  io.Writer
	buf  []byte
}

func newReadyWatcher(out io.Writer) *readyWatcher {
	return &readyWatcher{ch: make(chan struct{}), out: out}
}

// maxStdoutLine bounds how much of one stdout line is held while waiting for
// its newline.
const maxStdoutLine = 4096

func (w *readyWatcher) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := w.buf[:i]
		if string(line) == readyLine {
			w.once.Do(func() { close(w.ch) })
		} else {
			_, _ = w.out.Write(w.buf[:i+1])
		}
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > maxStdoutLine {
		_, _ = w.out.Write(w.buf)
		w.buf = w.buf[:0]
	}
	return len(p), nil
}

// lockedWriter serializes writes to w.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
