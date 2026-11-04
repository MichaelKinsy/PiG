package ordered

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// TestNoAnonymousReflectType rejects an anonymous reflect.Type field. Embedding the interface promotes its method set
// (MethodByName among it) onto the struct; once a reflection consumer such as protobuf is linked, the linker keeps every
// exported method of every reachable type, which grew an embedder's binary by about 4 MB (issue #385).
func TestNoAnonymousReflectType(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range names {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			structType, ok := n.(*ast.StructType)
			if !ok {
				return true
			}
			for _, f := range structType.Fields.List {
				if len(f.Names) != 0 {
					continue
				}
				expr := f.Type
				if star, ok := expr.(*ast.StarExpr); ok {
					expr = star.X
				}
				if sel, ok := expr.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "reflect" && sel.Sel.Name == "Type" {
						t.Errorf("%s: anonymous reflect.Type field; name it", name)
					}
				}
			}
			return true
		})
	}
}
