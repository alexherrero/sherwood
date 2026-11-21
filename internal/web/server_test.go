package web

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexherrero/sherwood/internal/clock"
	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/halt"
	"github.com/alexherrero/sherwood/internal/logging/loggingtest"
	"github.com/alexherrero/sherwood/internal/notify/notifytest"
	"github.com/alexherrero/sherwood/internal/secret"
	"github.com/alexherrero/sherwood/internal/store"
)

// Placeholders only: a login from the example domain and addresses from the
// start of the tailnet range.
const (
	login       = "you@example.com"
	phoneAddr   = "100.64.0.10"
	laptopAddr  = "100.64.0.11"
	phoneIPv6   = "fd7a:115c:a1e0::10"
	startOfTest = "2026-11-16T15:00:00Z"
	// feedToken is a made-up feed token for tests; the real one lives only
	// in the settings file on the NAS.
	feedToken = "fake-feed-token-for-tests-0123456789abcdef" //nolint:gosec // G101: a made-up token for tests
)

type fixture struct {
	t       *testing.T
	srv     *Server
	store   *store.Store
	clock   *clock.Fake
	log     *loggingtest.Output
	alerts  *notifytest.Fake // what the stop switch sent
	stop    *halt.Switch
	checker *fakeChecker // the risk check approvals run
	changes atomic.Int32 // how many times the test actions ran
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureMode(t, config.ModePaper)
}

// newFixtureMode builds a fixture whose server reports the given trading
// mode, over a fresh, empty database.
func newFixtureMode(t *testing.T, mode config.Mode) *fixture {
	t.Helper()
	return newFixtureWith(t, mode, nil)
}

// newFixtureWith builds a fixture and lets edit change the server's Config
// before the server is built.
func newFixtureWith(t *testing.T, mode config.Mode, edit func(*fixture, *Config)) *fixture {
	t.Helper()
	start, err := time.Parse(time.RFC3339, startOfTest)
	if err != nil {
		t.Fatal(err)
	}
	logger, out := loggingtest.New(t)
	f := &fixture{t: t, store: openStore(t, start), clock: clock.NewFake(start), log: out,
		alerts: &notifytest.Fake{}, checker: &fakeChecker{}}
	f.stop = newSwitch(t, f.store, f.clock, f.alerts)
	cfg := Config{
		Login:           login,
		ApprovalDevices: []netip.Addr{netip.MustParseAddr(phoneAddr), netip.MustParseAddr(phoneIPv6)},
		Clock:           f.clock,
		Logger:          logger,
		Store:           f.store,
		Mode:            mode,
		Switch:          f.stop,
		Strategy:        &config.Strategy{},
		Calendar:        clock.NYSE(),
		Checker:         f.checker,
		BrokerHealthy:   func(context.Context) bool { return true },
		FeedToken:       secret.New(feedToken),
	}
	if edit != nil {
		edit(f, &cfg)
	}
	f.srv, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// A page with a form, and two test actions: one anyone with the login may
	// take, and one only an approval device may take.
	f.srv.HandleRead("/form", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, Token(r)) //nolint:gosec // G705: the token is base64url text this package made, served as plain text
	}))
	count := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.changes.Add(1)
		_, _ = io.WriteString(w, "done")
	})
	f.srv.HandleChange("/action", count)
	f.srv.HandleChange("/approve", count, ApprovalDevicesOnly)
	f.srv.HandleRead("/boom", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("a view failed")
	}))
	return f
}

type req struct {
	method  string
	path    string
	form    url.Values
	headers map[string][]string
	cookie  *http.Cookie
	noLogin bool
}

func (f *fixture) do(rq req) *httptest.ResponseRecorder {
	f.t.Helper()
	var body io.Reader
	if rq.form != nil {
		body = strings.NewReader(rq.form.Encode())
	}
	r := httptest.NewRequest(rq.method, rq.path, body)
	if rq.form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if !rq.noLogin {
		r.Header.Set(headerLogin, login)
	}
	for k, vs := range rq.headers {
		r.Header.Del(k)
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	if rq.cookie != nil {
		r.AddCookie(rq.cookie)
	}
	w := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(w, r)
	return w
}

// session opens the form page and returns the session cookie and the token.
func (f *fixture) session() (*http.Cookie, string) {
	f.t.Helper()
	w := f.do(req{method: http.MethodGet, path: "/form"})
	if w.Code != http.StatusOK {
		f.t.Fatalf("GET /form = %d", w.Code)
	}
	var c *http.Cookie
	for _, k := range w.Result().Cookies() {
		if k.Name == sessionCookieName {
			c = k
		}
	}
	if c == nil {
		f.t.Fatal("no session cookie was set")
	}
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		f.t.Errorf("session cookie = %+v", c)
	}
	return c, w.Body.String()
}

func (f *fixture) post(path, token string, c *http.Cookie, headers map[string][]string) *httptest.ResponseRecorder {
	f.t.Helper()
	form := url.Values{}
	if token != "" {
		form.Set(TokenField, token)
	}
	return f.do(req{method: http.MethodPost, path: path, form: form, cookie: c, headers: headers})
}

func (f *fixture) wantChanges(n int32) {
	f.t.Helper()
	if got := f.changes.Load(); got != n {
		f.t.Errorf("the action ran %d times, want %d", got, n)
	}
}

func fromAddr(a string) map[string][]string { return map[string][]string{headerForwardedFor: {a}} }

func TestRefusesAMissingOrWrongLogin(t *testing.T) {
	f := newFixture(t)
	cases := map[string]req{
		"no login":         {method: http.MethodGet, path: "/health", noLogin: true},
		"empty login":      {method: http.MethodGet, path: "/health", headers: map[string][]string{headerLogin: {""}}},
		"wrong login":      {method: http.MethodGet, path: "/health", headers: map[string][]string{headerLogin: {"someone@example.com"}}},
		"different case":   {method: http.MethodGet, path: "/health", headers: map[string][]string{headerLogin: {"You@example.com"}}},
		"two logins":       {method: http.MethodGet, path: "/health", headers: map[string][]string{headerLogin: {login, login}}},
		"unknown page":     {method: http.MethodGet, path: "/nothing-here", noLogin: true},
		"change, no login": {method: http.MethodPost, path: "/action", form: url.Values{}, noLogin: true},
	}
	for name, rq := range cases {
		t.Run(name, func(t *testing.T) {
			w := f.do(rq)
			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
			if strings.Contains(w.Body.String(), "ok") {
				t.Errorf("the page was served: %q", w.Body.String())
			}
			if len(w.Result().Cookies()) != 0 {
				t.Error("a refused caller was given a session cookie")
			}
			if w.Header().Get("Content-Security-Policy") == "" {
				t.Error("a refusal is missing the security headers")
			}
		})
	}
	f.wantChanges(0)
	if !strings.Contains(f.log.String(), `"reason":"missing or wrong tailnet login"`) {
		t.Errorf("refusals were not logged:\n%s", f.log.String())
	}
}

func TestTheAllowedLoginReadsPages(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/", "/health"} {
		w := f.do(req{method: http.MethodGet, path: path})
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d", path, w.Code)
		}
	}
	w := f.do(req{method: http.MethodHead, path: "/health"})
	if w.Code != http.StatusOK {
		t.Errorf("HEAD /health = %d", w.Code)
	}
	if w := f.do(req{method: http.MethodGet, path: "/nothing-here"}); w.Code != http.StatusNotFound {
		t.Errorf("unknown page = %d, want 404", w.Code)
	}
}

func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	f := newFixture(t)
	w := f.do(req{method: http.MethodGet, path: "/"})
	h := w.Header()
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "object-src 'none'", "frame-ancestors 'none'", "form-action 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q is missing %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP allows inline or eval'd script: %q", csp)
	}
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"X-Frame-Options":        "DENY",
		"Cache-Control":          "no-store",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	// The views' own check for inline script and style is in shell_test.go.
}

func TestAChangeNeedsAValidToken(t *testing.T) {
	f := newFixture(t)
	c, token := f.session()

	if w := f.post("/action", "", c, nil); w.Code != http.StatusForbidden {
		t.Errorf("no token = %d, want 403", w.Code)
	}
	if w := f.post("/action", "not-a-token", c, nil); w.Code != http.StatusForbidden {
		t.Errorf("garbage token = %d, want 403", w.Code)
	}
	if w := f.post("/action", token, nil, nil); w.Code != http.StatusForbidden {
		t.Errorf("token without its session cookie = %d, want 403", w.Code)
	}
	other, _ := f.session()
	if w := f.post("/action", token, other, nil); w.Code != http.StatusForbidden {
		t.Errorf("token from another session = %d, want 403", w.Code)
	}
	// A token whose time has been edited no longer matches its MAC.
	_, sig, _ := strings.Cut(token, ".")
	if w := f.post("/action", "9999999999."+sig, c, nil); w.Code != http.StatusForbidden {
		t.Errorf("edited token = %d, want 403", w.Code)
	}
	f.wantChanges(0)

	if w := f.post("/action", token, c, nil); w.Code != http.StatusOK {
		t.Fatalf("valid token = %d (%s)", w.Code, w.Body.String())
	}
	f.wantChanges(1)
	if !strings.Contains(f.log.String(), `"reason":"missing or stale anti-forgery token"`) {
		t.Errorf("token refusals were not logged:\n%s", f.log.String())
	}
}

func TestAStaleTokenIsRefused(t *testing.T) {
	f := newFixture(t)
	c, token := f.session()
	f.clock.Advance(TokenLifetime)
	if w := f.post("/action", token, c, nil); w.Code != http.StatusOK {
		t.Fatalf("token at exactly its lifetime = %d", w.Code)
	}
	f.clock.Advance(time.Second)
	if w := f.post("/action", token, c, nil); w.Code != http.StatusForbidden {
		t.Errorf("stale token = %d, want 403", w.Code)
	}
	f.wantChanges(1)

	// A token from before a restart is stale too: the new run has a new key.
	g := newFixture(t)
	if w := g.post("/action", token, c, nil); w.Code != http.StatusForbidden {
		t.Errorf("token from an earlier run = %d, want 403", w.Code)
	}
	g.wantChanges(0)
}

func TestATokenFromTheFutureIsRefused(t *testing.T) {
	f := newFixture(t)
	c, _ := f.session()
	future := f.srv.tokens.issue(login, cookieSession(t, c), f.clock.Now().Add(tokenSkew+time.Second))
	if w := f.post("/action", future, c, nil); w.Code != http.StatusForbidden {
		t.Errorf("token from the future = %d, want 403", w.Code)
	}
	f.wantChanges(0)
}

func TestATokenIsTiedToTheLogin(t *testing.T) {
	f := newFixture(t)
	c, _ := f.session()
	other := f.srv.tokens.issue("someone@example.com", cookieSession(t, c), f.clock.Now())
	if w := f.post("/action", other, c, nil); w.Code != http.StatusForbidden {
		t.Errorf("token for another login = %d, want 403", w.Code)
	}
	f.wantChanges(0)
}

func cookieSession(t *testing.T, c *http.Cookie) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(c)
	id, ok := sessionFrom(r)
	if !ok {
		t.Fatal("bad session cookie")
	}
	return id
}

func TestAGetNeverChangesState(t *testing.T) {
	f := newFixture(t)
	c, token := f.session()
	q := "?" + url.Values{TokenField: {token}}.Encode()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, path := range []string{"/action", "/action" + q, "/approve" + q} {
			w := f.do(req{method: method, path: path, cookie: c, headers: fromAddr(phoneAddr)})
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 405", method, path, w.Code)
			}
		}
	}
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, "TRACE"} {
		w := f.do(req{method: method, path: "/action" + q, cookie: c})
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 405", method, w.Code)
		}
	}
	// A token in the query string of a POST does not count either.
	w := f.do(req{method: http.MethodPost, path: "/action" + q, form: url.Values{}, cookie: c})
	if w.Code != http.StatusForbidden {
		t.Errorf("POST with the token only in the query = %d, want 403", w.Code)
	}
	f.wantChanges(0)
}

func TestApprovalChangesComeOnlyFromApprovalDevices(t *testing.T) {
	f := newFixture(t)
	c, token := f.session()
	refused := map[string]map[string][]string{
		"another device":     fromAddr(laptopAddr),
		"no address":         nil,
		"empty address":      fromAddr(""),
		"not an address":     fromAddr("phone"),
		"with a port":        fromAddr(phoneAddr + ":443"),
		"phone added before": fromAddr(phoneAddr + ", " + laptopAddr),
		"phone header first": {headerForwardedFor: {phoneAddr, laptopAddr}},
		"zoned IPv6":         fromAddr(phoneIPv6 + "%eth0"),
	}
	for name, h := range refused {
		if w := f.post("/approve", token, c, h); w.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, w.Code)
		}
	}
	f.wantChanges(0)
	if !strings.Contains(f.log.String(), `"reason":"not an approval device"`) {
		t.Errorf("device refusals were not logged:\n%s", f.log.String())
	}

	for _, h := range []map[string][]string{
		fromAddr(phoneAddr),
		fromAddr(phoneIPv6),
		fromAddr("::ffff:" + phoneAddr),
		fromAddr(laptopAddr + ", " + phoneAddr), // Serve's entry is the last
	} {
		if w := f.post("/approve", token, c, h); w.Code != http.StatusOK {
			t.Errorf("%v: status = %d, want 200", h, w.Code)
		}
	}
	f.wantChanges(4)

	// Other changes are open to every device with the login.
	if w := f.post("/action", token, c, fromAddr(laptopAddr)); w.Code != http.StatusOK {
		t.Errorf("ordinary change from another device = %d", w.Code)
	}
	// And the device check never stands in for the token.
	if w := f.post("/approve", "", c, fromAddr(phoneAddr)); w.Code != http.StatusForbidden {
		t.Errorf("approval without a token = %d", w.Code)
	}
	f.wantChanges(5)
}

func TestACrossSiteFormIsRefused(t *testing.T) {
	f := newFixture(t)
	c, token := f.session()
	for _, site := range []string{"cross-site", "same-site"} {
		w := f.post("/action", token, c, map[string][]string{"Sec-Fetch-Site": {site}})
		if w.Code != http.StatusForbidden {
			t.Errorf("Sec-Fetch-Site %s = %d, want 403", site, w.Code)
		}
	}
	f.wantChanges(0)
	if w := f.post("/action", token, c, map[string][]string{"Sec-Fetch-Site": {"same-origin"}}); w.Code != http.StatusOK {
		t.Errorf("same-origin = %d", w.Code)
	}
}

func TestAnOversizedFormIsRefused(t *testing.T) {
	f := newFixture(t)
	c, token := f.session()
	form := url.Values{TokenField: {token}, "pad": {strings.Repeat("x", MaxFormBytes)}}
	w := f.do(req{method: http.MethodPost, path: "/action", form: form, cookie: c})
	if w.Code != http.StatusBadRequest {
		t.Errorf("oversized form = %d, want 400", w.Code)
	}
	f.wantChanges(0)
}

func TestTheLogNamesTheRouteNeverThePathOrHeaders(t *testing.T) {
	f := newFixture(t)
	c, token := f.session()
	f.do(req{method: http.MethodGet, path: "/health?planted=query-value", cookie: c})
	f.post("/approve?planted=query-value", token, c, fromAddr(laptopAddr))
	f.do(req{method: http.MethodGet, path: "/no/such/planted-path"})
	log := f.log.String()
	for _, want := range []string{`"route":"GET /health"`, `"route":"POST /approve"`, `"route":"unmatched"`, `"method":"GET"`, `"status":200`, `"status":404`} {
		if !strings.Contains(log, want) {
			t.Errorf("log is missing %s:\n%s", want, log)
		}
	}
	for _, leak := range []string{"planted", token, c.Value, laptopAddr, login} {
		if strings.Contains(log, leak) {
			t.Errorf("log carries %q:\n%s", leak, log)
		}
	}
}

func TestAPanickingViewIsAServerError(t *testing.T) {
	f := newFixture(t)
	w := f.do(req{method: http.MethodGet, path: "/boom"})
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "a view failed") || strings.Contains(f.log.String(), "a view failed") {
		t.Error("the panic's text escaped")
	}
	if !strings.Contains(f.log.String(), `"reason":"the handler failed"`) {
		t.Errorf("the failure was not logged:\n%s", f.log.String())
	}
}

func TestTokenOutsideTheServerIsEmpty(t *testing.T) {
	if got := Token(httptest.NewRequest(http.MethodGet, "/", nil)); got != "" {
		t.Errorf("Token = %q", got)
	}
}

func TestNewRefusesAnIncompleteConfig(t *testing.T) {
	logger, _ := loggingtest.New(t)
	clk := clock.NewFake(time.Unix(0, 0))
	st := openStore(t, time.Unix(0, 0))
	paper := config.ModePaper
	sw := newSwitch(t, st, clk, &notifytest.Fake{})
	complete := func(edit func(*Config)) Config {
		c := Config{Login: login, Clock: clk, Logger: logger, Store: st, Mode: paper, Switch: sw,
			Strategy: &config.Strategy{}, Calendar: clock.NYSE(), Checker: &fakeChecker{},
			BrokerHealthy: func(context.Context) bool { return true }, FeedToken: secret.New(feedToken)}
		edit(&c)
		return c
	}
	for name, cfg := range map[string]Config{
		"no login":         complete(func(c *Config) { c.Login = "" }),
		"no clock":         complete(func(c *Config) { c.Clock = nil }),
		"no logger":        complete(func(c *Config) { c.Logger = nil }),
		"short key":        complete(func(c *Config) { c.Key = []byte("short") }),
		"no database":      complete(func(c *Config) { c.Store = nil }),
		"no mode":          complete(func(c *Config) { c.Mode = "" }),
		"unknown mode":     complete(func(c *Config) { c.Mode = "practice" }),
		"no switch":        complete(func(c *Config) { c.Switch = nil }),
		"no strategy file": complete(func(c *Config) { c.Strategy = nil }),
		"no calendar":      complete(func(c *Config) { c.Calendar = nil }),
		"no risk check":    complete(func(c *Config) { c.Checker = nil }),
		"no broker health": complete(func(c *Config) { c.BrokerHealthy = nil }),
		"no feed token":    complete(func(c *Config) { c.FeedToken = secret.Secret{} }),
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: New accepted it", name)
		}
	}
	if _, err := New(complete(func(c *Config) { c.Mode = config.ModeLive })); err != nil {
		t.Errorf("a complete config was refused: %v", err)
	}
}

// newSwitch builds a stop switch over st that sends to alerts.
func newSwitch(t *testing.T, st *store.Store, clk clock.Clock, alerts *notifytest.Fake) *halt.Switch {
	t.Helper()
	logger, _ := loggingtest.New(t)
	sw, err := halt.New(halt.Config{Store: st, Clock: clk, Notifier: alerts, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	return sw
}

// openStore opens a fresh database with every migration applied, closed when
// the test ends.
func openStore(t *testing.T, now time.Time) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sherwood.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	return st
}

// TestServeAnswersAndStopsWhenCancelled runs the real server on a loopback
// port, makes a request through it and shuts it down the way SIGTERM does.
func TestServeAnswersAndStopsWhenCancelled(t *testing.T) {
	f := newFixture(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.srv.Serve(ctx, ln) }()

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ln.Addr().String()+"/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set(headerLogin, login)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "ok\n" {
		t.Errorf("GET /health = %d %q", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve = %v, want nil after a clean shutdown", err)
		}
	case <-time.After(ShutdownTimeout + 5*time.Second):
		t.Fatal("Serve did not return after cancel")
	}
	if c, err := net.Dial("tcp", ln.Addr().String()); err == nil {
		_ = c.Close()
		t.Error("the port still accepts connections after shutdown")
	}
}

func TestServeReportsAFailedListener(t *testing.T) {
	f := newFixture(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	err = f.srv.Serve(context.Background(), ln)
	if err == nil || !errors.Is(err, net.ErrClosed) {
		t.Errorf("Serve = %v, want a closed-listener error", err)
	}
}
