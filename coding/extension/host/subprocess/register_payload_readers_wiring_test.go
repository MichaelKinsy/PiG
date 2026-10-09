package subprocess

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestEveryRegisterPayloadFieldHasAHostReader fails for a RegisterPayload field that no production file reads. Pi's loader consumes each capability an extension registers (packages/coding-agent/src/core/extensions/loader.ts:273-497), so a wire declaration the host never reads is a registration nothing observes.
func TestEveryRegisterPayloadFieldHasAHostReader(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	read := map[string]bool{}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "protocol.go" {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				read[sel.Sel.Name] = true
			}
			return true
		})
	}
	payload := reflect.TypeFor[RegisterPayload]()
	for i := range payload.NumField() {
		if field := payload.Field(i).Name; !read[field] {
			t.Errorf("RegisterPayload.%s is declared on the wire and no production file reads it", field)
		}
	}
}
