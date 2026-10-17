package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Class is a test's mechanical classification. The classes follow the
// test-prune policy in docs/project/test-economy.md.
type Class string

const (
	// ClassOracle runs Pi, or reads Pi's own sources or tests, at run time.
	ClassOracle Class = "A-oracle"
	// ClassPiCited asserts values that carry a Pi source citation.
	ClassPiCited Class = "A-cited"
	// ClassInvariant checks a rule over generated or looped inputs.
	ClassInvariant Class = "B-invariant"
	// ClassSelfRef compares PiG output with a stored copy of PiG output.
	ClassSelfRef Class = "C-selfref"
	// ClassShape checks only that a type, method or field exists.
	ClassShape Class = "D-shape"
	// ClassUnclassified has no mechanical signal: hand-written example values.
	ClassUnclassified Class = "U-example"
)

// testFunc is one Test, Fuzz or Benchmark function.
type testFunc struct {
	Path     string
	Pkg      string
	Name     string
	Kind     string
	Line     int
	Lines    int
	Tags     string
	Class    Class
	Signals  []string
	Asserts  int
	Literals int
}

var (
	citeRE     = regexp.MustCompile(`(?:packages/[a-z-]+/(?:src|test)/[A-Za-z0-9_./-]+\.ts|\.upstream/|\bPi\b[^\n]{0,80}\.ts\b|upstream[^\n]{0,60}\.ts\b|[A-Za-z0-9_-]+\.ts\b)`)
	oracleRE   = regexp.MustCompile(`(?i)\b(?:oracle|pioracle|runpi|piprobe|probepi)\w*|node_modules/\.bin/pi|\.upstream`)
	goldenRE   = regexp.MustCompile(`(?i)golden|\.snap\b|snapshot\w*\.(?:txt|json|jsonl)|updateGolden|\*update(?:Goldens?)?\b`)
	shapeCall  = regexp.MustCompile(`\b(?:reflect\.TypeOf|reflect\.ValueOf|MethodByName|NumMethod|Implements|NumField|FieldByName|\.Method\()`)
	failMethod = map[string]bool{"Errorf": true, "Error": true, "Fatalf": true, "Fatal": true, "Fail": true, "FailNow": true}
	nameShape  = regexp.MustCompile(`(?i)(?:Shape|Surface|Exported|HasMethod|MethodSet|Fields?Exist|Implements|Interface)`)
)

// piGoldenBases holds the base names of the golden files the provenance
// manifest declares as produced by Pi; a test that reads one compares PiG with Pi.
var piGoldenBases []string

func readsPiGolden(text string) bool {
	for _, b := range piGoldenBases {
		if strings.Contains(text, b) {
			return true
		}
	}
	return false
}

func listTestFiles(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--", "*_test.go").Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for f := range strings.SplitSeq(string(out), "\x00") {
		if f == "" || strings.Contains(f, "node_modules/") || strings.Contains(f, "/testdata/") || strings.HasPrefix(f, "testdata/") {
			continue
		}
		files = append(files, f)
	}
	sort.Strings(files)
	return files, nil
}

func goBuildTags(src string) string {
	for line := range strings.SplitSeq(src, "\n") {
		if after, ok := strings.CutPrefix(line, "//go:build "); ok {
			return after
		}
		if strings.HasPrefix(line, "package ") {
			break
		}
	}
	return ""
}

// inventoryFiles parses every test file and classifies its test functions.
func inventoryFiles(root string, files []string, unitEvidence map[string]bool) ([]testFunc, error) {
	var all []testFunc
	fset := token.NewFileSet()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, err
		}
		src := string(data)
		f, err := parser.ParseFile(fset, rel, data, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		fileCited := citeRE.MatchString(headerComment(src))
		pkgDir := filepath.Dir(rel)
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Body == nil {
				continue
			}
			kind := testKind(fd)
			if kind == "" {
				continue
			}
			start := fset.Position(fd.Pos())
			end := fset.Position(fd.End())
			text := src[fset.Position(fd.Pos()).Offset:fset.Position(fd.End()).Offset]
			doc := ""
			if fd.Doc != nil {
				doc = fd.Doc.Text()
			}
			tf := testFunc{
				Path: rel, Pkg: pkgDir, Name: fd.Name.Name, Kind: kind,
				Line: start.Line, Lines: end.Line - start.Line + 1,
				Tags: goBuildTags(src),
			}
			classify(&tf, fd, text, doc, fileCited, unitEvidence[pkgDir+"|"+fd.Name.Name])
			all = append(all, tf)
		}
	}
	return all, nil
}

func headerComment(src string) string {
	var b strings.Builder
	sc := bufio.NewScanner(strings.NewReader(src))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "package ") {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func testKind(fd *ast.FuncDecl) string {
	name := fd.Name.Name
	if fd.Type.Params == nil || len(fd.Type.Params.List) != 1 {
		return ""
	}
	switch {
	case strings.HasPrefix(name, "Test") && name != "TestMain":
		return "Test"
	case strings.HasPrefix(name, "Fuzz"):
		return "Fuzz"
	case strings.HasPrefix(name, "Benchmark"):
		return "Benchmark"
	}
	return ""
}

func classify(tf *testFunc, fd *ast.FuncDecl, text, doc string, fileCited, evidenced bool) {
	var failCalls, tPassed, loops, cmps, lits int
	var shape bool
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && failMethod[sel.Sel.Name] {
				failCalls++
			}
			for _, a := range x.Args {
				if id, ok := a.(*ast.Ident); ok && (id.Name == "t" || id.Name == "b" || id.Name == "f") {
					tPassed++
				}
			}
		case *ast.ForStmt, *ast.RangeStmt:
			loops++
		case *ast.BinaryExpr:
			if x.Op == token.EQL || x.Op == token.NEQ {
				cmps++
			}
		case *ast.BasicLit:
			if x.Kind == token.STRING || x.Kind == token.INT {
				lits++
			}
		}
		return true
	})
	shape = shapeCall.MatchString(text)
	tf.Asserts = failCalls + tPassed
	tf.Literals = lits
	cited := fileCited || citeRE.MatchString(doc) || citeRE.MatchString(text)
	oracle := oracleRE.MatchString(text) || readsPiGolden(text)
	golden := goldenRE.MatchString(text)
	if evidenced {
		tf.Signals = append(tf.Signals, "unit-evidence")
	}
	if golden {
		tf.Signals = append(tf.Signals, "golden")
	}
	if shape {
		tf.Signals = append(tf.Signals, "reflect-shape")
	}
	if cited {
		tf.Signals = append(tf.Signals, "pi-cite")
	}
	if tf.Asserts == 0 && cmps == 0 {
		tf.Signals = append(tf.Signals, "no-assert")
	}
	if tf.Kind == "Fuzz" {
		tf.Signals = append(tf.Signals, "fuzz")
	}
	switch {
	case oracle || evidenced:
		tf.Class = ClassOracle
	case tf.Kind == "Fuzz":
		tf.Class = ClassInvariant
	case golden && !cited:
		tf.Class = ClassSelfRef
	case shape && failCalls <= 3 && !golden && (nameShape.MatchString(tf.Name) || tf.Lines <= 25):
		tf.Class = ClassShape
	case cited:
		tf.Class = ClassPiCited
	case loops > 0 && cmps > 0 && lits < 6:
		tf.Class = ClassInvariant
	default:
		tf.Class = ClassUnclassified
	}
}
