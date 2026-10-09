// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"

	"github.com/MichaelKinsy/PiG/automation/ci/portlint/checks"
)

// Hit is one finding with the identity the baseline keys on.
type Hit struct {
	Check   string
	File    string
	Line    int
	Func    string
	Snippet string
	Message string
}

// Key identifies a hit independent of its line number.
func (h Hit) Key() string { return h.Check + "|" + h.File + "|" + h.Func + "|" + h.Snippet }

func run(root, baselinePath, tags string, patterns []string, report, printBaseline bool) int {
	hits, err := scanAll(root, tags, patterns)
	if err != nil {
		fmt.Fprintln(os.Stderr, "port-lint:", err)
		return 2
	}
	if printBaseline {
		fmt.Print(renderBaseline(hits))
		return 0
	}
	if report {
		printReport(hits)
		return 0
	}
	base, err := loadBaseline(baselinePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "port-lint:", err)
		return 2
	}
	unknown, stale := compare(hits, base)
	if len(unknown) == 0 && len(stale) == 0 {
		fmt.Printf("port-lint: %d checks, %d baselined findings (%s)\n", len(checks.All()), len(hits), countsLine(hits))
		return 0
	}
	if len(unknown) > 0 {
		fmt.Println("FAIL: new port-lint findings. Fix them to match Pi, or mark the line `//portlint:allow <check> <reason>`:")
		for _, h := range unknown {
			fmt.Printf("  %s:%d: [%s] %s\n      %s\n", h.File, h.Line, h.Check, h.Message, h.Snippet)
		}
	}
	if len(stale) > 0 {
		fmt.Printf("FAIL: %s lists findings that no longer match; remove the fixed entries:\n", baselinePath)
		for _, s := range stale {
			fmt.Println("  -", s)
		}
	}
	return 1
}

// scanAll scans the default build and, when tags is set, the build with those tags, and merges the findings.
func scanAll(root, tags string, patterns []string) ([]Hit, error) {
	hits, err := scan(root, "", patterns)
	if err != nil || tags == "" {
		return hits, err
	}
	tagged, err := scan(root, tags, patterns)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, h := range hits {
		seen[fmt.Sprintf("%s|%s:%d|%s", h.Check, h.File, h.Line, h.Message)] = true
	}
	for _, h := range tagged {
		if id := fmt.Sprintf("%s|%s:%d|%s", h.Check, h.File, h.Line, h.Message); !seen[id] {
			hits = append(hits, h)
		}
	}
	slices.SortFunc(hits, func(a, b Hit) int {
		return strings.Compare(fmt.Sprintf("%s|%s|%08d|%s", a.Check, a.File, a.Line, a.Message), fmt.Sprintf("%s|%s|%08d|%s", b.Check, b.File, b.Line, b.Message))
	})
	return hits, nil
}

// scan loads the packages with their tests and runs every analyzer.
func scan(root, tags string, patterns []string) ([]Hit, error) {
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Dir:   root,
		Tests: true,
	}
	if tags != "" {
		cfg.BuildFlags = []string{"-tags=" + tags}
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	var loadErrs []string
	var clean []*packages.Package
	for _, p := range pkgs {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e.Error())
		}
		if len(p.Errors) == 0 {
			clean = append(clean, p)
		}
	}
	// The default build must load cleanly. A tagged build compiles only part of a package set that is built with other tags too, so its broken packages are left to the default pass.
	if len(loadErrs) > 0 && tags == "" {
		return nil, fmt.Errorf("load: %s", strings.Join(loadErrs[:min(5, len(loadErrs))], "; "))
	}
	pkgs = clean
	var analyzers []*analysis.Analyzer
	for _, c := range checks.All() {
		analyzers = append(analyzers, c.Analyzer)
	}
	graph, err := checker.Analyze(analyzers, pkgs, nil)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var hits []Hit
	lines := map[string][]string{}
	for _, act := range graph.Roots {
		if act.Err != nil {
			return nil, fmt.Errorf("%s on %s: %w", act.Analyzer.Name, act.Package.PkgPath, act.Err)
		}
		for _, d := range act.Diagnostics {
			pos := act.Package.Fset.Position(d.Pos)
			rel, err := filepath.Rel(root, pos.Filename)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			rel = filepath.ToSlash(rel)
			id := fmt.Sprintf("%s|%s:%d|%s", d.Category, rel, pos.Line, d.Message)
			if seen[id] {
				continue
			}
			seen[id] = true
			src, ok := lines[pos.Filename]
			if !ok {
				data, _ := os.ReadFile(pos.Filename)
				src = strings.Split(string(data), "\n")
				lines[pos.Filename] = src
			}
			snippet := ""
			if pos.Line-1 < len(src) {
				snippet = strings.TrimSpace(src[pos.Line-1])
			}
			hits = append(hits, Hit{
				Check: d.Category, File: rel, Line: pos.Line, Func: enclosingFunc(act.Package, d.Pos),
				Snippet: snippet, Message: d.Message,
			})
		}
	}
	slices.SortFunc(hits, func(a, b Hit) int {
		return strings.Compare(fmt.Sprintf("%s|%s|%08d|%s", a.Check, a.File, a.Line, a.Message), fmt.Sprintf("%s|%s|%08d|%s", b.Check, b.File, b.Line, b.Message))
	})
	return hits, nil
}

// enclosingFunc names the declaration containing pos: Recv.Name or Name.
func enclosingFunc(pkg *packages.Package, pos token.Pos) string {
	for _, f := range pkg.Syntax {
		if f.FileStart > pos || pos > f.FileEnd {
			continue
		}
		for _, d := range f.Decls {
			if d.Pos() > pos || pos > d.End() {
				continue
			}
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil && len(d.Recv.List) == 1 {
					return recvName(d.Recv.List[0].Type) + "." + d.Name.Name
				}
				return d.Name.Name
			case *ast.GenDecl:
				return "(decl)"
			}
		}
	}
	return ""
}

func recvName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.StarExpr:
		return recvName(e.X)
	case *ast.IndexExpr:
		return recvName(e.X)
	case *ast.IndexListExpr:
		return recvName(e.X)
	case *ast.Ident:
		return e.Name
	}
	return "?"
}
