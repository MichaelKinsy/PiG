package tlsdistrust_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	hookPackage   = "github.com/MichaelKinsy/PiG/internal/tlsdistrust"
	helperPackage = "github.com/MichaelKinsy/PiG/internal/tlsdistrust/tlsdistrusttest"
)

// Outbound TLS that takes its configuration from the default transport: a package that does this must link the hook, whose init
// hardens http.DefaultTransport.
var defaultTransportUses = map[string]bool{
	"net/http.DefaultClient": true, "net/http.DefaultTransport": true,
	"net/http.Get": true, "net/http.Post": true, "net/http.Head": true, "net/http.PostForm": true,
}

// Outbound TLS with a configuration of its own: the file must call into the hook (Harden or HardenTransport) itself.
var ownConfigUses = map[string]bool{
	"crypto/tls.Client": true, "crypto/tls.Dial": true, "crypto/tls.DialWithDialer": true,
	"github.com/gorilla/websocket.DefaultDialer":                         true,
	"github.com/aws/aws-sdk-go-v2/aws/transport/http.NewBuildableClient": true,
}

var ownConfigLiterals = map[string]bool{
	"crypto/tls.Config": true, "net/http.Transport": true, "github.com/gorilla/websocket.Dialer": true,
}

// Directories that are not PiG's own runtime: the repository's test, benchmark, tooling and documentation trees, and the vendored
// AWS fork, whose clients PiG builds in ai/bedrock.go.
var skippedTrees = map[string]string{
	"test": "test fixtures", "bench": "benchmarks", "docs": "documentation", "examples": "examples", "plans": "plans",
	"research": "research", "handoffs": "handoffs", "automation": "CI tooling", "third_party": "vendored forks; the AWS fork's clients are built and hardened in ai/bedrock.go",
	"tlsdistrusttest": "the test helper",
}

type finding struct {
	file     string
	defaults bool
	own      bool
	hooked   bool
	linked   bool
}

func scan(t *testing.T, path string) finding {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	names := map[string]string{}
	result := finding{file: path}
	for _, spec := range file.Imports {
		importPath, _ := strconv.Unquote(spec.Path.Value)
		name := filepath.Base(importPath)
		switch importPath {
		case "github.com/gorilla/websocket":
			name = "websocket"
		case "github.com/aws/aws-sdk-go-v2/aws/transport/http":
			name = "http"
		}
		if spec.Name != nil {
			name = spec.Name.Name
		}
		names[name] = importPath
		if importPath == hookPackage {
			result.linked = true
		}
	}
	key := func(expr ast.Expr) string {
		selector, ok := expr.(*ast.SelectorExpr)
		if !ok {
			return ""
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || names[pkg.Name] == "" {
			return ""
		}
		return names[pkg.Name] + "." + selector.Sel.Name
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.SelectorExpr:
			switch k := key(node); {
			case defaultTransportUses[k]:
				result.defaults = true
			case ownConfigUses[k]:
				result.own = true
			case k == hookPackage+".Harden" || k == hookPackage+".HardenTransport":
				result.hooked = true
			}
		case *ast.CompositeLit:
			k := key(node.Type)
			switch {
			case ownConfigLiterals[k]:
				result.own = true
			case k == "net/http.Client":
				// A client with a Transport of its own is covered where that transport is built; one without uses http.DefaultTransport.
				explicit := false
				for _, element := range node.Elts {
					if pair, ok := element.(*ast.KeyValueExpr); ok {
						if name, ok := pair.Key.(*ast.Ident); ok && name.Name == "Transport" {
							explicit = true
						}
					}
				}
				result.defaults = result.defaults || !explicit
			}
		}
		return true
	})
	return result
}

// Every outbound TLS client of the repository's own packages is behind the hook: a package that builds a TLS configuration or a
// transport applies Harden or HardenTransport in the same file, and a package that relies on http.DefaultTransport imports the package
// whose init hardens it. A new client that does neither fails here.
func TestEveryOutboundTLSClientIsBehindTheHook(t *testing.T) {
	root := filepath.Join("..", "..")
	type pkg struct{ findings []finding }
	packages := map[string]*pkg{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			rel, _ := filepath.Rel(root, path)
			if name == ".git" || name == "node_modules" || name == "testdata" || strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			if _, skipped := skippedTrees[name]; skipped && (filepath.Dir(rel) == "." || name == "tlsdistrusttest") {
				return filepath.SkipDir
			}
			if rel == "extensions" || rel == "piglets" {
				return filepath.SkipDir // separate modules; see TestPigletModulesCarryTheirOwnCopy
			}
			if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if strings.HasSuffix(filepath.Dir(path), filepath.Join("internal", "tlsdistrust")) {
			return nil
		}
		f := scan(t, path)
		dir := filepath.Dir(path)
		if packages[dir] == nil {
			packages[dir] = &pkg{}
		}
		packages[dir].findings = append(packages[dir].findings, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var violations []string
	for dir, p := range packages {
		var uses, linked bool
		for _, f := range p.findings {
			uses = uses || f.defaults || f.own
			linked = linked || f.linked
			if f.own && !f.hooked {
				violations = append(violations, f.file+": builds a TLS client or transport without tlsdistrust.Harden or HardenTransport")
			}
		}
		if uses && !linked {
			violations = append(violations, dir+": a package that dials TLS must import internal/tlsdistrust")
		}
	}
	sort.Strings(violations)
	for _, violation := range violations {
		t.Error(violation)
	}
	if len(packages) < 50 {
		t.Fatalf("the scan found only %d packages; the walk is broken", len(packages))
	}
}

// The helper builds a certificate with a compromised key; production code must never link it.
func TestNoProductionCodeImportsTheTestHelper(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "tlsdistrusttest" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), `"`+helperPackage+`"`) {
			t.Errorf("%s imports %s", path, helperPackage)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Piglet modules depend on the SDK alone and cannot import the root module's internal packages, so each one that dials TLS
// carries a byte-identical copy of this package.
func TestPigletModulesCarryAnIdenticalCopy(t *testing.T) {
	want, err := os.ReadFile("tlsdistrust.go")
	if err != nil {
		t.Fatal(err)
	}
	for module, user := range map[string]string{
		"standard": filepath.Join("piglets", "standard", "extensions", "bunruntime", "extension.go"),
		"aot":      filepath.Join("piglets", "aot", "extensions", "aotruntime", "worker.go"),
	} {
		root := filepath.Join("..", "..")
		got, err := os.ReadFile(filepath.Join(root, "piglets", module, "internal", "tlsdistrust", "tlsdistrust.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("piglets/%s/internal/tlsdistrust/tlsdistrust.go differs from internal/tlsdistrust/tlsdistrust.go: copy it again", module)
		}
		source, err := os.ReadFile(filepath.Join(root, user))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(source), `_ "github.com/MichaelKinsy/PiG/piglets/`+module+`/internal/tlsdistrust"`) {
			t.Errorf("%s does not import its module's copy", user)
		}
	}
}
