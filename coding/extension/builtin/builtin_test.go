//go:build !pig_strip_codemode && !pig_strip_tool_search

package builtin_test

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"

	"github.com/MichaelKinsy/PiG/coding/extension/builtin"
)

func names(extensions []builtin.Extension) []string {
	var out []string
	for _, extension := range extensions {
		out = append(out, extension.Name)
	}
	return out
}

// .upstream/v0.99.1/packages/coding-agent/src/extensions/index.ts:6-14 lists codemode, tool-search and mcp as
// replaceable built-ins, in that order after llama.cpp. Only the entries this registry owns are compared.
func TestAllListsTheCodemodeAndToolSearchBuiltinsAsReplaceable(t *testing.T) {
	all := builtin.All(builtin.Options{})
	var got []string
	for _, extension := range all {
		if extension.Name == "codemode" || extension.Name == "tool-search" {
			got = append(got, extension.Name)
			if !extension.Replaceable {
				t.Errorf("%s: Replaceable = false, upstream marks it replaceable (index.ts:10-11)", extension.Name)
			}
		}
	}
	if want := []string{"codemode", "tool-search"}; !slices.Equal(got, want) {
		t.Fatalf("built-ins %v (registry %v), want %v in that order", got, names(all), want)
	}
}

// resource-loader.ts:716-720 reports an unknown name as "Unknown built-in extension: <path>".
func TestResolveReportsUnknownBuiltinsLikePi(t *testing.T) {
	_, err := builtin.Resolve("builtin:nope", builtin.Options{})
	if err == nil || err.Error() != "Unknown built-in extension: builtin:nope" {
		t.Fatalf("Resolve(builtin:nope) error = %v", err)
	}
	extension, err := builtin.Resolve("builtin:tool-search", builtin.Options{})
	if err != nil || extension.Name != "tool-search" {
		t.Fatalf("Resolve(builtin:tool-search) = %+v, %v", extension, err)
	}
}

// index.ts:14 and :41 register both tools inactive (`defaultActive: false`) with `model-only` exposure (tool.ts), and a
// built-in extension's source info is `builtin:<name>` (docs/specs/builtin-codemode-tool-search.md, "Identity and
// naming").
func TestFactoriesRegisterTheirToolInactive(t *testing.T) {
	for name, tool := range map[string]string{"codemode": "codemode", "tool-search": "tool_search"} {
		entry, err := builtin.Resolve("builtin:"+name, builtin.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if entry.Factory == nil {
			t.Fatalf("%s: no factory", name)
		}
		ext, err := factoryload.LoadExtensionFromFactory(entry.Factory, ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "builtin:"+name+"")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		registered, ok := ext.RegisteredTool(tool)
		if !ok || len(ext.RegisteredTools()) != 1 {
			t.Fatalf("%s registers %v, want only %s", name, ext.RegisteredTools(), tool)
		}
		definition := registered.Definition
		if definition.DefaultActive == nil || *definition.DefaultActive {
			t.Errorf("%s: DefaultActive = %v, want false", name, definition.DefaultActive)
		}
		if definition.Exposure != "model-only" {
			t.Errorf("%s: Exposure = %q, want model-only", name, definition.Exposure)
		}
		// 0.99.2 (tool-search/tool.ts): tool_search has a fixed description and no prepareLoadout; codemode still builds
		// its description from the loadout.
		if wantLoadout := name == "codemode"; definition.Execute == nil || (definition.PrepareLoadout != nil) != wantLoadout {
			t.Errorf("%s: Execute %v PrepareLoadout %v, want PrepareLoadout %v", name, definition.Execute != nil, definition.PrepareLoadout != nil, wantLoadout)
		}
	}
}
