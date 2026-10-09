package rules

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

type ouLoaded struct {
	pkg   *types.Package
	files []*ast.File
	info  *types.Info
}

// load type-checks a snippet as package x.
func ouLoad(t *testing.T, src string) ouLoaded {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", "package x\n"+src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}}
	pkg, err := (&types.Config{Importer: importer.Default()}).Check("x", fset, []*ast.File{f}, info)
	if err != nil {
		t.Fatal(err)
	}
	return ouLoaded{pkg, []*ast.File{f}, info}
}

func (l ouLoaded) typ(t *testing.T, name string) types.Type {
	t.Helper()
	obj := l.pkg.Scope().Lookup(name)
	if obj == nil {
		t.Fatalf("no declaration %s", name)
	}
	return obj.Type()
}

func (l ouLoaded) named(t *testing.T, name string) *types.Named {
	t.Helper()
	n, ok := l.typ(t, name).(*types.Named)
	if !ok {
		t.Fatalf("%s is not a named type", name)
	}
	return n
}

// field returns the member of struct type structName called fieldName.
func (l ouLoaded) field(t *testing.T, structName, fieldName string) GoMember {
	t.Helper()
	st := l.named(t, structName).Underlying().(*types.Struct)
	for i := 0; i < st.NumFields(); i++ {
		if st.Field(i).Name() == fieldName {
			return GoMember{Type: st.Field(i).Type(), Tag: st.Tag(i)}
		}
	}
	t.Fatalf("no field %s.%s", structName, fieldName)
	return GoMember{}
}
