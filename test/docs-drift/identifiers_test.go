package docsdrift

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A Go identifier the docs name in a code span, such as `coding.CreateAgentSessionServices`, must be an exported declaration of that public package. The sample compile check covers programs; this covers the prose around them, where a renamed function survives longest.

// publicPackages maps the qualifier a page uses to the directory that declares the package.
var publicPackages = map[string]string{
	"coding": "coding",
	"agent":  "agent",
	"ai":     "ai",
	"tui":    "tui",
	"sdk":    "extensions/sdk",
	"env":    "env",
}

var identRef = regexp.MustCompile("`(coding|agent|ai|tui|sdk|env)\\.([A-Z][A-Za-z0-9_]*)(?:[.(\\[{`\\s]|$)")

// declaredNames returns the exported top-level names of the package in dir: types, functions, variables and constants. Methods and fields are not top-level names, so a reference such as `coding.Session.Send` is checked through its first name only.
func declaredNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	files, err := filepath.Glob(filepath.Join(repoRoot, filepath.FromSlash(dir), "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files in %s", dir)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.IsExported() {
					names[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							names[s.Name.Name] = true
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								names[n.Name] = true
							}
						}
					}
				}
			}
		}
	}
	return names
}

func TestDocumentedGoIdentifiersExist(t *testing.T) {
	declared := map[string]map[string]bool{}
	for q, dir := range publicPackages {
		declared[q] = declaredNames(t, dir)
		if len(declared[q]) < 5 {
			t.Fatalf("read only %d names of package %s; the reader is broken", len(declared[q]), q)
		}
	}
	checked := 0
	var problems []string
	for _, src := range corpus(t) {
		if strings.HasSuffix(src.Rel, "bug-ledger.md") {
			continue
		}
		for i, line := range strings.Split(src.Body, "\n") {
			for _, m := range identRef.FindAllStringSubmatch(line, -1) {
				checked++
				if !declared[m[1]][m[2]] {
					problems = append(problems, src.Rel+":"+itoa(i+1)+": `"+m[1]+"."+m[2]+"` is not an exported name of package "+publicPackages[m[1]])
				}
			}
		}
	}
	if checked < 50 {
		t.Fatalf("found only %d identifier references; the pattern is broken", checked)
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// The names an earlier SDK page taught (coding.NewServices, coding.ServicesOptions, coding.Services) never existed in this form; the reader must report them missing and report their real replacements present.
func TestIdentifierReaderSeparatesRealNamesFromInvented(t *testing.T) {
	names := declaredNames(t, "coding")
	for _, invented := range []string{"NewServices", "ServicesOptions", "Services"} {
		if names[invented] {
			t.Errorf("coding declares %s; the SDK page's old names are valid again, so revisit the docs", invented)
		}
	}
	for _, real := range []string{"CreateAgentSessionServices", "CreateAgentSessionServicesOptions", "AgentSessionServices", "NewRuntime"} {
		if !names[real] {
			t.Errorf("coding no longer declares %s", real)
		}
	}
}
