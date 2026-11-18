package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// routePath turns a registered read pattern into a path that reaches it.
func routePath(pattern string) string {
	if pattern == "/{$}" {
		return "/"
	}
	return pattern
}

// shellRoutes are the read patterns that serve the shell, not data.
var shellRoutes = []string{staticPattern, workerPath}

// dataRoutes are every read route the server registered except the shell's.
func (f *fixture) dataRoutes() []string {
	var out []string
	for _, p := range f.srv.reads {
		if !slices.Contains(shellRoutes, p) {
			out = append(out, routePath(p))
		}
	}
	return out
}

var shellListRE = regexp.MustCompile(`(?s)const SHELL = \[(.*?)\];`)
var quotedRE = regexp.MustCompile(`'([^']*)'`)

// cacheList reads the service worker's cache list from the served file.
func (f *fixture) cacheList() (list []string, worker string) {
	f.t.Helper()
	w := f.do(req{method: http.MethodGet, path: workerPath})
	if w.Code != http.StatusOK {
		f.t.Fatalf("GET %s = %d", workerPath, w.Code)
	}
	worker = w.Body.String()
	m := shellListRE.FindStringSubmatch(worker)
	if m == nil {
		f.t.Fatal("the served worker has no SHELL list")
	}
	for _, q := range quotedRE.FindAllStringSubmatch(m[1], -1) {
		list = append(list, q[1])
	}
	if len(list) == 0 {
		f.t.Fatal("the worker's SHELL list is empty")
	}
	return list, worker
}

func TestTheServiceWorkerCachesOnlyTheShell(t *testing.T) {
	f := newFixture(t)
	list, worker := f.cacheList()
	data := f.dataRoutes()

	// Every view and the health check are data routes.
	for _, v := range views {
		if !slices.Contains(data, v.path) {
			t.Errorf("the view %s is not among the registered data routes %v", v.path, data)
		}
	}
	if !slices.Contains(data, "/health") {
		t.Errorf("/health is not among the registered data routes %v", data)
	}

	for _, entry := range list {
		if slices.Contains(data, entry) {
			t.Errorf("the worker caches %s, a data route", entry)
		}
		r, _ := http.NewRequest(http.MethodGet, entry, nil)
		if _, pattern := f.srv.mux.Handler(r); pattern != http.MethodGet+" "+staticPattern {
			t.Errorf("the worker caches %s, which is served by %q, not the static files", entry, pattern)
		}
		w := f.do(req{method: http.MethodGet, path: entry})
		if w.Code != http.StatusOK {
			t.Errorf("GET %s (in the cache list) = %d", entry, w.Code)
		}
	}

	// The list is the whole shell, so the worker never needs to store anything
	// at run time, and it never does.
	for name := range shell.static {
		if !slices.Contains(list, staticPrefix+name) {
			t.Errorf("static/%s is not in the worker's cache list", name)
		}
	}
	if strings.Contains(worker, ".put(") {
		t.Error("the worker stores responses at run time; it must cache only its SHELL list")
	}
	if n := strings.Count(worker, ".addAll("); n != 1 || !strings.Contains(worker, ".addAll(SHELL)") {
		t.Error("the worker must fill its cache only with addAll(SHELL)")
	}
	if strings.Contains(worker, shellVersionMark) {
		t.Error("the served worker still has its version mark")
	}
}

func TestEveryDataResponseIsNoStore(t *testing.T) {
	f := newFixture(t)
	check := func(what string, w interface{ Header() http.Header }) {
		t.Helper()
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", what, got)
		}
	}
	for _, p := range f.dataRoutes() {
		if p == "/boom" {
			continue // the panicking test view is checked below
		}
		for _, m := range []string{http.MethodGet, http.MethodHead} {
			w := f.do(req{method: m, path: p})
			if w.Code != http.StatusOK {
				t.Errorf("%s %s = %d", m, p, w.Code)
			}
			check(m+" "+p, w)
		}
	}
	check("a failing view", f.do(req{method: http.MethodGet, path: "/boom"}))
	check("an unknown page", f.do(req{method: http.MethodGet, path: "/nothing-here"}))
	check("an unknown static file", f.do(req{method: http.MethodGet, path: "/static/nothing.css"}))
	check("a refused login", f.do(req{method: http.MethodGet, path: "/static/app.css", noLogin: true}))
	c, token := f.session()
	check("an action", f.post("/action", token, c, nil))
	check("a refused action", f.post("/action", "", c, nil))
}

func TestShellFilesCarryTheirOwnCacheRule(t *testing.T) {
	f := newFixture(t)
	paths := []string{workerPath}
	for name := range shell.static {
		paths = append(paths, staticPrefix+name)
	}
	for _, p := range paths {
		w := f.do(req{method: http.MethodGet, path: p})
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d", p, w.Code)
			continue
		}
		h := w.Header()
		if got := h.Get("Cache-Control"); got != shellCacheControl {
			t.Errorf("%s: Cache-Control = %q, want %q", p, got, shellCacheControl)
		}
		if h.Get("Content-Type") == "" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: Content-Type = %q, nosniff = %q", p, h.Get("Content-Type"), h.Get("X-Content-Type-Options"))
		}
		etag := h.Get("ETag")
		if etag == "" {
			t.Errorf("%s has no ETag", p)
			continue
		}
		again := f.do(req{method: http.MethodGet, path: p, headers: map[string][]string{"If-None-Match": {etag}}})
		if again.Code != http.StatusNotModified {
			t.Errorf("%s with its own ETag = %d, want 304", p, again.Code)
		}
	}
	if got := f.do(req{method: http.MethodGet, path: workerPath}).Header().Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
		t.Errorf("the worker's Content-Type = %q", got)
	}
}

func TestTheManifestMakesTheAppInstallable(t *testing.T) {
	f := newFixture(t)
	w := f.do(req{method: http.MethodGet, path: "/static/manifest.webmanifest"})
	if ct := w.Header().Get("Content-Type"); ct != "application/manifest+json" {
		t.Errorf("manifest Content-Type = %q", ct)
	}
	var m struct {
		Name            string `json:"name"`
		ShortName       string `json:"short_name"`
		StartURL        string `json:"start_url"`
		Scope           string `json:"scope"`
		Display         string `json:"display"`
		ThemeColor      string `json:"theme_color"`
		BackgroundColor string `json:"background_color"`
		Icons           []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Type    string `json:"type"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("the manifest is not JSON: %v", err)
	}

	if m.Name != "Sherwood" || m.ShortName == "" {
		t.Errorf("name = %q, short_name = %q", m.Name, m.ShortName)
	}
	if m.Display != "standalone" {
		t.Errorf("display = %q, want standalone", m.Display)
	}
	if m.StartURL != "/" || m.Scope != "/" {
		t.Errorf("start_url = %q, scope = %q, want / and /", m.StartURL, m.Scope)
	}
	if m.ThemeColor == "" || m.BackgroundColor == "" {
		t.Error("the manifest has no theme or background color")
	}

	// Android needs 192 and 512 PNGs and a maskable icon; each must be served
	// at the size it claims.
	sizes := map[string]bool{}
	maskable := false
	for _, ic := range m.Icons {
		if ic.Purpose == "maskable" {
			maskable = true
		}
		if ic.Type != "image/png" {
			continue
		}
		var want, height int
		if _, err := fmt.Sscanf(ic.Sizes, "%dx%d", &want, &height); err != nil || want != height {
			t.Errorf("icon %s has sizes %q", ic.Src, ic.Sizes)
			continue
		}
		checkPNG(t, f, ic.Src, want)
		sizes[ic.Sizes] = true
	}
	for _, s := range []string{"192x192", "512x512"} {
		if !sizes[s] {
			t.Errorf("the manifest has no %s PNG icon", s)
		}
	}
	if !maskable {
		t.Error("the manifest has no maskable icon")
	}
	// iPhone takes its home-screen icon from apple-touch-icon, 180 pixels.
	checkPNG(t, f, "/static/apple-touch-icon.png", 180)
}

func checkPNG(t *testing.T, f *fixture, path string, size int) {
	t.Helper()
	w := f.do(req{method: http.MethodGet, path: path})
	if w.Code != http.StatusOK {
		t.Errorf("GET %s = %d", path, w.Code)
		return
	}
	img, err := png.Decode(w.Body)
	if err != nil {
		t.Errorf("%s: %v", path, err)
		return
	}
	if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
		t.Errorf("%s is %dx%d, want %dx%d", path, b.Dx(), b.Dy(), size, size)
	}
}

var (
	scriptTagRE = regexp.MustCompile(`<script\b[^>]*>`)
	currentRE   = regexp.MustCompile(`<a href="([^"]*)" aria-current="page">`)
)

func TestEveryViewRendersInTheLayout(t *testing.T) {
	f := newFixture(t)
	for _, v := range views {
		w := f.do(req{method: http.MethodGet, path: v.path})
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d", v.path, w.Code)
			continue
		}
		if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("%s: Content-Type = %q", v.path, ct)
		}
		body := w.Body.String()
		for _, want := range []string{
			"<h1>" + v.name + "</h1>",
			`<title>` + v.name + ` · Sherwood</title>`,
			`<link rel="manifest" href="/static/manifest.webmanifest">`,
			`<link rel="apple-touch-icon" href="/static/apple-touch-icon.png">`,
			`<link rel="stylesheet" href="/static/app.css">`,
			`<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">`,
			`<nav class="views" aria-label="Views">`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s is missing %s", v.path, want)
			}
		}
		if v.content == "placeholder" && !strings.Contains(body, "Not built yet.") {
			t.Errorf("%s does not say it isn't built yet", v.path)
		}
		// Every view links to all six, and marks only itself as current.
		for _, n := range views {
			if !strings.Contains(body, `<a href="`+n.path+`"`) {
				t.Errorf("%s has no link to %s", v.path, n.path)
			}
		}
		cur := currentRE.FindAllStringSubmatch(body, -1)
		if len(cur) != 1 || cur[0][1] != v.path {
			t.Errorf("%s marks %v as current", v.path, cur)
		}
		// The CSP forbids inline script and style, so the page has none.
		for _, tag := range scriptTagRE.FindAllString(body, -1) {
			if !strings.Contains(tag, ` src="/static/`) {
				t.Errorf("%s has an inline script: %s", v.path, tag)
			}
		}
		if strings.Contains(body, "<style") || strings.Contains(body, " style=") {
			t.Errorf("%s has inline style", v.path)
		}
		if strings.Contains(body, "user-scalable") || strings.Contains(body, "maximum-scale") {
			t.Errorf("%s stops the reader zooming", v.path)
		}
	}
}

func TestAFailingViewIsAServerErrorWithNoPartialPage(t *testing.T) {
	f := newFixture(t)
	f.srv.HandleRead("/broken", f.srv.viewHandler(view{
		path: "/broken", name: "Broken", content: "placeholder",
		load: func(*Server, *http.Request) (any, error) { return nil, errors.New("the store is closed") },
	}))
	w := f.do(req{method: http.MethodGet, path: "/broken"})
	if w.Code != http.StatusInternalServerError {
		t.Errorf("GET /broken = %d, want 500", w.Code)
	}
	if body := w.Body.String(); strings.Contains(body, "<nav") || strings.Contains(body, "the store is closed") {
		t.Errorf("the error page is %q", body)
	}
	if !strings.Contains(f.log.String(), `"reason":"the view failed"`) {
		t.Error("the failure was not logged")
	}
}

func TestEveryViewHasATemplate(t *testing.T) {
	seen := map[string]bool{}
	for _, v := range views {
		if _, ok := pages[v.content]; !ok {
			t.Errorf("view %s uses missing template %q", v.path, v.content)
		}
		if seen[v.path] {
			t.Errorf("view %s is listed twice", v.path)
		}
		seen[v.path] = true
	}
	if len(views) != 6 {
		t.Errorf("there are %d views, want 6", len(views))
	}
}
