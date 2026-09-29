package tools

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"testing"
)

// Pi ansi-utils.test.ts calls stripAnsi through an unchecked unknown-value cast. Go's concrete byte-slice API cannot receive those dynamic values. This guards that compile-time boundary, not a fabricated runtime TypeError; a nil byte slice denotes empty text rather than JavaScript null.
func TestStripANSIRejectsNonTextAtCompileTime(t *testing.T) {
	signature := reflect.TypeOf(StripANSI)
	if want := reflect.TypeFor[func([]byte) []byte](); signature != want {
		t.Errorf("StripANSI signature=%v; want %v", signature, want)
	}
	for _, tc := range []struct {
		name, argument string
		accepted       bool
	}{
		// Go has no separate undefined/null runtime values; both negative probes retain the dynamic, non-byte-slice type at the call boundary.
		{name: "undefined", argument: "any(nil)"},
		{name: "null", argument: "any(nil)"},
		{name: "number", argument: "123"},
		{name: "object", argument: "map[string]any{}"},
		{name: "boxed text", argument: `struct{ Value string }{Value: "x"}`},
		{name: "ordinary text bytes", argument: `[]byte("x")`, accepted: true},
		{name: "empty text bytes", argument: "[]byte{}", accepted: true},
		{name: "nil text bytes", argument: "[]byte(nil)", accepted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Use the actual compiled function type, so a source overlay widening the API also changes this declaration. No test-only implementation stands in for StripANSI.
			source := fmt.Sprintf("package probe\nfunc StripANSI%s\nfunc probe() { StripANSI(%s) }\n", strings.TrimPrefix(signature.String(), "func"), tc.argument)
			files := token.NewFileSet()
			file, err := parser.ParseFile(files, "probe.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = new(types.Config).Check("probe", files, []*ast.File{file}, nil)
			if accepted := err == nil; accepted != tc.accepted {
				t.Fatalf("argument %s accepted=%v; want %v; error=%v", tc.argument, accepted, tc.accepted, err)
			}
		})
	}
}
