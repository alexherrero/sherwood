package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/store"
)

// covered is a time the holiday table covers, in November so next year's
// table is not yet required.
var covered = time.Date(2026, time.November, 16, 15, 0, 0, 0, time.UTC)

// exampleFeedToken is the feed token in settings.example.yaml. It must never
// reach a log line or an error.
const exampleFeedToken = "replace-me-with-a-long-random-feed-token"

// files writes copies of the example settings and strategy files into a temp
// folder, with the data folder and database moved into it. edit, when set,
// changes each file's text before it is written.
type files struct {
	dir, settings, strategy, database string
}

func writeExamples(t *testing.T, editSettings, editStrategy func(string) string) files {
	t.Helper()
	dir := t.TempDir()
	f := files{
		dir:      dir,
		settings: filepath.Join(dir, "settings.yaml"),
		strategy: filepath.Join(dir, "strategy.yaml"),
		database: filepath.Join(dir, "sherwood.db"),
	}
	settings := readRepoFile(t, "settings.example.yaml")
	settings = replaceOnce(t, settings, "data_dir: /data\n", "data_dir: "+dir+"\n")
	settings = replaceOnce(t, settings, "database_path: /data/sherwood.db\n", "database_path: "+f.database+"\n")
	strategy := readRepoFile(t, "strategy.example.yaml")
	if editSettings != nil {
		settings = editSettings(settings)
	}
	if editStrategy != nil {
		strategy = editStrategy(strategy)
	}
	for path, text := range map[string]string{f.settings: settings, f.strategy: strategy} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", name)) //nolint:gosec // a fixed example file in this repo
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func replaceOnce(t *testing.T, s, old, repl string) string {
	t.Helper()
	if strings.Count(s, old) != 1 {
		t.Fatalf("expected exactly one %q in the example file", old)
	}
	return strings.Replace(s, old, repl, 1)
}

// dropLimit removes the line that sets the named limit.
func dropLimit(t *testing.T, name string) func(string) string {
	return func(s string) string {
		var out []string
		dropped := 0
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), name+":") {
				dropped++
				continue
			}
			out = append(out, line)
		}
		if dropped != 1 {
			t.Fatalf("expected one %s line, found %d", name, dropped)
		}
		return strings.Join(out, "\n")
	}
}

// watchWriter is a log writer a test can wait on.
type watchWriter struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	want map[string]chan struct{}
}

func newWatchWriter(wants ...string) *watchWriter {
	w := &watchWriter{want: map[string]chan struct{}{}}
	for _, s := range wants {
		w.want[s] = make(chan struct{})
	}
	return w
}

func (w *watchWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, _ := w.buf.Write(p)
	for s, ch := range w.want {
		if strings.Contains(w.buf.String(), s) {
			close(ch)
			delete(w.want, s)
		}
	}
	return n, nil
}

func (w *watchWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *watchWriter) wait(t *testing.T, s string) {
	t.Helper()
	w.mu.Lock()
	ch, ok := w.want[s]
	w.mu.Unlock()
	if !ok {
		return // already seen
	}
	select {
	case <-ch:
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for %q in the log:\n%s", s, w.String())
	}
}

func testEnv(f files, out io.Writer, clk clock.Clock) env {
	return env{
		args:   []string{"-settings", f.settings, "-strategy", f.strategy},
		getenv: func(string) string { return "" },
		stdout: out,
		stderr: io.Discard,
		clock:  clk,
	}
}

// startAndStop runs sherwood until it reports started, then cancels it the
// way SIGTERM does, and returns run's error and the log.
func startAndStop(t *testing.T, e env) (string, error) {
	t.Helper()
	var out bytes.Buffer
	if e.stdout == nil {
		e.stdout = &out
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.started = cancel
	err := run(ctx, e)
	return e.stdout.(interface{ String() string }).String(), err
}

func TestStartsInPaperModeAndStopsCleanly(t *testing.T) {
	f := writeExamples(t, nil, nil)
	e := testEnv(f, nil, clock.NewFake(covered))
	log, err := startAndStop(t, e)
	if err != nil {
		t.Fatalf("run: %v\nlog:\n%s", err, log)
	}

	strategy, err := config.LoadStrategy(f.strategy)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"msg":"strategy file loaded"`,
		`"strategy_hash":"` + strategy.Hash + `"`,
		`"msg":"database ready","migrations":1`,
		`"msg":"paper mode: the broker helper is not started","mode":"paper"`,
		`"msg":"sherwood started","version":"dev","mode":"paper"`,
		`"msg":"sherwood stopped","reason":"shutdown requested"`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log is missing %s:\n%s", want, log)
		}
	}
	if strings.Contains(log, "broker helper ready") {
		t.Error("paper mode started the broker helper")
	}
	if strings.Contains(log, "refused_fields") {
		t.Errorf("start-up logged a field the allowlist refuses:\n%s", log)
	}
	if strings.Contains(log, exampleFeedToken) {
		t.Error("the feed token reached the log")
	}
	if _, err := os.Stat(f.database); err != nil {
		t.Errorf("database was not created: %v", err)
	}

	// A second start reuses the migrated database.
	log, err = startAndStop(t, testEnv(f, nil, clock.NewFake(covered)))
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !strings.Contains(log, `"msg":"database ready","migrations":0`) {
		t.Errorf("second start re-applied migrations:\n%s", log)
	}
}

func TestRefusesToStartWhenALimitIsMissing(t *testing.T) {
	for _, limit := range []string{"max_order_value", "daily_loss_limit", "max_holdings", "min_paper_weeks"} {
		t.Run(limit, func(t *testing.T) {
			f := writeExamples(t, nil, dropLimit(t, limit))
			log, err := startAndStop(t, testEnv(f, nil, clock.NewFake(covered)))
			if !errors.Is(err, config.ErrInvalid) {
				t.Fatalf("err = %v, want config.ErrInvalid", err)
			}
			if want := "limits." + limit + " is missing"; !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name the limit (want %q)", err, want)
			}
			if !strings.HasPrefix(err.Error(), "refusing to start: ") {
				t.Errorf("error %q does not say it refused to start", err)
			}
			if strings.Contains(log, "sherwood started") {
				t.Error("it started anyway")
			}
			if _, err := os.Stat(f.database); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the database was opened before validation finished: %v", err)
			}
		})
	}
}

func TestRefusesToStartWithInvalidSettingsWithoutRevealingSecrets(t *testing.T) {
	// Drop the mode line: validation fails on a file that holds secrets.
	f := writeExamples(t, func(s string) string {
		return replaceOnce(t, s, "mode: paper\n", "")
	}, nil)
	log, err := startAndStop(t, testEnv(f, nil, clock.NewFake(covered)))
	if !errors.Is(err, config.ErrInvalid) {
		t.Fatalf("err = %v, want config.ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "mode") {
		t.Errorf("error %q does not name the field", err)
	}
	for _, s := range []string{err.Error(), log} {
		if strings.Contains(s, exampleFeedToken) || strings.Contains(s, "replace-me-with-the-vapid-private-key") {
			t.Errorf("a secret reached the output: %s", s)
		}
	}
}

func TestRefusesToStartWhenAFileIsMissing(t *testing.T) {
	for _, name := range []string{"settings", "strategy"} {
		t.Run(name, func(t *testing.T) {
			f := writeExamples(t, nil, nil)
			absent := filepath.Join(f.dir, "absent.yaml")
			if name == "settings" {
				f.settings = absent
			} else {
				f.strategy = absent
			}
			_, err := startAndStop(t, testEnv(f, nil, clock.NewFake(covered)))
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("err = %v, want os.ErrNotExist", err)
			}
			if !strings.Contains(err.Error(), name+" file") {
				t.Errorf("error %q does not say which file", err)
			}
		})
	}
}

func TestRefusesToStartOutsideTheHolidayTable(t *testing.T) {
	f := writeExamples(t, nil, nil)
	for name, now := range map[string]time.Time{
		"uncovered year":         time.Date(2031, time.March, 2, 15, 0, 0, 0, time.UTC),
		"december, next missing": time.Date(2027, time.December, 1, 15, 0, 0, 0, time.UTC),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := startAndStop(t, testEnv(f, nil, clock.NewFake(now)))
			if !errors.Is(err, clock.ErrNotCovered) {
				t.Fatalf("err = %v, want clock.ErrNotCovered", err)
			}
		})
	}
}

func TestRefusesToStartWhenTheDatabaseIsFromANewerBuild(t *testing.T) {
	f := writeExamples(t, nil, nil)
	ctx := context.Background()
	db, err := store.Open(ctx, f.database, covered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (9999, '9999_future.sql', ?)`,
		store.FormatTime(covered)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	log, err := startAndStop(t, testEnv(f, nil, clock.NewFake(covered)))
	if !errors.Is(err, store.ErrMigration) {
		t.Fatalf("err = %v, want store.ErrMigration", err)
	}
	if strings.Contains(log, "sherwood started") {
		t.Error("it started anyway")
	}
}

func TestPathsComeFromTheEnvironment(t *testing.T) {
	f := writeExamples(t, nil, nil)
	e := testEnv(f, nil, clock.NewFake(covered))
	e.args = nil
	e.getenv = func(k string) string {
		return map[string]string{envSettings: f.settings, envStrategy: f.strategy}[k]
	}
	if log, err := startAndStop(t, e); err != nil {
		t.Fatalf("run: %v\n%s", err, log)
	}
}

func TestDefaultPathsAreInTheDataFolder(t *testing.T) {
	o, err := parseOptions(env{getenv: func(string) string { return "" }, stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if o.settingsPath != "/data/settings.yaml" || o.strategyPath != "/data/strategy.yaml" {
		t.Errorf("defaults = %q, %q", o.settingsPath, o.strategyPath)
	}
	if _, err := parseOptions(env{args: []string{"stray"}, getenv: func(string) string { return "" }, stderr: io.Discard}); err == nil {
		t.Error("a stray argument was accepted")
	}
}

// TestLiveModeStartsTheBrokerHelper runs the real helper under live mode. It
// talks to no broker: the helper offers only ping.
func TestLiveModeStartsTheBrokerHelper(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not on PATH")
	}
	src, err := filepath.Abs(filepath.Join("..", "..", "brokerhelper", "src"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PYTHONPATH", src)
	sockDir, err := os.MkdirTemp("", "sw") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })

	f := writeExamples(t, func(s string) string { return replaceOnce(t, s, "mode: paper\n", "mode: live\n") }, nil)
	out := newWatchWriter("broker helper ready")
	e := testEnv(f, out, clock.Real{})
	e.args = append(e.args, "-broker-socket", filepath.Join(sockDir, "b.sock"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, e) }()
	out.wait(t, "broker helper ready")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "paper mode") {
		t.Error("live mode logged paper mode")
	}
}

// TestBinary builds the real binary and checks its exit codes and signal
// handling: it starts with the example files and stops cleanly on SIGTERM,
// and it exits non-zero naming the field when a limit is removed.
func TestBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "sherwood")
	build := exec.Command("go", "build", "-o", bin, ".") //nolint:gosec // bin is in the test's temp folder
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	t.Run("starts and stops on SIGTERM", func(t *testing.T) {
		f := writeExamples(t, nil, nil)
		cmd := exec.Command(bin, "-settings", f.settings, "-strategy", f.strategy) //nolint:gosec // the binary this test just built
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		lines := bufio.NewScanner(stdout)
		var log strings.Builder
		for lines.Scan() {
			log.WriteString(lines.Text() + "\n")
			if strings.Contains(lines.Text(), "sherwood started") {
				break
			}
		}
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		for lines.Scan() {
			log.WriteString(lines.Text() + "\n")
		}
		if err := cmd.Wait(); err != nil {
			t.Fatalf("exit: %v\nstdout:\n%s\nstderr:\n%s", err, log.String(), stderr.String())
		}
		if !strings.Contains(log.String(), "sherwood stopped") {
			t.Errorf("no clean stop logged:\n%s", log.String())
		}
	})

	t.Run("refuses a missing limit", func(t *testing.T) {
		f := writeExamples(t, nil, dropLimit(t, "max_stock_share"))
		cmd := exec.Command(bin, "-settings", f.settings, "-strategy", f.strategy) //nolint:gosec // the binary this test just built
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatalf("exit = %v, want status 1", err)
		}
		if !strings.Contains(stderr.String(), "limits.max_stock_share is missing") {
			t.Errorf("stderr does not name the limit: %s", stderr.String())
		}
	})
}
