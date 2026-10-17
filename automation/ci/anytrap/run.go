// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/tools/go/packages"
)

// finding is the decodes of one destination type made by one call in one declaration. The key has no line number, so unrelated edits do not churn the baseline.
type finding struct {
	File  string `toml:"file"`
	Func  string `toml:"func"`
	Call  string `toml:"call"`
	Dest  string `toml:"dest"`
	Count int    `toml:"count"`
}

func (f finding) key() string { return f.File + "|" + f.Func + "|" + f.Call + "|" + f.Dest }

type baselineFile struct {
	Hit []finding `toml:"hit"`
}

// destinationArgument maps a decoding function of encoding/json (v1 or v2) or of the SDK's copy of it to the index of its destination argument.
func destinationArgument(pkgPath, name string) (int, bool) {
	// extensions/sdk/json is the extension SDK's own copy of the v1 codec, which the host and the session code decode IPC frames and session lines with.
	if pkgPath != "encoding/json" && pkgPath != "encoding/json/v2" && pkgPath != sdkJSONPath {
		return 0, false
	}
	switch name {
	case "Unmarshal", "UnmarshalRead", "UnmarshalDecode":
		return 1, true
	case "Decode":
		return 0, true
	}
	return 0, false
}

// sdkJSONPath is the extension SDK's copy of encoding/json.
const sdkJSONPath = "github.com/MichaelKinsy/PiG/extensions/sdk/json"

// callName is the package and function of a decoding call, with the SDK's copy named apart from encoding/json.
func callName(used types.Object) string {
	if used.Pkg().Path() == sdkJSONPath {
		return "sdkjson." + used.Name()
	}
	return used.Pkg().Name() + "." + used.Name()
}

func isEmptyInterface(t types.Type) bool {
	iface, ok := t.Underlying().(*types.Interface)
	return ok && iface.Empty()
}

// isAnyTrap reports whether decoding into a value of type t builds a tree of interface values: t is a pointer to, or is, an empty interface, a type parameter, or a map or slice of empty interfaces.
func isAnyTrap(t types.Type) bool {
	if pointer, ok := t.(*types.Pointer); ok {
		t = pointer.Elem()
	}
	switch u := t.Underlying().(type) {
	case *types.Interface:
		return u.Empty()
	case *types.Map:
		return isEmptyInterface(u.Elem())
	case *types.Slice:
		return isEmptyInterface(u.Elem())
	}
	return false
}

// scan returns the decodes into interface-typed destinations in the non-test code of pkgs, with file names relative to root.
func scan(root string, pkgs []*packages.Package) []finding {
	counts := map[string]*finding{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			name := pkg.Fset.Position(file.Pos()).Filename
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			rel, err := filepath.Rel(root, name)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			rel = filepath.ToSlash(rel)
			if isToolingPath(rel) {
				continue
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				owner := declarationName(fn)
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					var used types.Object
					switch fun := call.Fun.(type) {
					case *ast.SelectorExpr:
						used = pkg.TypesInfo.Uses[fun.Sel]
					case *ast.Ident:
						used = pkg.TypesInfo.Uses[fun]
					case *ast.IndexExpr:
						if id, ok := fun.X.(*ast.Ident); ok {
							used = pkg.TypesInfo.Uses[id]
						} else if sel, ok := fun.X.(*ast.SelectorExpr); ok {
							used = pkg.TypesInfo.Uses[sel.Sel]
						}
					}
					if used == nil || used.Pkg() == nil {
						return true
					}
					index, ok := destinationArgument(used.Pkg().Path(), used.Name())
					if !ok || index >= len(call.Args) {
						return true
					}
					dest := pkg.TypesInfo.TypeOf(call.Args[index])
					if dest == nil || !isAnyTrap(dest) {
						return true
					}
					f := finding{File: rel, Func: owner, Call: callName(used), Dest: types.TypeString(dest, shortQualifier)}
					if existing := counts[f.key()]; existing != nil {
						existing.Count++
					} else {
						f.Count = 1
						counts[f.key()] = &f
					}
					return true
				})
			}
		}
	}
	found := make([]finding, 0, len(counts))
	for _, f := range counts {
		found = append(found, *f)
	}
	slices.SortFunc(found, func(a, b finding) int { return strings.Compare(a.key(), b.key()) })
	return found
}

// isToolingPath reports whether rel is build, test or example tooling, or vendored, which reads files at build time and is off every runtime path.
func isToolingPath(rel string) bool {
	for _, prefix := range []string{"test/", "automation/", "tmp/", "examples/", "docs/", "third_party/"} {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

func shortQualifier(p *types.Package) string { return p.Name() }

// declarationName is Type.Method for a method and the name for a function.
func declarationName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := fn.Recv.List[0].Type
	for {
		switch t := recv.(type) {
		case *ast.StarExpr:
			recv = t.X
			continue
		case *ast.IndexExpr:
			recv = t.X
			continue
		case *ast.IndexListExpr:
			recv = t.X
			continue
		case *ast.Ident:
			return t.Name + "." + fn.Name.Name
		}
		return fn.Name.Name
	}
}

func load(root string, patterns []string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Dir:  root,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return nil, fmt.Errorf("load %s: %w", pkg.PkgPath, pkg.Errors[0])
		}
	}
	return pkgs, nil
}

func entry(f finding) string {
	return fmt.Sprintf("[[hit]]\nfile = %q\nfunc = %q\ncall = %q\ndest = %q\ncount = %d\n", f.File, f.Func, f.Call, f.Dest, f.Count)
}

// compare lists the differences between the decodes found and the baseline: a decode the baseline does not allow, and a baseline entry that allows more than is found.
func compare(found []finding, baseline []finding) (grew, stale []string) {
	allowed := map[string]finding{}
	for _, b := range baseline {
		allowed[b.key()] = b
	}
	seen := map[string]bool{}
	for _, f := range found {
		seen[f.key()] = true
		b, ok := allowed[f.key()]
		switch {
		case !ok:
			grew = append(grew, fmt.Sprintf("%s %s: %d new %s into %s", f.File, f.Func, f.Count, f.Call, f.Dest))
		case f.Count > b.Count:
			grew = append(grew, fmt.Sprintf("%s %s: %s into %s went from %d to %d", f.File, f.Func, f.Call, f.Dest, b.Count, f.Count))
		case f.Count < b.Count:
			stale = append(stale, fmt.Sprintf("%s %s: %s into %s fell from %d to %d; lower the baseline entry", f.File, f.Func, f.Call, f.Dest, b.Count, f.Count))
		}
	}
	for _, b := range baseline {
		if !seen[b.key()] {
			stale = append(stale, fmt.Sprintf("%s %s: %s into %s is gone; delete the baseline entry", b.File, b.Func, b.Call, b.Dest))
		}
	}
	return grew, stale
}

func run(root, baselinePath string, patterns []string, report, printBaseline bool) int {
	pkgs, err := load(root, patterns)
	if err != nil {
		fmt.Fprintln(os.Stderr, "any-trap:", err)
		return 2
	}
	found := scan(root, pkgs)
	if printBaseline {
		// REUSE-IgnoreStart
		fmt.Print("# SPDX-License-Identifier: MIT\n#\n# Any-trap ratchet (automation/ci/anytrap, `make any-trap`). Counts may only shrink: a fix lowers or deletes its entry in the same change; never add an entry for new code.\n# Regenerate the whole file with `go run ./automation/ci/anytrap -print` only when lowering counts.\n\n")
		// REUSE-IgnoreEnd
		for _, f := range found {
			fmt.Println(entry(f))
		}
		return 0
	}
	if report {
		total := 0
		for _, f := range found {
			total += f.Count
			fmt.Printf("%4d  %s  %s  %s into %s\n", f.Count, f.File, f.Func, f.Call, f.Dest)
		}
		fmt.Printf("%d decodes into interface values in %d places\n", total, len(found))
		return 0
	}
	var baseline baselineFile
	if _, err := toml.DecodeFile(baselinePath, &baseline); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "any-trap:", err)
		return 2
	}
	grew, stale := compare(found, baseline.Hit)
	for _, line := range grew {
		fmt.Fprintln(os.Stderr, "any-trap: new decode into an interface value:", line)
	}
	for _, line := range stale {
		fmt.Fprintln(os.Stderr, "any-trap: stale baseline:", line)
	}
	if len(grew)+len(stale) > 0 {
		fmt.Fprintln(os.Stderr, "any-trap: decode into a struct, a json.RawMessage or a scanner instead; lower the baseline with `go run ./automation/ci/anytrap -print` when a decode is removed")
		return 1
	}
	return 0
}
