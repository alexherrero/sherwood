package broker

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// shortSocketPath returns a socket path in a fresh temporary directory. Unix
// socket paths are limited to about 100 bytes, and t.TempDir's paths can be
// longer than that on macOS.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "bh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "h.sock")
}

// fakeHelper listens on a fresh socket and runs serve for every connection.
// It returns the socket path.
func fakeHelper(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	path := shortSocketPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = conn.Close() }()
				serve(conn)
			}()
		}
	}()
	return path
}

// fakeClock hands each After call to the test, which decides when it fires.
type fakeClock struct {
	waits chan fakeWait
}

type fakeWait struct {
	d  time.Duration
	ch chan time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{waits: make(chan fakeWait, 16)} }

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	w := fakeWait{d: d, ch: make(chan time.Time, 1)}
	c.waits <- w
	return w.ch
}

// syncBuffer is a bytes.Buffer safe to read while the supervisor writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// within bounds how long a test waits for something the code under test
// should do promptly. It is a safety net against hangs, not a sleep.
const within = 10 * time.Second

func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(within):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}
