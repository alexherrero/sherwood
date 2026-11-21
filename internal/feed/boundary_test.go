package feed

import (
	"bufio"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// forbidden are the packages the feed must have no import path to: the
// execution layer and everything that can make, judge or send an order or
// change the stop switch. A package under one of these is forbidden too.
var forbidden = []string{
	"internal/execution",
	"internal/broker",
	"internal/orders",
	"internal/proposal",
	"internal/halt",
	"internal/risk",
}

// moduleRoot finds the directory holding go.mod above dir, and the module
// path it declares.
func moduleRoot(dir string) (root, module string, err error) {
	for d := dir; ; d = filepath.Dir(d) {
		f, err := os.Open(filepath.Join(d, "go.mod")) //nolint:gosec // go.mod in a directory above this test
		if err == nil {
			defer func() { _ = f.Close() }()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				if m, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
					return d, strings.TrimSpace(m), nil
				}
			}
			return "", "", fmt.Errorf("%s/go.mod has no module line", d)
		}
		if filepath.Dir(d) == d {
			return "", "", errors.New("no go.mod above " + dir)
		}
	}
}

// forbiddenPaths walks the transitive imports of the packages in start (paths
// relative to the module root, such as "internal/feed") and every package
// under them, reading each package's non-test Go files whatever their build
// tags. It follows only imports inside module (nothing outside the module can
// import its internal packages) and returns, for each forbidden package
// reached, the chain of imports that reaches it.
func forbiddenPaths(root, module string, start string) ([]string, error) {
	type visit struct {
		rel   string
		chain []string
	}
	var queue []visit
	err := filepath.WalkDir(filepath.Join(root, start), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			queue = append(queue, visit{rel: rel, chain: []string{rel}})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var found []string
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if seen[v.rel] {
			continue
		}
		seen[v.rel] = true
		for _, f := range forbidden {
			if v.rel == f || strings.HasPrefix(v.rel, f+"/") {
				found = append(found, strings.Join(v.chain, " -> "))
			}
		}
		imports, err := packageImports(filepath.Join(root, filepath.FromSlash(v.rel)))
		if err != nil {
			return nil, err
		}
		for _, imp := range imports {
			rel, ok := strings.CutPrefix(imp, module+"/")
			if !ok || seen[rel] {
				continue
			}
			chain := append(append([]string(nil), v.chain...), rel)
			queue = append(queue, visit{rel: rel, chain: chain})
		}
	}
	sort.Strings(found)
	return found, nil
}

// packageImports lists the imports of the non-test Go files in dir.
func packageImports(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			return nil, err
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
	}
	return out, nil
}

// TestNoImportPathToExecution is the design's import boundary: nothing the
// feed package imports, directly or through another package, reaches the
// execution layer.
func TestNoImportPathToExecution(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, module, err := moduleRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "feed", "doc.go")); err != nil {
		t.Fatalf("the walk is not looking at this repo: %v", err)
	}
	found, err := forbiddenPaths(root, module, "internal/feed")
	if err != nil {
		t.Fatal(err)
	}
	for _, chain := range found {
		t.Errorf("the feed has an import path to the execution layer: %s", chain)
	}
}

// TestBoundaryCatchesPlantedImport proves the walk refuses a forbidden import
// planted in a throwaway module, directly and two packages deep, so
// TestNoImportPathToExecution cannot pass by looking at nothing.
func TestBoundaryCatchesPlantedImport(t *testing.T) {
	write := func(root, rel, src string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	module := "example.test/planted"
	cases := map[string]map[string]string{
		"direct": {
			"internal/feed/feed.go":   `package feed; import _ "example.test/planted/internal/execution"`,
			"internal/execution/x.go": `package execution`,
		},
		"two deep": {
			"internal/feed/feed.go":      `package feed; import _ "example.test/planted/internal/web"`,
			"internal/web/web.go":        `package web; import _ "example.test/planted/internal/helper"`,
			"internal/helper/h.go":       `package helper; import _ "example.test/planted/internal/risk"`,
			"internal/risk/risk.go":      `package risk`,
			"internal/unused/u.go":       `package unused`,
			"internal/feed/feed_test.go": `package feed`,
		},
		"sub-package of a forbidden one": {
			"internal/feed/feed.go":      `package feed; import _ "example.test/planted/internal/broker/paper"`,
			"internal/broker/paper/p.go": `package paper`,
		},
		"from a package under feed": {
			"internal/feed/feed.go":     `package feed`,
			"internal/feed/sub/sub.go":  `package sub; import _ "example.test/planted/internal/orders"`,
			"internal/orders/orders.go": `package orders`,
		},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(root, "go.mod", "module "+module+"\n\ngo 1.26\n")
			for rel, src := range files {
				write(root, rel, src)
			}
			r, m, err := moduleRoot(filepath.Join(root, "internal", "feed"))
			if err != nil {
				t.Fatal(err)
			}
			if m != module {
				t.Fatalf("module %q, want %q", m, module)
			}
			found, err := forbiddenPaths(r, m, "internal/feed")
			if err != nil {
				t.Fatal(err)
			}
			if len(found) == 0 {
				t.Fatalf("planted import not caught")
			}
		})
	}

	// And a module with only allowed imports passes.
	root := t.TempDir()
	write(root, "go.mod", "module "+module+"\n")
	write(root, "internal/feed/feed.go", `package feed; import ( _ "fmt"; _ "example.test/planted/internal/store" )`)
	write(root, "internal/store/s.go", `package store`)
	write(root, "internal/feed/x_test.go", `package feed; import _ "example.test/planted/internal/execution"`)
	write(root, "internal/execution/x.go", `package execution`)
	found, err := forbiddenPaths(root, module, "internal/feed")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("clean module flagged: %v", found)
	}
}
