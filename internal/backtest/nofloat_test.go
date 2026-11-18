package backtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// floatUses lists every place a parsed file names a float type or calls
// something with "Float" in its name (decimal.NewFromFloat, d.Float64,
// d.InexactFloat64, strconv.ParseFloat, big.Float, ...). It is the same check
// internal/money runs on itself.
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

// TestNoFloatInBackTests fails if float32 or float64, or any Float
// conversion, appears in a non-test file of the back-tester, the paper fill
// model, or the backtest subcommand.
func TestNoFloatInBackTests(t *testing.T) {
	files := []string{"../../cmd/sherwood/backtest.go"}
	for _, dir := range []string{".", "../fill"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
				files = append(files, filepath.Join(dir, name))
			}
		}
	}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, use := range floatUses(fset, f) {
			t.Errorf("float in the back-tester: %s", use)
		}
	}
	if len(files) < 6 {
		t.Fatalf("parsed only %d files; is the walk looking in the right place?", len(files))
	}
}

// TestFloatUsesCatchesEachForm proves the scanner sees every form it is meant
// to refuse, so TestNoFloatInRules cannot pass by looking at nothing.
func TestFloatUsesCatchesEachForm(t *testing.T) {
	for name, src := range map[string]string{
		"type":       "package p\nvar x float64",
		"float32":    "package p\nfunc f(v float32) {}",
		"conversion": "package p\nvar x = float64(1)",
		"from float": "package p\nvar x = decimal.NewFromFloat(1)",
		"to float":   "package p\nvar x = d.InexactFloat64()",
		"strconv":    "package p\nvar x, _ = strconv.ParseFloat(\"1\", 64)",
	} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name+".go", src, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(floatUses(fset, f)) == 0 {
			t.Errorf("%s: not caught: %s", name, src)
		}
	}
}
