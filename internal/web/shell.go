package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
)

// The app's shell: templates rendered on the server, and the static files a
// phone needs to install the app. Everything is embedded in the binary.
//
//go:embed templates static
var files embed.FS

// swSource is the service worker. It is served from the root, /sw.js, so its
// scope covers every view; a worker served from /static/ could only control
// pages under /static/.
//
//go:embed sw.js
var swSource []byte

// shellVersionMark in sw.js is replaced with a hash of the static files, so a
// change to any of them changes the worker's bytes, the phone installs the
// new worker, and the worker swaps in a fresh cache.
const shellVersionMark = "__SHELL_VERSION__"

// Paths of the shell's own routes. Neither is a data route: the static files
// and the worker are the same for every request.
const (
	staticPrefix  = "/static/"
	staticPattern = staticPrefix + "{file}"
	workerPath    = "/sw.js"
)

// shellCacheControl lets the browser keep a shell file but ask the server
// whether it changed before using it. Data responses keep no-store.
const shellCacheControl = "private, no-cache"

// view is one of the app's six views, in navigation order.
//
// A view not built yet renders the "placeholder" template with a sentence
// saying what it will show. To build one, add templates/views/<name>.html
// defining "content", point the view's content at it, and give it a load
// function that reads the view's data fresh for each request.
type view struct {
	// path is the view's address; Today is the home page.
	path string
	// name is the navigation label and the page title.
	name string
	// content names the template in templates/views that fills the layout.
	content string
	// load returns what content renders, read fresh on every request.
	load func(s *Server, r *http.Request) (any, error)
}

var views = []view{
	{path: "/", name: "Today", content: "today", load: loadToday},
	{path: approvalsPath, name: "Approvals", content: "approvals", load: loadApprovals},
	{path: "/portfolio", name: "Portfolio", content: "portfolio", load: loadPortfolio},
	{path: "/feed", name: "Feed", content: "placeholder", load: describe(
		"candidate cases with their sources, and a button to add each one to your watchlist")},
	{path: ordersPath, name: "Orders", content: "orders", load: loadOrders},
	{path: settingsPath, name: "Settings", content: "settings", load: loadSettings},
}

// describe is the load function of a view not built yet: its data is the
// sentence the placeholder shows.
func describe(what string) func(*Server, *http.Request) (any, error) {
	return func(*Server, *http.Request) (any, error) { return what, nil }
}

// pattern is the mux pattern for a view's path. The home page matches "/"
// exactly, not every path.
func (v view) pattern() string {
	if v.path == "/" {
		return "/{$}"
	}
	return v.path
}

// navItem is one link in the navigation bar.
type navItem struct {
	Path    string
	Name    string
	Current bool
}

// page is what the layout renders.
type page struct {
	// Title is the view's name.
	Title string
	// Nav lists the six views, with the current one marked.
	Nav []navItem
	// Token goes in a hidden csrf_token field on every form.
	Token string
	// Data is what the view's load function returned.
	Data any
}

// pages holds one template set per view template: the layout plus that
// view's "content".
var pages = mustParsePages()

func mustParsePages() map[string]*template.Template {
	p, err := parsePages()
	if err != nil {
		panic(err) // the templates are embedded; a parse error is a build mistake
	}
	return p
}

func parsePages() (map[string]*template.Template, error) {
	layout, err := template.ParseFS(files, "templates/layout.html")
	if err != nil {
		return nil, err
	}
	names, err := fs.Glob(files, "templates/views/*.html")
	if err != nil {
		return nil, err
	}
	out := make(map[string]*template.Template, len(names))
	for _, n := range names {
		t, err := layout.Clone()
		if err != nil {
			return nil, err
		}
		if t, err = t.ParseFS(files, n); err != nil {
			return nil, err
		}
		out[strings.TrimSuffix(path.Base(n), ".html")] = t
	}
	return out, nil
}

// shellFile is one static file, ready to serve.
type shellFile struct {
	body  []byte
	ctype string
	etag  string
}

// shell is the static files by name, and the worker with its version filled
// in.
type shellSet struct {
	static map[string]shellFile
	worker shellFile
}

var shell = mustLoadShell()

var shellTypes = map[string]string{
	".css":         "text/css; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".webmanifest": "application/manifest+json",
}

func mustLoadShell() shellSet {
	s, err := loadShell()
	if err != nil {
		panic(err) // the files are embedded; a missing type is a build mistake
	}
	return s
}

func loadShell() (shellSet, error) {
	entries, err := fs.ReadDir(files, "static")
	if err != nil {
		return shellSet{}, err
	}
	set := shellSet{static: make(map[string]shellFile, len(entries))}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	version := sha256.New()
	for _, n := range names {
		body, err := fs.ReadFile(files, "static/"+n)
		if err != nil {
			return shellSet{}, err
		}
		ctype, ok := shellTypes[path.Ext(n)]
		if !ok {
			return shellSet{}, fmt.Errorf("web: static/%s has no known content type", n)
		}
		set.static[n] = shellFile{body: body, ctype: ctype, etag: etagOf(body)}
		_, _ = fmt.Fprintf(version, "%s\x00%d\x00", n, len(body))
		_, _ = version.Write(body)
	}
	v := hex.EncodeToString(version.Sum(nil))[:16]
	if !bytes.Contains(swSource, []byte(shellVersionMark)) {
		return shellSet{}, fmt.Errorf("web: sw.js has no %s mark", shellVersionMark)
	}
	worker := bytes.ReplaceAll(swSource, []byte(shellVersionMark), []byte(v))
	set.worker = shellFile{body: worker, ctype: shellTypes[".js"], etag: etagOf(worker)}
	return set, nil
}

func etagOf(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:])[:32] + `"`
}

// mountShell registers the six views, the static files and the worker.
func (s *Server) mountShell() {
	for _, v := range views {
		s.HandleRead(v.pattern(), s.viewHandler(v))
	}
	s.HandleRead(staticPattern, http.HandlerFunc(serveStatic))
	s.HandleRead(workerPath, http.HandlerFunc(serveWorker))
}

// viewHandler renders a view in the layout. The page is built in full before
// anything is written, so a failure is a clean error page, never half a page.
func (s *Server) viewHandler(v view) http.Handler {
	t, ok := pages[v.content]
	if !ok {
		panic("web: no template for view content " + v.content)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := v.load(s, r)
		if err != nil {
			refuse(w, r, "the view failed", http.StatusInternalServerError,
				"This page could not be loaded. Try again.")
			return
		}
		nav := make([]navItem, len(views))
		for i, n := range views {
			nav[i] = navItem{Path: n.path, Name: n.name, Current: n.path == v.path}
		}
		var buf bytes.Buffer
		err = t.ExecuteTemplate(&buf, "layout", page{Title: v.name, Nav: nav, Token: Token(r), Data: data})
		if err != nil {
			refuse(w, r, "the view failed", http.StatusInternalServerError,
				"This page could not be loaded. Try again.")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = buf.WriteTo(w)
	})
}

func serveStatic(w http.ResponseWriter, r *http.Request) {
	f, ok := shell.static[r.PathValue("file")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	serveShellFile(w, r, f)
}

func serveWorker(w http.ResponseWriter, r *http.Request) {
	serveShellFile(w, r, shell.worker)
}

// serveShellFile answers with a shell file under its own cache rule, in
// place of the default no-store. ServeContent answers If-None-Match with 304.
func serveShellFile(w http.ResponseWriter, r *http.Request, f shellFile) {
	h := w.Header()
	h.Set("Content-Type", f.ctype)
	h.Set("Cache-Control", shellCacheControl)
	h.Set("ETag", f.etag)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.body))
}
