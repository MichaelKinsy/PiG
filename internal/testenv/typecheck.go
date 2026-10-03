package testenv

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
)

// exportData maps an import path to the compiled export data of the package and its dependencies, so a snippet is
// type-checked against the packages as built. One `go list` runs for each distinct set of imports.
var exportData sync.Map // sorted import paths joined by "\n" -> func() (map[string]string, error)

func exportsOf(paths []string) (map[string]string, error) {
	key := strings.Join(paths, "\n")
	load, _ := exportData.LoadOrStore(key, sync.OnceValues(func() (map[string]string, error) {
		goTool, err := exec.LookPath("go")
		if err != nil {
			return nil, err
		}
		args := append([]string{"list", "-export", "-deps", "-f", "{{.ImportPath}}={{.Export}}"}, paths...)
		out, err := exec.Command(goTool, args...).Output()
		if err != nil {
			return nil, fmt.Errorf("go list -export: %w", err)
		}
		exports := map[string]string{}
		for line := range strings.SplitSeq(string(out), "\n") {
			if path, export, ok := strings.Cut(line, "="); ok {
				exports[path] = export
			}
		}
		return exports, nil
	}))
	return load.(func() (map[string]string, error))()
}

// TypeErrors type-checks body, the statements of a function, in a file that imports each package of imports (alias to
// import path) as built, and returns the type errors. Go reports a variable no statement reads and an import no
// statement uses as errors; a snippet declares values to test their types, so those errors are dropped.
func TypeErrors(t testing.TB, imports map[string]string, body string) []string {
	t.Helper()
	aliases := make([]string, 0, len(imports))
	paths := make([]string, 0, len(imports))
	for alias, path := range imports {
		aliases = append(aliases, alias)
		paths = append(paths, path)
	}
	slices.Sort(aliases)
	slices.Sort(paths)
	exports, err := exportsOf(paths)
	if err != nil {
		t.Fatal(err)
	}
	var source strings.Builder
	source.WriteString("package snippet\n\n")
	for _, alias := range aliases {
		fmt.Fprintf(&source, "import %s %q\n", alias, imports[alias])
	}
	source.WriteString("\nfunc snippet() {\n" + body + "\n}\n")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "snippet.go", source.String(), 0)
	if err != nil {
		t.Fatalf("parse snippet: %v\n%s", err, source.String())
	}
	var errs []string
	config := types.Config{
		Importer: importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) { return os.Open(exports[path]) }),
		Error: func(err error) {
			if message := err.Error(); !strings.Contains(message, "declared and not used") && !strings.Contains(message, "imported and not used") {
				errs = append(errs, err.Error())
			}
		},
	}
	_, _ = config.Check("snippet", fset, []*ast.File{file}, nil)
	return errs
}

// ExpectCompiles fails the test when the snippet has a type error.
func ExpectCompiles(t testing.TB, name string, imports map[string]string, body string) {
	t.Helper()
	if errs := TypeErrors(t, imports, body); len(errs) > 0 {
		t.Fatalf("%s: unexpected type errors: %v", name, errs)
	}
}

// ExpectTypeError fails the test when the snippet compiles: upstream's @ts-expect-error.
func ExpectTypeError(t testing.TB, name string, imports map[string]string, body string) {
	t.Helper()
	if errs := TypeErrors(t, imports, body); len(errs) == 0 {
		t.Fatalf("%s: compiled, want a type error", name)
	}
}
