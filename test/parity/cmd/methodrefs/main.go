// Command methodrefs reports, with type information, which functions call each method of one Go type.
//
// Usage: go run ./test/parity/cmd/methodrefs (-type <pkg import path>.<Type> | -types A,B,... | -funcs <pkg import path>.<Func>,...) [-out refs.json] <package pattern>...
//
// -types keys the output by type name; the member "_type" lists the functions that mention the type or read or write one of its fields.
// -funcs keys the output by function name; the member "_call" lists the functions that call it.
//
// A call counts only when the selector's receiver type is the named type (or a pointer to it), so a generic method name
// such as Render or Messages is attributed to its own type and not to every type that defines it. A caller is the
// enclosing top-level function, written Recv.Name for a method. Calls in files ending _test.go are reported as tests.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

type refs struct {
	Prod  []string `json:"prod"`
	Tests []string `json:"tests"`
}

func main() {
	typeFlag := flag.String("type", "", "import path and type name, for example github.com/MichaelKinsy/PiG/coding.Session")
	implFlag := flag.String("impl", "", "comma-separated import path and interface names; a call to one of the interface's methods on any type that implements the whole interface counts, in addition to calls through the interface itself")
	funcsFlag := flag.String("funcs", "", "comma-separated import path and package-level function names; the output is keyed by function name with member \"_call\"")
	typesFlag := flag.String("types", "", "comma-separated import path and type names; the output is keyed by type name")
	out := flag.String("out", "-", "output file or -")
	flag.Parse()
	wanted := map[string][2]string{}
	var order []string
	for spec := range strings.SplitSeq(strings.Trim(*typeFlag+","+*typesFlag+","+*funcsFlag+","+*implFlag, ","), ",") {
		dot := strings.LastIndex(spec, ".")
		if dot < 0 {
			continue
		}
		wanted[spec] = [2]string{spec[:dot], spec[dot+1:]}
		order = append(order, spec)
	}
	if len(wanted) == 0 || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: methodrefs (-type|-types) <import path>.<Type> [-out file] <pattern>...")
		os.Exit(2)
	}
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps, Tests: true}
	loaded, err := packages.Load(cfg, flag.Args()...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "methodrefs:", err)
		os.Exit(1)
	}
	ifaces := map[string]*types.Interface{}
	if *implFlag != "" {
		packages.Visit(loaded, nil, func(p *packages.Package) {
			for spec := range strings.SplitSeq(*implFlag, ",") {
				w := wanted[spec]
				if p.PkgPath != w[0] || p.Types == nil {
					continue
				}
				if obj := p.Types.Scope().Lookup(w[1]); obj != nil {
					if it, ok := obj.Type().Underlying().(*types.Interface); ok {
						ifaces[spec] = it
					}
				}
			}
		})
	}
	results := map[string]map[string]*refs{}
	for _, spec := range order {
		results[wanted[spec][1]] = map[string]*refs{}
	}
	seen := map[string]bool{}
	record := func(typ, member, rel, caller string, isTest bool) {
		key := typ + "|" + member + "|" + rel + "|" + caller
		if seen[key] {
			return
		}
		seen[key] = true
		r := results[typ][member]
		if r == nil {
			r = &refs{}
			results[typ][member] = r
		}
		entry := rel + "#" + caller
		if isTest {
			r.Tests = append(r.Tests, entry)
		} else {
			r.Prod = append(r.Prod, entry)
		}
	}
	for _, pkg := range loaded {
		if pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			name := pkg.Fset.File(file.Pos()).Name()
			isTest := strings.HasSuffix(name, "_test.go")
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				caller := fn.Name.Name
				if fn.Recv != nil && len(fn.Recv.List) == 1 {
					caller = receiverName(fn.Recv.List[0].Type) + "." + caller
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					rel, _ := filepath.Rel(mustGetwd(), name)
					if id, ok := n.(*ast.Ident); ok {
						if fn, ok := pkg.TypesInfo.Uses[id].(*types.Func); ok && fn.Pkg() != nil && fn.Type().(*types.Signature).Recv() == nil {
							if w, ok := wanted[fn.Pkg().Path()+"."+fn.Name()]; ok {
								record(w[1], "_call", rel, caller, isTest)
							}
						}
						if tn, ok := pkg.TypesInfo.Uses[id].(*types.TypeName); ok && tn.Pkg() != nil {
							if w, ok := wanted[tn.Pkg().Path()+"."+tn.Name()]; ok {
								record(w[1], "_type", rel, caller, isTest)
							}
						}
					}
					sel, ok := n.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					selection := pkg.TypesInfo.Selections[sel]
					if selection == nil {
						return true
					}
					if field, ok := selection.Obj().(*types.Var); ok && field.IsField() {
						t := selection.Recv()
						if p, ok := t.(*types.Pointer); ok {
							t = p.Elem()
						}
						if n, ok := t.(*types.Named); ok && n.Obj().Pkg() != nil {
							if w, ok := wanted[n.Obj().Pkg().Path()+"."+n.Obj().Name()]; ok {
								record(w[1], "_type", rel, caller, isTest)
							}
						}
						return true
					}
					if method, ok := selection.Obj().(*types.Func); ok {
						for spec, it := range ifaces {
							if !types.Implements(selection.Recv(), it) && !types.Implements(types.NewPointer(selection.Recv()), it) {
								continue
							}
							for method0 := range it.Methods() {
								if method0.Name() == method.Name() {
									record(wanted[spec][1], method.Name(), rel, caller, isTest)
								}
							}
						}
					}
					method, ok := selection.Obj().(*types.Func)
					if !ok || method.Pkg() == nil {
						return true
					}
					recv := method.Type().(*types.Signature).Recv()
					if recv == nil {
						return true
					}
					recvName := namedName(recv.Type())
					if recvName == "" {
						// A method of an interface reports the interface literal as its receiver; the static type of the operand names it.
						recvName = namedName(selection.Recv())
					}
					if w, ok := wanted[method.Pkg().Path()+"."+recvName]; ok {
						record(w[1], method.Name(), rel, caller, isTest)
					}
					return true
				})
			}
		}
	}
	for _, members := range results {
		for _, r := range members {
			sort.Strings(r.Prod)
			sort.Strings(r.Tests)
		}
	}
	var payload any = results
	if *typesFlag == "" && *funcsFlag == "" {
		payload = results[wanted[*typeFlag][1]]
	}
	data, _ := json.MarshalIndent(payload, "", " ")
	if *out == "-" {
		fmt.Println(string(data))
		return
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "methodrefs:", err)
		os.Exit(1)
	}
}

func namedName(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok {
		return n.Obj().Name()
	}
	return ""
}

func receiverName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.IndexExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}
