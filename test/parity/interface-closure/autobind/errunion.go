package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/test/parity/interface-closure/autobind/rules"
)

var (
	errUnionTestRe  = regexp.MustCompile(`\bTest\w+`)
	errUnionNilRe   = regexp.MustCompile(`(?is)\bnil\b[^.;]*\btrue\b`)
	errUnionTextRe  = regexp.MustCompile(`(?is)\bnon-empty\b[^.;]*\b(?:message|text|string)\b`)
	errUnionEmptyRe = regexp.MustCompile(`(?is)\bempty\b[^.;]*\bfalse\b`)
	errNewEmptyRe   = regexp.MustCompile(`errors\.New\(\s*""\s*\)`)
	errNewTextRe    = regexp.MustCompile(`errors\.New\(\s*"[^"]`)
	nilCaseRe       = regexp.MustCompile(`\bnil\b`)
)

// errorUnionPinned applies the S5s evidence rule: an upstream callback whose result is `boolean | string` has the Go error as its
// result only when the Go declaration's doc comment states all three outcomes (nil is true, a non-empty error message is the string,
// an empty message is false) and names a Go test of the package whose body exercises all three (a nil error, an error with an empty
// message, an error with a non-empty message). Without both, the union is not carried by the error and the row stays a gap.
func (d *detector) errorUnionPinned(returns string, c *sym) bool {
	if c == nil || !rules.IsBoolStringUnion(returns) {
		return false
	}
	doc := d.goDeclDoc(c)
	if doc == "" || !errUnionNilRe.MatchString(doc) || !errUnionTextRe.MatchString(doc) || !errUnionEmptyRe.MatchString(doc) {
		return false
	}
	for _, name := range errUnionTestRe.FindAllString(doc, -1) {
		if body, ok := d.packageTestBody(c.Dir, name); ok && nilCaseRe.MatchString(body) && errNewEmptyRe.MatchString(body) && errNewTextRe.MatchString(body) {
			return true
		}
	}
	return false
}

// goDeclDoc returns the doc comment of the struct field, method or function a symbol names, or "" when it has none.
func (d *detector) goDeclDoc(c *sym) string {
	if c.File == "" {
		return ""
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(d.ix.root, filepath.FromSlash(c.File)), nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return ""
	}
	var doc string
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.TypeSpec:
			st, ok := x.Type.(*ast.StructType)
			if !ok || x.Name.Name != c.Owner {
				return true
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if name.Name == c.Name && field.Doc != nil {
						doc = field.Doc.Text()
					}
				}
			}
		case *ast.FuncDecl:
			if x.Name.Name == c.Name && x.Doc != nil && (c.Owner == "" && x.Recv == nil || c.Owner != "" && x.Recv != nil) {
				doc = x.Doc.Text()
			}
		}
		return true
	})
	return strings.TrimSpace(doc)
}

// packageTestBody returns the source of the Test function name declared in a _test.go file of dir.
func (d *detector) packageTestBody(dir, name string) (string, bool) {
	files, _ := filepath.Glob(filepath.Join(d.ix.root, filepath.FromSlash(dir), "*_test.go"))
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, src, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, decl := range f.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == name && fd.Recv == nil && fd.Body != nil {
				return string(src[fset.Position(fd.Pos()).Offset:fset.Position(fd.End()).Offset]), true
			}
		}
	}
	return "", false
}
