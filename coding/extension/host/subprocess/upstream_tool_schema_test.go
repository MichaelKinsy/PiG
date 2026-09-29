package subprocess

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
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
