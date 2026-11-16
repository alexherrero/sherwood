package logging_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/logging"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/secret"
)

// planted is an obviously fake credential. Tests check it never appears in
// any log output.
const planted = "fake-token-XYZ789"

// recorder collects refusals.
type recorder struct {
	mu   sync.Mutex
	list []logging.Refusal
}

func (r *recorder) add(x logging.Refusal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, x)
}

func (r *recorder) keys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ks []string
	for _, x := range r.list {
		ks = append(ks, x.Key)
	}
	return ks
}

func newLogger(t *testing.T) (*slog.Logger, *bytes.Buffer, *recorder) {
	t.Helper()
	var buf bytes.Buffer
	rec := &recorder{}
	return logging.New(&buf, &logging.Options{OnRefused: rec.add}), &buf, rec
}

func decodeLines(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %v: %q", err, line)
		}
		out = append(out, m)
	}
	return out
}

func TestWritesAllowlistedFields(t *testing.T) {
	logger, out := loggingtest.New(t)
	when := time.Date(2026, 11, 16, 9, 30, 0, 0, time.UTC)
	logger.Info("job finished",
		"job", "daily-signals",
		"trading_date", "2026-11-16",
		"order_key", "ord-0001",
		"reason", "all rules passed",
		"state", "filled",
		"halt_cause", "none",
		"attempt", 2,
		"status", uint64(200),
		"price", "12.34",
		"duration", 1500*time.Millisecond,
		"migrations", 3,
		"mode", "paper",
		"decision", "allowed",
		slog.Bool("missed_dates", false),
		slog.Time("trading_date", when),
		"error", errors.New("helper did not reply"),
	)
	lines, err := out.Lines()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	l := lines[0]
	want := map[string]any{
		"msg":        "job finished",
		"level":      "INFO",
		"job":        "daily-signals",
		"order_key":  "ord-0001",
		"reason":     "all rules passed",
		"state":      "filled",
		"halt_cause": "none",
		"attempt":    float64(2),
		"status":     float64(200),
		"price":      "12.34",
		"migrations": float64(3),
		"error":      "helper did not reply",
	}
	for k, v := range want {
		if l[k] != v {
			t.Errorf("%s = %#v, want %#v", k, l[k], v)
		}
	}
	if _, ok := l[logging.RefusedKey]; ok {
		t.Errorf("a line with only allowed fields has %s", logging.RefusedKey)
	}
}

func TestUnlistedFieldIsDroppedAndReported(t *testing.T) {
	logger, buf, rec := newLogger(t)
	logger.Info("request", "job", "daily", "authorization", "Bearer "+planted, "jobb", "typo")
	out := buf.String()
	if strings.Contains(out, planted) || strings.Contains(out, "Bearer") || strings.Contains(out, "typo") {
		t.Errorf("a refused value reached the log: %s", out)
	}
	l := decodeLines(t, out)[0]
	if l["job"] != "daily" {
		t.Errorf("the allowed field was lost: %v", l)
	}
	if l[logging.RefusedKey] != "authorization,jobb" {
		t.Errorf("%s = %v, want authorization,jobb", logging.RefusedKey, l[logging.RefusedKey])
	}
	if got := strings.Join(rec.keys(), ","); got != "authorization,jobb" {
		t.Errorf("OnRefused saw %q", got)
	}
}

// fakeTB records whether the test would have failed.
type fakeTB struct {
	mu     sync.Mutex
	errors []string
}

func (f *fakeTB) Helper() {}
func (f *fakeTB) Errorf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
}

func TestLoggingAnUnlistedFieldFailsTheTest(t *testing.T) {
	ft := &fakeTB{}
	logger, out := loggingtest.New(ft)
	logger.Info("ok", "job", "daily")
	if len(ft.errors) != 0 {
		t.Fatalf("an allowed field failed the test: %v", ft.errors)
	}
	logger.Info("bad", "api_key", planted)
	if len(ft.errors) != 1 || !strings.Contains(ft.errors[0], `"api_key"`) {
		t.Fatalf("logging an unlisted field should fail the test naming the key; got %v", ft.errors)
	}
	if strings.Contains(out.String(), planted) {
		t.Errorf("the refused value reached the log: %s", out.String())
	}
}

// stringer is a struct with a String method; such values are refused, so a
// type cannot sneak text into the log without implementing slog.LogValuer.
type stringer struct{ v string }

func (s stringer) String() string { return s.v }

func TestHeadersTokensAndSecretsNeverGetThrough(t *testing.T) {
	header := http.Header{"Authorization": {"Bearer " + planted}, "Cookie": {"session=" + planted}}
	req, err := http.NewRequest(http.MethodGet, "http://example.invalid/feed?token="+planted, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = header
	urlErr := &url.Error{Op: "Get", URL: "http://example.invalid/feed?token=" + planted, Err: errors.New("refused")}
	s := secret.New(planted)

	cases := []struct {
		name  string
		attrs []any
		keep  bool // whether the field is written (as a mask)
	}{
		{"headers under an unlisted key", []any{"headers", header}, false},
		{"headers under an allowed key", []any{"reason", header}, false},
		{"a request under an allowed key", []any{"reason", req}, false},
		{"a URL under an allowed key", []any{"route", req.URL}, false},
		{"a cookie under an allowed key", []any{"reason", &http.Cookie{Name: "s", Value: planted, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}}, false},
		{"a token under an unlisted key", []any{"token", planted}, false},
		{"bytes under an allowed key", []any{"reason", []byte(planted)}, false},
		{"a Stringer under an allowed key", []any{"reason", stringer{planted}}, false},
		{"a slice under an allowed key", []any{"reason", []string{planted}}, false},
		{"a URL error", []any{"error", urlErr}, false},
		{"a wrapped URL error", []any{"error", fmt.Errorf("feed upload: %w", urlErr)}, false},
		{"a secret under an unlisted key", []any{"password", s}, false},
		{"a secret under an allowed key", []any{"new_value", s}, true},
		{"a secret pointer under an allowed key", []any{"new_value", &s}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logger, buf, rec := newLogger(t)
			logger.Info("x", tc.attrs...)
			out := buf.String()
			if strings.Contains(out, planted) || strings.Contains(out, "XYZ789") {
				t.Fatalf("the token reached the log: %s", out)
			}
			l := decodeLines(t, out)[0]
			key := tc.attrs[0].(string)
			if tc.keep {
				if l[key] != secret.Mask {
					t.Errorf("%s = %v, want the mask", key, l[key])
				}
				if len(rec.keys()) != 0 {
					t.Errorf("unexpected refusals %v", rec.keys())
				}
				return
			}
			if _, ok := l[key]; ok {
				t.Errorf("the refused field %q was written: %s", key, out)
			}
			if l[logging.RefusedKey] != key || len(rec.keys()) != 1 {
				t.Errorf("the refusal of %q was not reported: line %v, OnRefused %v", key, l[logging.RefusedKey], rec.keys())
			}
		})
	}
}

func TestGroupsAreRefused(t *testing.T) {
	logger, buf, rec := newLogger(t)
	logger.WithGroup("req").Info("a", "status", 200)
	logger.Info("b", slog.Group("req", "status", 200, "header", "x"))
	logger.Info("c", slog.Group("", "status", 200, "cookie", "x"))
	logger.WithGroup("").Info("d", "status", 200)

	lines := decodeLines(t, buf.String())
	if _, ok := lines[0]["req"]; ok || lines[0][logging.RefusedKey] != "req.status" {
		t.Errorf("WithGroup: %v", lines[0])
	}
	if _, ok := lines[1]["req"]; ok || lines[1][logging.RefusedKey] != "req" {
		t.Errorf("a named group: %v", lines[1])
	}
	if lines[2]["status"] != float64(200) || lines[2][logging.RefusedKey] != "cookie" {
		t.Errorf("an inline group should be checked field by field: %v", lines[2])
	}
	if lines[3]["status"] != float64(200) {
		t.Errorf("WithGroup(\"\") should change nothing: %v", lines[3])
	}
	if got := strings.Join(rec.keys(), ","); got != "req.status,req,cookie" {
		t.Errorf("OnRefused saw %q", got)
	}
}

func TestWithAttrs(t *testing.T) {
	logger, buf, rec := newLogger(t)
	l := logger.With("job", "daily", "cookie", planted)
	l.Info("one", "state", "open")
	l.Info("two", "x_forwarded_for", "127.0.0.1")
	out := buf.String()
	if strings.Contains(out, planted) {
		t.Fatalf("the token reached the log: %s", out)
	}
	lines := decodeLines(t, out)
	if lines[0]["job"] != "daily" || lines[0]["state"] != "open" || lines[0][logging.RefusedKey] != "cookie" {
		t.Errorf("line one: %v", lines[0])
	}
	if lines[1]["job"] != "daily" || lines[1][logging.RefusedKey] != "cookie,x_forwarded_for" {
		t.Errorf("line two: %v", lines[1])
	}
	// The With refusal is reported once, when With is called.
	if got := strings.Join(rec.keys(), ","); got != "cookie,x_forwarded_for" {
		t.Errorf("OnRefused saw %q", got)
	}
}

func TestRefusedKeyCannotBeSpoofed(t *testing.T) {
	logger, buf, _ := newLogger(t)
	logger.Info("x", logging.RefusedKey, "nothing")
	l := decodeLines(t, buf.String())[0]
	if l[logging.RefusedKey] != logging.RefusedKey {
		t.Errorf("a caller-set %s should itself be refused: %v", logging.RefusedKey, l)
	}
}

func TestBuiltInKeysCannotBeOverwritten(t *testing.T) {
	logger, buf, rec := newLogger(t)
	logger.Info("real", "msg", "fake", "level", "fake", "time", "fake")
	if got := strings.Join(rec.keys(), ","); got != "msg,level,time" {
		t.Errorf("OnRefused saw %q", got)
	}
	if strings.Contains(buf.String(), `"fake"`) {
		t.Errorf("a refused value reached the log: %s", buf.String())
	}
}

func TestLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, &logging.Options{Level: slog.LevelWarn})
	logger.Info("quiet", "job", "a")
	logger.Warn("loud", "job", "b")
	lines := decodeLines(t, buf.String())
	if len(lines) != 1 || lines[0]["job"] != "b" {
		t.Errorf("got %v", lines)
	}
	buf.Reset()
	logging.New(&buf, nil).Debug("hidden")
	if buf.Len() != 0 {
		t.Errorf("the default level should be Info: %s", buf.String())
	}
}

func TestConcurrentLogging(t *testing.T) {
	logger, out := loggingtest.New(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.With("job", "j").Info("x", "attempt", i)
		}()
	}
	wg.Wait()
	lines, err := out.Lines()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 20 {
		t.Errorf("got %d lines, want 20", len(lines))
	}
}

// TestAllowlistStaysReviewable guards the list itself: flat snake_case keys,
// no duplicates, nothing that names a credential, and none of slog's built-in
// keys or the refusal key.
func TestAllowlistStaysReviewable(t *testing.T) {
	snake := regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	banned := []string{"token", "secret", "password", "passwd", "header", "cookie", "auth", "session", "url", "body", "key_id", "api_key", "mfa", "otp", "credential"}
	for _, k := range logging.AllowedKeys() {
		if !snake.MatchString(k) {
			t.Errorf("%q is not flat snake_case", k)
		}
		for _, b := range banned {
			if strings.Contains(k, b) {
				t.Errorf("%q names a credential (%q)", k, b)
			}
		}
		switch k {
		case slog.TimeKey, slog.LevelKey, slog.MessageKey, slog.SourceKey, logging.RefusedKey:
			t.Errorf("%q collides with a key the logger writes itself", k)
		}
		if !logging.Allowed(k) {
			t.Errorf("Allowed(%q) is false", k)
		}
	}
	seen := map[string]bool{}
	for _, k := range logging.AllowedKeys() {
		if seen[k] {
			t.Errorf("%q is listed twice", k)
		}
		seen[k] = true
	}
	for _, k := range []string{"job", "trading_date", "order_key", "reason", "state", "halt_cause"} {
		if !logging.Allowed(k) {
			t.Errorf("the design's logging plan names %q, but it is not allowed", k)
		}
	}
}
