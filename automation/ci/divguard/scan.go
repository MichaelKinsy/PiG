// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Hit is one place where a check matched.
type Hit struct {
	Check   string
	File    string // slash path relative to the repository root
	Line    int
	Func    string // enclosing declaration, the stable part of the baseline key
	Snippet string // the whole flagged source line, whitespace-collapsed
	Message string
}

// Key identifies a hit in the baseline independently of its line number.
func (h Hit) Key() string { return h.Check + "|" + h.File + "|" + h.Func + "|" + h.Snippet }

// check inspects one parsed file. Checks are syntactic so the whole tree
// scans in well under a second without loading type information.
type check struct {
	Name string
	Doc  string
	// Applies reports whether the check scans the file at rel.
	Applies func(rel string) bool
	Run     func(fc *fileCtx) []Hit
}

// hotPrefixes are the packages on the agent, streaming, tool, persistence,
// rendering and extension-host paths, where an invented limit or a silent
// exit changes what users see.
var hotPrefixes = []string{"agent/", "ai/", "coding/", "internal/codingagent/", "tui/", "cmd/pig/"}

func inHot(rel string) bool {
	for _, p := range hotPrefixes {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

var skipDirs = map[string]bool{".git": true, ".upstream": true, "node_modules": true, "testdata": true, "vendor": true, "tmp": true, "bin": true, ".devcache": true}

var generatedRe = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

// env is the repository context shared by every check.
type env struct {
	Root     string
	Upstream *upstreamIndex
	// Divergences holds every D<N> id recorded in DIVERGENCES.md or
	// docs/additive-features.md.
	Divergences map[string]bool
	// PortMap maps a Go file to the upstream files PORT_MAP.md says it ports.
	PortMap map[string][]string
	// ErrorFuncs indexes which declared functions return error.
	ErrorFuncs errorFuncs
}

func loadEnv(root, upstreamDir string) (*env, error) {
	e := &env{Root: root, Divergences: map[string]bool{}}
	for _, ledger := range []string{"DIVERGENCES.md", "docs/additive-features.md"} {
		data, err := os.ReadFile(filepath.Join(root, ledger))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, m := range ledgerHeadingRe.FindAllSubmatch(data, -1) {
			e.Divergences[string(m[1])] = true
		}
	}
	idx, err := loadUpstreamIndex(upstreamDir)
	if err != nil {
		return nil, err
	}
	e.Upstream = idx
	if e.PortMap, err = loadPortMap(root); err != nil {
		return nil, err
	}
	if e.ErrorFuncs, err = indexErrorFuncs(root); err != nil {
		return nil, err
	}
	return e, nil
}

var ledgerHeadingRe = regexp.MustCompile(`(?m)^## (D[0-9]+)\b`)

// fileCtx is one parsed source file.
type fileCtx struct {
	Rel   string
	Fset  *token.FileSet
	File  *ast.File
	Src   []byte
	Lines []string
	Env   *env
	// ErrorFuncs is the repository index plus this file's own declarations.
	ErrorFuncs errorFuncs
	// Problems collects invalid allow markers found while checking.
	Problems *[]string
}

func newFileCtx(rel string, src []byte, e *env, problems *[]string) (*fileCtx, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	funcs := errorFuncs{}
	if e.ErrorFuncs == nil {
		funcs.add(f)
	} else {
		funcs = e.ErrorFuncs
	}
	return &fileCtx{Rel: rel, Fset: fset, File: f, Src: src, Lines: strings.Split(string(src), "\n"), Env: e, ErrorFuncs: funcs, Problems: problems}, nil
}

func (fc *fileCtx) line(pos token.Pos) int { return fc.Fset.Position(pos).Line }

// problem records an invalid allow marker once.
func (fc *fileCtx) problem(msg string) {
	if slices.Contains(*fc.Problems, msg) {
		return
	}
	*fc.Problems = append(*fc.Problems, msg)
}

var spaceRe = regexp.MustCompile(`\s+`)

// snippet is the whole flagged source line, whitespace-collapsed and without
// its trailing comment. It is part of the baseline key, so it is never
// truncated: a changed value anywhere on the line changes the key.
func (fc *fileCtx) snippet(line int) string {
	if line < 1 || line > len(fc.Lines) {
		return ""
	}
	s := fc.Lines[line-1]
	if i := strings.Index(s, "//"); i >= 0 && !strings.Contains(s[:i], `"`) && !strings.Contains(s[:i], "`") {
		s = s[:i]
	}
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

// display shortens a snippet for terminal output only.
func display(snippet string) string {
	const width = 140
	if r := []rune(snippet); len(r) > width {
		return string(r[:width]) + "…"
	}
	return snippet
}

// enclosing names the top-level declaration containing pos: Recv.Method,
// Func, or the names declared by a var/const/type block.
func (fc *fileCtx) enclosing(pos token.Pos) string {
	for _, d := range fc.File.Decls {
		if pos < d.Pos() || pos >= d.End() {
			continue
		}
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil && len(d.Recv.List) > 0 {
				return recvName(d.Recv.List[0].Type) + "." + d.Name.Name
			}
			return d.Name.Name
		case *ast.GenDecl:
			for _, s := range d.Specs {
				if pos < s.Pos() || pos >= s.End() {
					continue
				}
				switch s := s.(type) {
				case *ast.ValueSpec:
					var names []string
					for _, n := range s.Names {
						names = append(names, n.Name)
					}
					return strings.Join(names, ",")
				case *ast.TypeSpec:
					return s.Name.Name
				}
			}
			return d.Tok.String()
		}
	}
	return "file"
}

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
	}
	return "?"
}

// markerLines lists the lines where an allow marker for a hit at pos may
// sit: the hit's own line and the first line of each enclosing statement,
// case, spec or declaration (whose doc comment ends on the line above).
func (fc *fileCtx) markerLines(pos token.Pos, stack []ast.Node) []int {
	lines := []int{fc.line(pos)}
	for _, s := range slices.Backward(stack) {
		switch n := s.(type) {
		case ast.Stmt, ast.Spec, *ast.GenDecl, *ast.FuncDecl, *ast.KeyValueExpr, *ast.Field:
			if _, block := n.(*ast.BlockStmt); block {
				continue
			}
			if ln := fc.line(n.Pos()); ln != lines[len(lines)-1] {
				lines = append(lines, ln)
			}
			if gd, ok := n.(*ast.GenDecl); ok && gd.Doc != nil {
				lines = append(lines, fc.line(gd.Doc.End())+1)
			}
			if fd, ok := n.(*ast.FuncDecl); ok && fd.Doc != nil {
				lines = append(lines, fc.line(fd.Doc.End())+1)
			}
		}
	}
	return lines
}

// hit builds a Hit at pos. It reports false when a recorded divergence
// marker (`// pig divergence (D<N>)` or `// pig additive (D<N>)`) sits on
// the flagged line, directly above it, or directly above an enclosing
// statement or declaration (stack holds pos's ancestors, outermost first).
// For every check but magic-literal, which also verifies the value, an
// `// upstream: <file>:<symbol>` reference in the same place allows the hit
// when the mirror file exists and contains the symbol.
func (fc *fileCtx) hit(check string, pos token.Pos, stack []ast.Node, msg string) (Hit, bool) {
	lines := fc.markerLines(pos, stack)
	if fc.dnMarked(lines...) || (check != magicLiteralName && fc.refAllowed(lines)) {
		return Hit{}, false
	}
	ln := fc.line(pos)
	return Hit{Check: check, File: fc.Rel, Line: ln, Func: fc.enclosing(pos), Snippet: fc.snippet(ln), Message: msg}, true
}

// refAllowed reports whether an `// upstream:` reference on one of lines
// names a mirror file that exists and contains the symbol. A reference that
// does not verify is recorded as a problem.
func (fc *fileCtx) refAllowed(lines []int) bool {
	for _, ln := range lines {
		for _, c := range fc.commentsNear(ln) {
			for _, m := range upstreamRefRe.FindAllStringSubmatch(c, -1) {
				if _, err := fc.Env.Upstream.verifyRef(m[1], m[2], nil); err != nil {
					fc.problem(fmt.Sprintf("%s:%d: `upstream: %s`: %v", fc.Rel, ln, strings.TrimSuffix(m[1]+":"+m[2], ":"), err))
					continue
				}
				return true
			}
		}
	}
	return false
}

// commentsNear returns the text of comments on line ln and of the comment
// group that ends on the line before it.
func (fc *fileCtx) commentsNear(ln int) []string {
	var out []string
	for _, g := range fc.File.Comments {
		start, end := fc.line(g.Pos()), fc.line(g.End())
		if (start <= ln && ln <= end) || end == ln-1 {
			for _, c := range g.List {
				out = append(out, c.Text)
			}
		}
	}
	return out
}

var dnMarkerRe = regexp.MustCompile(`pig (?:divergence|additive) \((D[0-9]+)\)`)

// dnMarked reports whether a comment on any of lines names a recorded
// divergence. A marker naming an unrecorded D<N> is a problem, not an allow.
func (fc *fileCtx) dnMarked(lines ...int) bool {
	for _, ln := range lines {
		for _, c := range fc.commentsNear(ln) {
			for _, m := range dnMarkerRe.FindAllStringSubmatch(c, -1) {
				if fc.Env.Divergences[m[1]] {
					return true
				}
				fc.problem(fmt.Sprintf("%s:%d: marker names %s, which DIVERGENCES.md does not record", fc.Rel, ln, m[1]))
			}
		}
	}
	return false
}

// scan runs every applicable check over the Go files under root.
func scan(root string, e *env, checks []check) ([]Hit, []string, error) {
	var hits []Hit
	var problems []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		var active []check
		for _, c := range checks {
			if c.Applies(rel) {
				active = append(active, c)
			}
		}
		if len(active) == 0 {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fileHits, fileProblems, err := scanSource(rel, src, e, active)
		if err != nil {
			return err
		}
		hits = append(hits, fileHits...)
		problems = append(problems, fileProblems...)
		return nil
	})
	sortHits(hits)
	return hits, problems, err
}

// scanSource runs checks over one file's source. Generated files are skipped.
func scanSource(rel string, src []byte, e *env, checks []check) ([]Hit, []string, error) {
	if generatedRe.Match(src) || bytes.Contains(src, []byte("//go:build ignore")) {
		return nil, nil, nil
	}
	var problems []string
	fc, err := newFileCtx(rel, src, e, &problems)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	var hits []Hit
	for _, c := range checks {
		hits = append(hits, c.Run(fc)...)
	}
	return hits, problems, nil
}

func sortHits(hits []Hit) {
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Check < b.Check
	})
}

// inspect walks n and passes each node with its ancestors (outermost first).
func inspect(n ast.Node, fn func(n ast.Node, stack []ast.Node) bool) {
	var stack []ast.Node
	ast.Inspect(n, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if !fn(n, stack) {
			return false
		}
		stack = append(stack, n)
		return true
	})
}

// exprName is the last identifier of an identifier or selector chain.
func exprName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.StarExpr:
		return exprName(t.X)
	case *ast.ParenExpr:
		return exprName(t.X)
	case *ast.IndexExpr:
		return exprName(t.X)
	}
	return ""
}

// calleeName is the called function or method name.
func calleeName(call *ast.CallExpr) string { return exprName(call.Fun) }

// isPkgCall reports a call to pkg.name.
func isPkgCall(call *ast.CallExpr, pkg, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}
