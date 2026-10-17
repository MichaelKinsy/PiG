package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"
)

const narrowFile = "test/parity/interface-closure/autobind/narrow-interfaces.json"

// narrowEntry is a reviewed narrow interface (T9n): a Pi parameter typed as a class whose Go package the consumer cannot import (an
// import cycle) is satisfied by a consumer-owned interface that lists exactly the Pi members the consumer uses.
type narrowEntry struct {
	// Interface is the consumer-owned Go interface, `dir#Name`.
	Interface string `json:"interface"`
	// Members maps each Pi member the consumer uses to the interface method that carries it.
	Members map[string]string `json:"members"`
	// Test is a Go test, `dir#TestName`, that passes the real producer: its body names the Go type of the Pi class.
	Test   string `json:"test"`
	Reason string `json:"reason"`
}

// narrowAccepts reports whether Go type t is the verified narrow interface that stands for the upstream class up.
func (d *detector) narrowAccepts(pkg, up string, t types.Type) bool {
	entries := d.narrow[pkg+":"+up]
	if len(entries) == 0 {
		return false
	}
	label := typeLabel(deref(t))
	for _, e := range entries {
		if d.narrowIface(e) == nil || label != typeLabel(d.narrowIface(e)) {
			continue
		}
		key := pkg + ":" + up + ":" + e.Interface
		why, done := d.narrowMemo[key]
		if !done {
			why = d.verifyNarrow(pkg, up, e)
			if d.narrowMemo == nil {
				d.narrowMemo = map[string]string{}
			}
			d.narrowMemo[key] = why
		}
		if why == "" {
			return true
		}
	}
	return false
}

// narrowIface returns the named Go interface type of an entry, or nil.
func (d *detector) narrowIface(e narrowEntry) types.Type {
	dir, name, ok := strings.Cut(e.Interface, "#")
	if !ok {
		return nil
	}
	for _, s := range d.ix.topLevel([]string{dir}) {
		if s.Kind == "type" && s.Name == name {
			if _, isIface := s.Obj.Type().Underlying().(*types.Interface); isIface {
				return s.Obj.Type()
			}
		}
	}
	return nil
}

// verifyNarrow returns "" when the narrow interface of class up is sound, otherwise the reason it is not: the interface lists exactly the
// mapped Pi members, every one is a member of the Pi class, the Go type of the Pi class implements the interface with a compile-time
// assertion in its package, and the named test passes that real producer.
func (d *detector) verifyNarrow(pkg, up string, e narrowEntry) string {
	ifaceType := d.narrowIface(e)
	if ifaceType == nil {
		return "no Go interface " + e.Interface
	}
	iface := ifaceType.Underlying().(*types.Interface)
	producerTN := d.typeFor(pkg, up)
	if producerTN == nil {
		return "the Pi class " + up + " has no Go type"
	}
	var classID string
	for _, row := range d.l.entries {
		if row.Name == up && row.Role == "" && row.Kind == "class" && parseID(row.ID).Pkg == pkg {
			classID = row.ID
		}
	}
	if classID == "" {
		return "no Pi class " + up
	}
	piMembers := map[string]bool{}
	for _, c := range d.l.children[classID] {
		piMembers[strings.TrimPrefix(c.Name, up+".")] = true
	}
	want := map[string]bool{}
	for piName, goName := range e.Members {
		if !piMembers[piName] {
			return fmt.Sprintf("%s is not a member of the Pi class %s", piName, up)
		}
		want[goName] = true
	}
	for m := range iface.Methods() {
		if !want[m.Name()] {
			return "the interface lists " + m.Name() + ", which no listed Pi member uses"
		}
		delete(want, m.Name())
	}
	for goName := range want {
		return "the interface does not list " + goName
	}
	if !types.Implements(types.NewPointer(producerTN.Type()), iface) && !types.Implements(producerTN.Type(), iface) {
		return producerTN.Name() + " does not implement " + e.Interface
	}
	if !d.hasAssertion(producerTN, e) {
		return "no compile-time assertion `var _ " + e.Interface[strings.Index(e.Interface, "#")+1:] + " = (*" + producerTN.Name() + ")(nil)` in " + d.ix.dirOf(producerTN)
	}
	dir, name, _ := strings.Cut(e.Test, "#")
	if body, ok := d.packageTestBody(dir, name); !ok || !mentions(body, producerTN.Name()) {
		return "test " + e.Test + " does not exist or never names the producer " + producerTN.Name()
	}
	return ""
}

// mentions reports whether source names identifier id.
func mentions(src, id string) bool {
	for i := strings.Index(src, id); i >= 0; {
		before := i == 0 || !isIdentByte(src[i-1])
		after := i+len(id) >= len(src) || !isIdentByte(src[i+len(id)])
		if before && after {
			return true
		}
		next := strings.Index(src[i+1:], id)
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return false
}

func isIdentByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// hasAssertion reports whether a non-test file of the producer's package declares `var _ Iface = ...` naming the producer.
func (d *detector) hasAssertion(producer *types.TypeName, e narrowEntry) bool {
	_, ifaceName, _ := strings.Cut(e.Interface, "#")
	for _, f := range d.packageFiles(d.ix.dirOf(producer)) {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "_" || vs.Type == nil || !slices.Contains(typeExprNames(vs.Type), ifaceName) {
					continue
				}
				for _, v := range vs.Values {
					found := false
					ast.Inspect(v, func(n ast.Node) bool {
						if id, ok := n.(*ast.Ident); ok && id.Name == producer.Name() {
							found = true
						}
						return !found
					})
					if found {
						return true
					}
				}
			}
		}
	}
	return false
}

// typeExprNames returns the identifier a type expression ends in: `I`, `pkg.I`.
func typeExprNames(x ast.Expr) []string {
	switch t := x.(type) {
	case *ast.Ident:
		return []string{t.Name}
	case *ast.SelectorExpr:
		return []string{t.Sel.Name}
	}
	return nil
}

// packageFiles parses the non-test Go files of dir.
func (d *detector) packageFiles(dir string) []*ast.File {
	files, _ := filepath.Glob(filepath.Join(d.ix.root, filepath.FromSlash(dir), "*.go"))
	var out []*ast.File
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		if f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution); err == nil {
			out = append(out, f)
		}
	}
	return out
}
