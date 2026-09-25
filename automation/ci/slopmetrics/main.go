// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Command slopmetrics measures two code-quality signals from SlopCodeBench
// (arXiv:2603.24755) over the repository's hand-written Go code:
//
//   - Erosion: the share of function mass held by functions whose cyclomatic
//     complexity exceeds 10, where mass(f) = CC(f) * sqrt(SLOC(f)).
//   - Verbosity: the share of lines that belong to duplicated blocks. A block is
//     a window of 6 normalized code lines that occurs more than once. The paper
//     also counts lines flagged by hand-written AST-Grep rules; this tool does
//     not, so its verbosity is a lower bound of the paper's.
//
// SlopCodeBench reports established repositories at verbosity 0.15 +/- 0.06 and
// erosion 0.31 +/- 0.17, and agent-written code at 0.33 and 0.68. Generated
// files and tests are excluded. Run it with `make slop`.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const window = 6

type totals struct {
	Files, LOC, Functions, CloneLines int
	Mass, ErodedMass                  float64
}

func (t totals) Verbosity() float64 { return ratio(float64(t.CloneLines), float64(t.LOC)) }
func (t totals) Erosion() float64   { return ratio(t.ErodedMass, t.Mass) }

func ratio(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

var generated = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)
var skipDirs = map[string]bool{".git": true, ".upstream": true, "node_modules": true, "testdata": true, "vendor": true, "tmp": true, "bin": true}

func main() {
	root := flag.String("root", ".", "repository root")
	asJSON := flag.Bool("json", false, "print JSON")
	maxVerbosity := flag.Float64("max-verbosity", 0, "fail when repository verbosity exceeds this (0 disables)")
	maxErosion := flag.Float64("max-erosion", 0, "fail when repository erosion exceeds this (0 disables)")
	top := flag.Int("top", 15, "list this many of the heaviest functions with CC > 10")
	flag.Parse()

	byArea := map[string]*totals{}
	all := &totals{}
	counts := map[string]int{}
	type heavy struct {
		Where string  `json:"where"`
		CC    int     `json:"cc"`
		SLOC  int     `json:"sloc"`
		Mass  float64 `json:"mass"`
	}
	var heaviest []heavy
	type fileLines struct {
		area  string
		lines []string
	}
	var files []fileLines
	fset := token.NewFileSet()
	err := filepath.WalkDir(*root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil || generated.Match(src) {
			return err
		}
		rel, _ := filepath.Rel(*root, path)
		area, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
		if !strings.Contains(filepath.ToSlash(rel), "/") {
			area = "(root)"
		}
		t := byArea[area]
		if t == nil {
			t = &totals{}
			byArea[area] = t
		}
		lines := codeLines(string(src))
		t.Files++
		t.LOC += len(lines)
		for i := 0; i+window <= len(lines); i++ {
			counts[strings.Join(lines[i:i+window], "\n")]++
		}
		files = append(files, fileLines{area, lines})
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			var body *ast.BlockStmt
			switch fn := n.(type) {
			case *ast.FuncDecl:
				body = fn.Body
			case *ast.FuncLit:
				body = fn.Body
			}
			if body == nil {
				return true
			}
			cc := complexity(body)
			sloc := fset.Position(body.End()).Line - fset.Position(body.Pos()).Line + 1
			mass := float64(cc) * math.Sqrt(float64(sloc))
			t.Functions++
			t.Mass += mass
			if cc > 10 {
				t.ErodedMass += mass
				name := "func literal"
				if fn, ok := n.(*ast.FuncDecl); ok {
					name = fn.Name.Name
				}
				heaviest = append(heaviest, heavy{fmt.Sprintf("%s:%d %s", filepath.ToSlash(rel), fset.Position(n.Pos()).Line, name), cc, sloc, mass})
			}
			return true
		})
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "slopmetrics:", err)
		os.Exit(2)
	}
	for _, f := range files {
		cloned := make([]bool, len(f.lines))
		for i := 0; i+window <= len(f.lines); i++ {
			if counts[strings.Join(f.lines[i:i+window], "\n")] > 1 {
				for j := i; j < i+window; j++ {
					cloned[j] = true
				}
			}
		}
		for _, c := range cloned {
			if c {
				byArea[f.area].CloneLines++
			}
		}
	}
	areas := make([]string, 0, len(byArea))
	for a, t := range byArea {
		areas = append(areas, a)
		all.Files += t.Files
		all.LOC += t.LOC
		all.Functions += t.Functions
		all.CloneLines += t.CloneLines
		all.Mass += t.Mass
		all.ErodedMass += t.ErodedMass
	}
	sort.Strings(areas)
	sort.Slice(heaviest, func(i, j int) bool { return heaviest[i].Mass > heaviest[j].Mass })
	heaviest = heaviest[:min(*top, len(heaviest))]
	if *asJSON {
		out := map[string]any{"verbosity": all.Verbosity(), "erosion": all.Erosion(), "loc": all.LOC, "functions": all.Functions, "files": all.Files, "areas": map[string]any{}, "heaviest": heaviest}
		for _, a := range areas {
			t := byArea[a]
			out["areas"].(map[string]any)[a] = map[string]any{"verbosity": t.Verbosity(), "erosion": t.Erosion(), "loc": t.LOC, "functions": t.Functions}
		}
		if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
			fmt.Fprintln(os.Stderr, "slopmetrics:", err)
			os.Exit(2)
		}
	} else {
		w := bufio.NewWriter(os.Stdout)
		_, _ = fmt.Fprintf(w, "%-14s %8s %9s %10s %8s\n", "area", "LOC", "functions", "verbosity", "erosion")
		for _, a := range areas {
			t := byArea[a]
			_, _ = fmt.Fprintf(w, "%-14s %8d %9d %10.3f %8.3f\n", a, t.LOC, t.Functions, t.Verbosity(), t.Erosion())
		}
		_, _ = fmt.Fprintf(w, "%-14s %8d %9d %10.3f %8.3f\n", "repository", all.LOC, all.Functions, all.Verbosity(), all.Erosion())
		_, _ = fmt.Fprintf(w, "\nHeaviest functions with CC > 10 (refactoring candidates):\n")
		for _, h := range heaviest {
			_, _ = fmt.Fprintf(w, "  %7.0f  CC %3d  SLOC %4d  %s\n", h.Mass, h.CC, h.SLOC, h.Where)
		}
		_, _ = fmt.Fprintln(w, "\nReference (SlopCodeBench): established repositories 0.15 verbosity, 0.31 erosion; agent code 0.33 and 0.68.")
		_, _ = fmt.Fprintln(w, "Verbosity here counts duplicated 6-line windows only, a lower bound of the paper's measure.")
		// bufio.Writer keeps the first write error, so Flush reports every
		// failed write above.
		if err := w.Flush(); err != nil {
			fmt.Fprintln(os.Stderr, "slopmetrics:", err)
			os.Exit(2)
		}
	}
	failed := false
	if *maxVerbosity > 0 && all.Verbosity() > *maxVerbosity {
		fmt.Fprintf(os.Stderr, "slop: verbosity %.3f exceeds %.3f\n", all.Verbosity(), *maxVerbosity)
		failed = true
	}
	if *maxErosion > 0 && all.Erosion() > *maxErosion {
		fmt.Fprintf(os.Stderr, "slop: erosion %.3f exceeds %.3f\n", all.Erosion(), *maxErosion)
		failed = true
	}
	if failed {
		os.Exit(1)
	}
}

// codeLines returns the file's code lines with whitespace collapsed and
// comments, blank lines, and lone braces removed, so formatting cannot create
// or hide a clone.
func codeLines(src string) []string {
	var out []string
	inBlock := false
	for raw := range strings.SplitSeq(src, "\n") {
		line := strings.TrimSpace(raw)
		if inBlock {
			if i := strings.Index(line, "*/"); i >= 0 {
				line, inBlock = strings.TrimSpace(line[i+2:]), false
			} else {
				continue
			}
		}
		if strings.HasPrefix(line, "/*") && !strings.Contains(line, "*/") {
			inBlock = true
			continue
		}
		if line == "" || strings.HasPrefix(line, "//") || line == "}" || line == "{" || line == ")" || line == "})" {
			continue
		}
		out = append(out, strings.Join(strings.Fields(line), " "))
	}
	return out
}

// complexity is McCabe's cyclomatic complexity: one plus each decision point.
func complexity(body *ast.BlockStmt) int {
	cc := 1
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncLit:
			return false // Closures are measured as their own functions.
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			cc++
		case *ast.CaseClause:
			if x.List != nil {
				cc++
			}
		case *ast.CommClause:
			if x.Comm != nil {
				cc++
			}
		case *ast.BinaryExpr:
			if x.Op == token.LAND || x.Op == token.LOR {
				cc++
			}
		}
		return true
	})
	return cc
}
