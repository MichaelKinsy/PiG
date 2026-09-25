package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestRPCCommandSwitchCoversPinnedUpstreamTypes(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
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

	parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "cmd", "pig", "rpc_mode.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]struct{})
	ast.Inspect(parsed, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expression := range clause.List {
			literal, ok := expression.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			value := strings.Trim(literal.Value, `"`)
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
