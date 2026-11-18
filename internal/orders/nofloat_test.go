package orders

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestNoFloatInOrders fails if a non-test file in this package names a float
// type or calls anything with Float in its name, so a quantity or price never
// passes through floating point.
func TestNoFloatInOrders(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	parsed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed++
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if x.Name == "float32" || x.Name == "float64" {
					t.Errorf("%s: uses %s", fset.Position(x.Pos()), x.Name)
				}
			case *ast.SelectorExpr:
				if strings.Contains(x.Sel.Name, "Float") {
					t.Errorf("%s: uses %s", fset.Position(x.Pos()), x.Sel.Name)
				}
			}
			return true
		})
	}
	if parsed < 4 {
		t.Fatalf("parsed only %d files; is the walk looking in the right place?", parsed)
	}
}
