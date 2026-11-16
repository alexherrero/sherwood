// Package loggingtest gives tests a logger built on the real allowlist that
// fails the test when code logs a field the allowlist refuses.
package loggingtest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/alexherrero/sherwood/internal/logging"
)

// TB is the part of testing.TB the logger needs.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
}

// New returns a Debug-level logger with the real allowlist that calls
// t.Errorf for every refused field, and the output it writes.
func New(t TB) (*slog.Logger, *Output) {
	t.Helper()
	out := &Output{}
	logger := logging.New(out, &logging.Options{
		Level: slog.LevelDebug,
		OnRefused: func(r logging.Refusal) {
			t.Errorf("logging: field %q refused: %s", r.Key, r.Reason)
		},
	})
	return logger, out
}

// Output collects log lines. It is safe for concurrent use.
type Output struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p.
func (o *Output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

// String returns everything written so far.
func (o *Output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

// Lines decodes each JSON line written so far.
func (o *Output) Lines() ([]map[string]any, error) {
	var lines []map[string]any
	sc := bufio.NewScanner(strings.NewReader(o.String()))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			return nil, err
		}
		lines = append(lines, m)
	}
	return lines, sc.Err()
}
