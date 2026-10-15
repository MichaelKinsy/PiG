// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// span is a 1-based inclusive line range.
type span struct{ Start, End int }

type fileSyms struct {
	exists bool
	syms   map[string]span
	lines  []string
}

// symIndex resolves "Name" and "Type.Member" references of Go files to line ranges. A file is parsed once.
type symIndex struct {
	root  string
	mu    sync.Mutex
	files map[string]*fileSyms
}

func newSymIndex(root string) *symIndex {
	return &symIndex{root: root, files: map[string]*fileSyms{}}
}

func (x *symIndex) file(rel string) *fileSyms {
	x.mu.Lock()
	defer x.mu.Unlock()
	if f, ok := x.files[rel]; ok {
		return f
	}
	f := &fileSyms{syms: map[string]span{}}
	x.files[rel] = f
	b, err := os.ReadFile(filepath.Join(x.root, filepath.FromSlash(rel)))
	if err != nil {
		return f
	}
	f.exists = true
	f.lines = strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if !strings.HasSuffix(rel, ".go") {
		return f
	}
	fset := token.NewFileSet()
	af, _ := parser.ParseFile(fset, rel, b, parser.SkipObjectResolution)
	if af == nil {
		return f
	}
	line := func(p token.Pos) int { return fset.Position(p).Line }
	set := func(name string, s span) {
		if _, dup := f.syms[name]; !dup {
			f.syms[name] = s
		}
	}
	for _, d := range af.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				name = recvName(d.Recv.List[0].Type) + "." + name
			}
			set(name, span{line(d.Pos()), line(d.End())})
		case *ast.GenDecl:
			for _, sp := range d.Specs {
				switch sp := sp.(type) {
				case *ast.TypeSpec:
					s := span{line(sp.Pos()), line(sp.End())}
					if d.Lparen == token.NoPos {
						s = span{line(d.Pos()), line(d.End())}
					}
					set(sp.Name.Name, s)
					switch t := sp.Type.(type) {
					case *ast.StructType:
						for _, fl := range t.Fields.List {
							for _, n := range fl.Names {
								set(sp.Name.Name+"."+n.Name, span{line(fl.Pos()), line(fl.End())})
							}
						}
					case *ast.InterfaceType:
						for _, fl := range t.Methods.List {
							for _, n := range fl.Names {
								set(sp.Name.Name+"."+n.Name, span{line(fl.Pos()), line(fl.End())})
							}
						}
					}
				case *ast.ValueSpec:
					s := span{line(sp.Pos()), line(sp.End())}
					if d.Lparen == token.NoPos {
						s = span{line(d.Pos()), line(d.End())}
					}
					for _, n := range sp.Names {
						set(n.Name, s)
					}
				}
			}
		}
	}
	return f
}

// recvName is the receiver's base type name without pointer or type arguments.
func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.ParenExpr:
		return recvName(t.X)
	}
	return ""
}

// Ref is one Go source reference with its resolved lines.
type Ref struct {
	File string
	Sym  string
	Line int
	End  int
	// Missing is true when the file does not exist in the tree.
	Missing bool
}

// resolve finds the span of file#sym. A symbol that is not in the file resolves to line 0 and keeps the file.
func (x *symIndex) resolve(file, sym string) Ref {
	f := x.file(file)
	r := Ref{File: file, Sym: sym, Missing: !f.exists}
	if !f.exists {
		return r
	}
	if s, ok := f.syms[sym]; ok {
		r.Line, r.End = s.Start, s.End
		return r
	}
	// A member of an embedded or generic owner may be recorded under its type only.
	if top, _, ok := strings.Cut(sym, "."); ok {
		if s, ok := f.syms[top]; ok {
			r.Line, r.End = s.Start, s.End
		}
	}
	return r
}

// enclosing returns the innermost declaration that contains line.
func (x *symIndex) enclosing(file string, line int) (span, bool) {
	f := x.file(file)
	best := span{}
	found := false
	for _, s := range f.syms {
		if s.Start <= line && line <= s.End {
			if !found || s.End-s.Start < best.End-best.Start {
				best, found = s, true
			}
		}
	}
	return best, found
}

// text returns the lines [start,end] of a file and whether the end was cut at maxLines.
func (x *symIndex) text(file string, start, end, maxLines int) (string, int, int, bool) {
	f := x.file(file)
	if !f.exists || start < 1 || start > len(f.lines) {
		return "", 0, 0, false
	}
	if end > len(f.lines) {
		end = len(f.lines)
	}
	cut := false
	if end-start+1 > maxLines {
		end = start + maxLines - 1
		cut = true
	}
	return dedent(f.lines[start-1 : end]), start, end, cut
}

// dedent joins lines after removing the whitespace prefix they all share, so a snippet cut from a deep block starts at the left edge.
func dedent(lines []string) string {
	prefix := ""
	first := true
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		ws := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if first {
			prefix, first = ws, false
			continue
		}
		n := 0
		for n < len(prefix) && n < len(ws) && prefix[n] == ws[n] {
			n++
		}
		prefix = prefix[:n]
	}
	if prefix == "" {
		return strings.Join(lines, "\n")
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimPrefix(l, prefix)
	}
	return strings.Join(out, "\n")
}
