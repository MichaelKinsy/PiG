//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/tsruntime"
)

// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:402
func TestUpstreamRunnerRejectsToolWithoutParameterSchema(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "missing-parameters.js")
	write(t, path, `export default function(pi) {
 pi.registerTool({name:"noop",label:"No-op",description:"Do nothing",execute:async()=>({content:[{type:"text",text:"ok"}]})});
}`)
	host := NewHost(root)
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, failures := host.LoadAll(t.Context(), []ExtConfig{{Name: "missing-parameters", Source: path, Enabled: true}})
	if len(loaded) != 0 || len(failures) != 1 {
		t.Fatalf("loaded=%v failures=%v", loaded, failures)
	}
	outer, ok := errors.AsType[*ExtensionLoadError](failures[0])
	if !ok || outer.Path != path {
		t.Fatalf("load error=%+v", failures[0])
	}
	failure, ok := errors.AsType[*FactoryLoadError](failures[0])
	if !ok {
		t.Fatalf("missing factory failure: %v", failures[0])
	}
	want := `Failed to load extension: Tool "noop" registered by extension "` + path + `" must define an object parameter schema.`
	if failure.Message != want || outer.Err.Error() != want {
		t.Fatalf("source error=%q (reported as %q); want %q", failure.Message, outer.Err, want)
	}
}

// loader.ts:289-295: registerTool reads tool.parameters first, so a definition without a name fails the schema check (naming the tool "undefined") and a null definition throws the TypeError of the property read.
func TestRegisterToolWithoutANameFailsLikePisLoader(t *testing.T) {
	for _, tc := range []struct{ name, call, want string }{
		{"empty-object", `pi.registerTool({})`, `Failed to load extension: Tool "undefined" registered by extension "%s" must define an object parameter schema.`},
		{"null", `pi.registerTool(null)`, `Failed to load extension: Cannot read properties of null (reading 'parameters')`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, tc.name+".js")
			write(t, path, "export default function(pi) { "+tc.call+" }")
			host := NewHost(root)
			t.Cleanup(func() { host.Shutdown("test done") })
			_, failures := host.LoadAll(t.Context(), []ExtConfig{{Name: tc.name, Source: path, Enabled: true}})
			if len(failures) != 1 {
				t.Fatalf("failures=%v", failures)
			}
			failure, ok := errors.AsType[*FactoryLoadError](failures[0])
			want := tc.want
			if tc.name == "null" && TypeScriptRuntimeRequest().ID != tsruntime.Node {
				// The Bun row runs JavaScriptCore, which writes its own message for the property read (docs/site/docs/typescript-runtimes.md, kind bun).
				want = `Failed to load extension: null is not an object (evaluating 'definition.parameters')`
			}
			if strings.Contains(want, "%s") {
				want = fmt.Sprintf(want, path)
			}
			if !ok || failure.Message != want {
				t.Fatalf("error=%v; want %q", failures[0], want)
			}
		})
	}
}

// loader.ts:273-285 validates each registration before replacing an earlier definition.
func TestToolSchemasValidatedBeforeDeduplication(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", `"object"`, "1", "false"} {
		t.Run(raw, func(t *testing.T) {
			reg := &RegisterPayload{Name: "schema", Tools: []ToolDecl{{Name: "noop", Parameters: json.RawMessage(raw)}, {Name: "noop", Parameters: json.RawMessage(`{}`)}}}
			if err := validateRegisterPayload("schema", reg); err == nil {
				t.Fatal("invalid first registration was hidden by a later valid schema")
			}
		})
	}
	if err := validateRegisterPayload("schema", &RegisterPayload{Name: "schema", Tools: []ToolDecl{{Name: "noop", Parameters: json.RawMessage(`{}`)}}}); err != nil {
		t.Fatal(err)
	}
}

// Pi serves the TypeBox package namespace for typebox, typebox/value and typebox/compile (virtual-modules.ts:15-20), so an extension that default-imports TypeBox, as its 1.x documentation does, links and registers its tool.
func TestTypeBoxDefaultImportsLoadInANodeExtension(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "typebox-default.ts")
	write(t, path, `import Type from "typebox";
import Value from "typebox/value";
import Compile from "@sinclair/typebox/compile";
export default function(pi) {
 const parameters = Type.Object({ path: Type.String() });
 if (!Value.Check(parameters, { path: "a" }) || typeof Compile !== "function") throw new Error("TypeBox default exports differ");
 pi.registerTool({name:"typed",label:"Typed",description:"Typed",parameters,execute:async()=>({content:[{type:"text",text:"ok"}]})});
}`)
	host := NewHost(root)
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, failures := host.LoadAll(t.Context(), []ExtConfig{{Name: "typebox-default", Source: path, Enabled: true}})
	if len(failures) != 0 || len(loaded) != 1 {
		t.Fatalf("loaded=%d failures=%v", len(loaded), failures)
	}
	if _, ok := loaded[0].Tools["typed"]; !ok {
		t.Fatalf("tool not registered: %v", loaded[0].Tools)
	}
}
