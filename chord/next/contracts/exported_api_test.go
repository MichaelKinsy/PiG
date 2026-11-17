package contracts_test

import (
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"sort"
	"testing"
)

// Ports the intent of packages/chord/test/contracts/schema.test.ts "the types of a contract's members are all exported" (Pi
// 5f89cc0a7df6): a package that exports a contract's declaration must be able to NAME every type the contract's members have
// (in TypeScript a type missing from Chord's entry point is written as a path into node_modules, or refused with TS2883). Go has
// no declaration emit, so the counterpart is the one thing that makes a type unnameable here: an exported name of the package
// (a function, method, variable, constant, exported struct field or exported type's underlying type) whose signature mentions an
// unexported type of the package. No lint of the repository checks this (golangci's iface covers interfaces only).
func TestEveryTypeAnExportedNameMentionsIsExported(t *testing.T) {
	context := build.Default
	directory, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	found, err := context.ImportDir(directory, 0)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range found.GoFiles {
		file, err := parser.ParseFile(fset, filepath.Join(directory, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	config := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := config.Check(found.ImportPath, fset, files, nil)
	if err != nil {
		t.Fatal(err)
	}

	var leaks []string
	seen := map[types.Type]bool{}
	var mentions func(owner string, typ types.Type)
	mentions = func(owner string, typ types.Type) {
		if seen[typ] {
			return
		}
		seen[typ] = true
		defer delete(seen, typ)
		switch typ := typ.(type) {
		case *types.Named:
			if object := typ.Obj(); object.Pkg() == pkg && !object.Exported() {
				leaks = append(leaks, owner+" mentions the unexported type "+object.Name())
			}
			for argument := range typ.TypeArgs().Types() {
				mentions(owner, argument)
			}
		case *types.Alias:
			if object := typ.Obj(); object.Pkg() == pkg && !object.Exported() {
				leaks = append(leaks, owner+" mentions the unexported alias "+object.Name())
			}
		case *types.Pointer:
			mentions(owner, typ.Elem())
		case *types.Slice:
			mentions(owner, typ.Elem())
		case *types.Array:
			mentions(owner, typ.Elem())
		case *types.Chan:
			mentions(owner, typ.Elem())
		case *types.Map:
			mentions(owner, typ.Key())
			mentions(owner, typ.Elem())
		case *types.Signature:
			for variable := range typ.Params().Variables() {
				mentions(owner, variable.Type())
			}
			for variable := range typ.Results().Variables() {
				mentions(owner, variable.Type())
			}
		case *types.Struct:
			for field := range typ.Fields() {
				if field.Exported() {
					mentions(owner+"."+field.Name(), field.Type())
				}
			}
		case *types.Interface:
			for method := range typ.ExplicitMethods() {
				if method.Exported() {
					mentions(owner+"."+method.Name(), method.Type())
				}
			}
			for embedded := range typ.EmbeddedTypes() {
				mentions(owner, embedded)
			}
		}
	}

	scope := pkg.Scope()
	for _, name := range scope.Names() {
		object := scope.Lookup(name)
		if !object.Exported() {
			continue
		}
		switch object := object.(type) {
		case *types.TypeName:
			if object.IsAlias() {
				mentions(name, object.Type())
				continue
			}
			named, ok := object.Type().(*types.Named)
			if !ok {
				continue
			}
			mentions(name, named.Underlying())
			for method := range named.Methods() {
				if method.Exported() {
					mentions(name+"."+method.Name(), method.Type())
				}
			}
		default:
			mentions(name, object.Type())
		}
	}
	sort.Strings(leaks)
	leaks = slices.Compact(leaks)
	if len(leaks) > 0 {
		t.Fatalf("an exported name of package contracts mentions a type a consumer cannot name:\n%v", leaks)
	}
}
