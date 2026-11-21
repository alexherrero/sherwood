package feed

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// floatUses lists every place a parsed file names a float type or calls
// something with "Float" in its name (decimal.NewFromFloat, d.Float64,
// strconv.ParseFloat, big.Float, ...).
func floatUses(fset *token.FileSet, f *ast.File) []string {
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if x.Name == "float32" || x.Name == "float64" {
				found = append(found, fset.Position(x.Pos()).String()+": "+x.Name)
			}
		case *ast.SelectorExpr:
			if strings.Contains(x.Sel.Name, "Float") {
				found = append(found, fset.Position(x.Pos()).String()+": "+x.Sel.Name)
			}
		}
		return true
	})
	return found
}

// TestNoFloatInFeed fails if float32 or float64, or any Float conversion,
// appears in a non-test file of the feed package (and any package added
// under it). The feed carries closing prices as exact decimals.
func TestNoFloatInFeed(t *testing.T) {
	fset := token.NewFileSet()
	parsed := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		parsed++
		for _, use := range floatUses(fset, f) {
			t.Errorf("float in the feed: %s", use)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsed < 4 {
		t.Fatalf("parsed only %d files; is the walk looking in the right place?", parsed)
	}
}

// TestFloatUsesCatchesEachForm proves the scanner sees every form it is meant
// to refuse, so TestNoFloatInFeed cannot pass by looking at nothing.
func TestFloatUsesCatchesEachForm(t *testing.T) {
	cases := map[string]string{
		"type":        "package p\nvar x float64",
		"float32":     "package p\nfunc f(v float32) {}",
		"conversion":  "package p\nvar x = float64(1)",
		"from float":  "package p\nvar x = decimal.NewFromFloat(1)",
		"to float":    "package p\nvar x, _ = d.Float64()",
		"strconv":     "package p\nvar x, _ = strconv.ParseFloat(\"1\", 64)",
		"type switch": "package p\nfunc f(v any) { switch v.(type) { case float64: } }",
	}
	for name, src := range cases {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name+".go", src, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(floatUses(fset, f)) == 0 {
			t.Errorf("%s: not caught: %s", name, src)
		}
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "clean.go", "package p\nvar x = decimal.New(1, -2)", 0)
	if err != nil {
		t.Fatal(err)
	}
	if uses := floatUses(fset, f); len(uses) != 0 {
		t.Errorf("clean source flagged: %v", uses)
	}
}
