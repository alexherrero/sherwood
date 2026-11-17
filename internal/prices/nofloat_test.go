package prices

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoFloatInPrices fails if a non-test file in this package or its
// subpackages names a float type or calls anything with Float in its name, so
// a price can never pass through floating point on its way in.
func TestNoFloatInPrices(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "testdata" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
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
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
