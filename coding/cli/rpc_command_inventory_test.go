package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestRPCCommandSwitchCoversPinnedUpstreamTypes(t *testing.T) {
	root := testenv.ModuleRoot(t)
	upstream, err := os.ReadFile(filepath.Join(root, ".upstream", "current", "packages", "coding-agent", "src", "modes", "rpc", "rpc-types.ts"))
	if err != nil {
		t.Fatal(err)
	}
	commandSection, _, found := strings.Cut(string(upstream), "// RPC Slash Command")
	if !found {
		t.Fatal("upstream RPC command section marker missing")
	}
	matches := regexp.MustCompile(`type: "([a-z_]+)"`).FindAllStringSubmatch(commandSection, -1)
	want := make(map[string]struct{}, len(matches)+1)
	for _, match := range matches {
		want[match[1]] = struct{}{}
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "coding", "cli", "rpc_mode.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	typesFile, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "coding", "cli", "rpc_types.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The switch names each command by its RPCCommandType constant: resolve the constants to their literals.
	constants := make(map[string]string)
	ast.Inspect(typesFile, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if i < len(spec.Values) {
				if literal, ok := spec.Values[i].(*ast.BasicLit); ok && literal.Kind == token.STRING {
					constants[name.Name] = strings.Trim(literal.Value, `"`)
				}
			}
		}
		return true
	})
	got := make(map[string]struct{})
	ast.Inspect(parsed, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expression := range clause.List {
			var value string
			switch expression := expression.(type) {
			case *ast.BasicLit:
				if expression.Kind != token.STRING {
					continue
				}
				value = strings.Trim(expression.Value, `"`)
			case *ast.Ident:
				value = constants[expression.Name]
			}
			if _, expected := want[value]; expected {
				got[value] = struct{}{}
			}
		}
		return true
	})

	var missing []string
	for command := range want {
		if _, exists := got[command]; !exists {
			missing = append(missing, command)
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Fatalf("RPC switch is missing pinned upstream commands: %v", missing)
	}
}
