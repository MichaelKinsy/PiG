package factoryload_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const modulePath = "github.com/MichaelKinsy/PiG/"

type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func goList(t *testing.T, args ...string) []listedPackage {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list", "-json"}, args...)...)
	cmd.Dir = moduleRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %v: %v", args, err)
	}
	var packages []listedPackage
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	for decoder.More() {
		var p listedPackage
		if err := decoder.Decode(&p); err != nil {
			t.Fatal(err)
		}
		packages = append(packages, p)
	}
	return packages
}

// subprocessSide are the packages that load, serve or build subprocess-form extensions, fused Piglet members and Piglet frontends. Nothing in
// their transitive imports may be the compiled-in factory loader or the built-in table (docs/specs/extension-factory-trust.md, property 3).
var subprocessSide = []string{
	"./coding/extension/host/subprocess", "./coding/extension/host/runtimecell", "./coding/extension/host/cellpack",
	"./coding/extension/host/fusepack", "./coding/extension/host/invocation", "./coding/extension/source",
	"./internal/frontendpack", "./extensions/sdk/...",
}

// leafImplementations are built-in implementation packages that internal/codingagent links for their non-factory exports (renderers, command
// detection). They are not the loader or the table; the symbol check below still covers every other package.
var leafImplementations = []string{"coding/mcpext", "coding/piglogin", "internal/codingagent/llama"}

func TestSubprocessSideNeverImportsTheFactoryLoaderOrBuiltInTable(t *testing.T) {
	forbidden := []string{"coding/extension/factoryload", "coding/extension/builtin"}
	for _, pattern := range subprocessSide {
		for _, p := range goList(t, append([]string{"-deps"}, pattern)...) {
			path := strings.TrimPrefix(p.ImportPath, modulePath)
			for _, bad := range forbidden {
				if path == bad || strings.HasPrefix(path, bad+"/") {
					t.Errorf("%s transitively imports %s", pattern, path)
				}
			}
		}
	}
}

// referencedFactorySymbols lists the files of pkg that name extension.ExtensionFactory or extension.InlineExtension.
func referencedFactorySymbols(t *testing.T, pkg listedPackage) []string {
	t.Helper()
	var hits []string
	fset := token.NewFileSet()
	for _, name := range pkg.GoFiles {
		file, err := parser.ParseFile(fset, filepath.Join(pkg.Dir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if pkgIdent, ok := sel.X.(*ast.Ident); ok && pkgIdent.Name == "extension" && (sel.Sel.Name == "ExtensionFactory" || sel.Sel.Name == "InlineExtension") {
					hits = append(hits, name+":"+sel.Sel.Name)
				}
			}
			return true
		})
	}
	return hits
}

func TestSubprocessSideNeverReferencesTheFactoryTypes(t *testing.T) {
	seen := map[string]bool{}
	for _, pattern := range subprocessSide {
		for _, p := range goList(t, append([]string{"-deps"}, pattern)...) {
			path := strings.TrimPrefix(p.ImportPath, modulePath)
			if path == p.ImportPath || seen[path] || slices.Contains(leafImplementations, path) {
				continue
			}
			seen[path] = true
			if hits := referencedFactorySymbols(t, p); len(hits) != 0 {
				t.Errorf("%s (imported by the subprocess side) references %v", path, hits)
			}
		}
	}
	if len(seen) < 20 {
		t.Fatalf("checked only %d packages; the dependency walk found nothing", len(seen))
	}
}

// TestNoPackageLevelFactoryRegistry: a package-level variable whose type names a factory type, or an init function that mentions one, would let any
// linked package add compiled-in code (property 1).
func TestNoPackageLevelFactoryRegistry(t *testing.T) {
	names := func(n ast.Node) bool {
		found := false
		ast.Inspect(n, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && (id.Name == "ExtensionFactory" || id.Name == "InlineExtension") {
				found = true
			}
			return !found
		})
		return found
	}
	checked := 0
	for _, p := range goList(t, "./...") {
		fset := token.NewFileSet()
		for _, name := range p.GoFiles {
			file, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			checked++
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.GenDecl:
					if d.Tok != token.VAR {
						continue
					}
					for _, spec := range d.Specs {
						if names(spec) {
							t.Errorf("%s/%s declares a package-level variable naming a factory type", p.ImportPath, name)
						}
					}
				case *ast.FuncDecl:
					if d.Recv == nil && d.Name.Name == "init" && names(d) {
						t.Errorf("%s/%s: init mentions a factory type", p.ImportPath, name)
					}
				}
			}
		}
	}
	if checked < 500 {
		t.Fatalf("checked only %d files", checked)
	}
}
