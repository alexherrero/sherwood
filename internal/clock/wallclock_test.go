package clock

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// wallClockFuncs are the time package functions that read the wall clock.
var wallClockFuncs = map[string]bool{"Now": true, "Since": true, "Until": true}

// wallClockExceptions lists non-test files outside internal/clock allowed to
// read the wall clock, each with the exact number of calls and the reason.
// The count is exact so an exception never quietly widens; drop the entry
// when the call goes.
var wallClockExceptions = map[string]struct {
	calls  int
	reason string
}{
	// The socket deadline for one broker helper call. It times real I/O on a
	// real socket, not a trading decision, and is never given a fake clock.
	"internal/broker/client.go": {1, "socket deadline for one helper call"},
}

// TestNoWallClockOutsideClock fails if any non-test Go file in the module,
// outside internal/clock, calls time.Now, time.Since or time.Until. Rules,
// the risk check and the scheduler must take the time from an injected Clock.
func TestNoWallClockOutsideClock(t *testing.T) {
	root := moduleRoot(t)
	found := map[string][]token.Position{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			name := d.Name()
			if rel != "." && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
				name == "testdata" || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			// A nested go.mod (v1/) is another module with its own rules.
			if rel != "." {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			if rel == "internal/clock" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		if calls := wallClockCalls(fset, f); len(calls) > 0 {
			found[rel] = calls
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 && len(wallClockExceptions) == 0 {
		return
	}
	for rel, calls := range found {
		ex, ok := wallClockExceptions[rel]
		if ok && ex.calls == len(calls) {
			continue
		}
		for _, p := range calls {
			t.Errorf("%s:%d reads the wall clock; take the time from an injected clock.Clock", rel, p.Line)
		}
		if ok {
			t.Errorf("%s has %d wall-clock calls but its exception allows %d (%s)", rel, len(calls), ex.calls, ex.reason)
		}
	}
	for rel := range wallClockExceptions {
		if _, ok := found[rel]; !ok {
			t.Errorf("%s no longer reads the wall clock; remove its entry from wallClockExceptions", rel)
		}
	}
}

// wallClockCalls returns every use of time.Now, time.Since or time.Until in
// f, called or not, under whatever name the file imports "time" as. A dot
// import of "time" is reported too, since its uses cannot be told apart.
func wallClockCalls(fset *token.FileSet, f *ast.File) []token.Position {
	var out []token.Position
	names := map[string]bool{}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != "time" {
			continue
		}
		switch {
		case imp.Name == nil:
			names["time"] = true
		case imp.Name.Name == ".":
			out = append(out, fset.Position(imp.Pos()))
		case imp.Name.Name != "_":
			names[imp.Name.Name] = true
		}
	}
	if len(names) == 0 {
		return out
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && names[pkg.Name] && wallClockFuncs[sel.Sel.Name] {
			out = append(out, fset.Position(sel.Pos()))
		}
		return true
	})
	return out
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}

// The scanner itself must catch every way of reaching the wall clock.
func TestWallClockCallsFindsEveryForm(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"call", `package p; import "time"; var _ = time.Now()`, 1},
		{"since and until", `package p; import "time"; var t0 time.Time; var _, _ = time.Since(t0), time.Until(t0)`, 2},
		{"method value", `package p; import "time"; var now = time.Now`, 1},
		{"renamed import", `package p; import tm "time"; var _ = tm.Now()`, 1},
		{"dot import", `package p; import . "time"; var _ = Now()`, 1},
		{"other time functions", `package p; import "time"; var _ = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); var _ = time.Unix(0, 0)`, 0},
		{"another package's Now", `package p; import "example.com/clock"; var _ = clock.Now()`, 0},
		{"a field named Now", `package p; type c struct{ Now int }; var x c; var _ = x.Now`, 0},
	}
	for _, c := range cases {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, c.name+".go", c.src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := len(wallClockCalls(fset, f)); got != c.want {
			t.Errorf("%s: found %d, want %d", c.name, got, c.want)
		}
	}
}
