package feed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFreshEndsAtStaleAfter(t *testing.T) {
	at := time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		now  time.Time
		want bool
	}{
		"just arrived":        {at, true},
		"a day later":         {at.Add(24 * time.Hour), true},
		"exactly at the edge": {at.Add(StaleAfter), true},
		"just past the edge":  {at.Add(StaleAfter + time.Nanosecond), false},
		"three days later":    {at.Add(72 * time.Hour), false},
		"clock behind upload": {at.Add(-time.Minute), true},
	}
	for name, c := range cases {
		if got := Fresh(at, c.now); got != c.want {
			t.Errorf("%s: Fresh = %v, want %v", name, got, c.want)
		}
	}
}

// tradingPath are the packages that make, judge, send or schedule orders,
// fetch prices, run the rules or send alerts. None of them may reach the feed,
// so the feed's age, fresh or stale, can change no order, check, job or
// alert. Only the web views (and cmd/sherwood's research subcommands) read it.
var tradingPath = []string{
	"internal/backtest",
	"internal/broker",
	"internal/execution",
	"internal/fill",
	"internal/halt",
	"internal/holdings",
	"internal/limits",
	"internal/notify",
	"internal/orders",
	"internal/prices",
	"internal/proposal",
	"internal/risk",
	"internal/rules",
	"internal/scheduler",
	"internal/signals",
}

// pathsTo walks the in-module imports of start (relative to the module root,
// and every package under it), reading non-test files only, and returns each
// chain of imports that reaches target or a package under it.
func pathsTo(root, module, start, target string) ([]string, error) {
	type visit struct {
		rel   string
		chain []string
	}
	var queue []visit
	err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(start)), func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		queue = append(queue, visit{rel: rel, chain: []string{rel}})
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
		if v.rel == target || strings.HasPrefix(v.rel, target+"/") {
			found = append(found, strings.Join(v.chain, " -> "))
			continue
		}
		imports, err := packageImports(filepath.Join(root, filepath.FromSlash(v.rel)))
		if err != nil {
			return nil, err
		}
		for _, imp := range imports {
			if rel, ok := strings.CutPrefix(imp, module+"/"); ok && !seen[rel] {
				queue = append(queue, visit{rel: rel, chain: append(append([]string(nil), v.chain...), rel)})
			}
		}
	}
	return found, nil
}

// TestTheTradingPathNeverReadsTheFeed is why a stale feed changes nothing
// about trading: no package that trades, checks, schedules or alerts imports
// the feed, directly or through another package.
func TestTheTradingPathNeverReadsTheFeed(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, module, err := moduleRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range tradingPath {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Fatalf("%s is not in this repo: %v", p, err)
		}
		found, err := pathsTo(root, module, p, "internal/feed")
		if err != nil {
			t.Fatal(err)
		}
		for _, chain := range found {
			t.Errorf("the trading path reads the feed: %s", chain)
		}
	}
}

// TestPathsToCatchesAPlantedImport proves the walk finds the feed two
// packages deep, so the test above cannot pass by looking at nothing.
func TestPathsToCatchesAPlantedImport(t *testing.T) {
	root := t.TempDir()
	module := "example.test/planted"
	for rel, src := range map[string]string{
		"go.mod":                    "module " + module + "\n",
		"internal/risk/risk.go":     `package risk; import _ "example.test/planted/internal/helper"`,
		"internal/helper/h.go":      `package helper; import _ "example.test/planted/internal/feed"`,
		"internal/feed/feed.go":     `package feed`,
		"internal/orders/o.go":      `package orders; import _ "fmt"`,
		"internal/orders/o_test.go": `package orders; import _ "example.test/planted/internal/feed"`,
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	found, err := pathsTo(root, module, "internal/risk", "internal/feed")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0] != "internal/risk -> internal/helper -> internal/feed" {
		t.Errorf("found %v", found)
	}
	if found, err := pathsTo(root, module, "internal/orders", "internal/feed"); err != nil || len(found) != 0 {
		t.Errorf("a test-only import was flagged: %v, %v", found, err)
	}
}
