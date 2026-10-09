//go:build !pig_strip_codemode && !pig_strip_tool_search

package cli

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Trust-split spec test 3 (docs/specs/extension-factory-trust.md): the production built-in table, loaded through the production extension set,
// registers codemode and tool_search inactive, `-builtin:<name>` and `--no-extensions` remove them, and a third-party extension that registers
// the same tool name replaces them (both are `replaceable`).
// Ports packages/coding-agent/src/core/package-manager.ts:970-1006 (builtin resources), resource-loader.ts:574 and :686 (noExtensions keeps only
// the -e paths) and resource-loader.ts omitReplacedExtensions.

func TestProductionBuiltInToolsLoadInactiveAndCanBeRemoved(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	inline := nativeBuiltInExtensions(nil)

	load := func(flags Args) *extensionSetResult {
		t.Helper()
		loader := newExtensionSetTestLoader(t, cwd, agentDir, flags, inline...)
		result, err := loader.Reload(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(result.Close)
		if len(result.Errors) != 0 {
			t.Fatalf("errors = %+v", result.Errors)
		}
		return result
	}
	has := func(result *extensionSetResult, path string) bool { return slices.Contains(result.paths(), path) }

	result := load(Args{})
	for path, tool := range map[string]string{"builtin:codemode": "codemode", "builtin:tool-search": "tool_search"} {
		var found bool
		for _, ext := range result.Extensions {
			if ext.Path != path {
				continue
			}
			registered, ok := ext.RegisteredTool(tool)
			found = ok
			if !ok || registered.Definition.DefaultActive == nil || *registered.Definition.DefaultActive {
				t.Errorf("%s registers %q with defaultActive %v, want it registered inactive", path, tool, registered.Definition.DefaultActive)
			}
		}
		if !found {
			t.Fatalf("paths %q lack %s with tool %q", result.paths(), path, tool)
		}
	}

	writeResourceTestFiles(t, root, map[string]string{"project/.pig/settings.json": `{"extensions":["-builtin:codemode"]}`})
	removed := load(Args{})
	if has(removed, "builtin:codemode") || !has(removed, "builtin:tool-search") {
		t.Fatalf("with -builtin:codemode in settings, paths = %q; want codemode gone and tool-search kept", removed.paths())
	}
	writeResourceTestFiles(t, root, map[string]string{"project/.pig/settings.json": `{}`})

	none := load(Args{NoExtensions: true})
	if has(none, "builtin:codemode") || has(none, "builtin:tool-search") {
		t.Fatalf("with --no-extensions, paths = %q; want no built-in", none.paths())
	}
	explicit := load(Args{NoExtensions: true, Extensions: []string{"builtin:codemode"}})
	if !has(explicit, "builtin:codemode") || has(explicit, "builtin:tool-search") {
		t.Fatalf("with --no-extensions -e builtin:codemode, paths = %q; want only codemode", explicit.paths())
	}
}

// A third-party extension that registers the tool name a replaceable built-in registers takes it over: the built-in is not loaded
// (resource-loader.ts omitReplacedExtensions), and a reload that no longer has the third-party extension brings the built-in back.
func TestThirdPartyToolReplacesTheProductionBuiltInOfTheSameName(t *testing.T) {
	_, cwd, agentDir := resourceExtensionFixture(t)
	thirdParty := extension.NamedInlineExtension{Name: "other-codemode", Factory: func(pi extension.API) error {
		pi.RegisterTool(extension.ToolDefinition{Name: "codemode", Label: "Other", Description: "third-party codemode", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)})
		return nil
	}}
	load := func(inline ...extension.InlineExtension) []string {
		t.Helper()
		loader := newExtensionSetTestLoader(t, cwd, agentDir, Args{}, inline...)
		result, err := loader.Reload(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(result.Close)
		if len(result.Errors) != 0 {
			t.Fatalf("errors = %+v", result.Errors)
		}
		return result.paths()
	}
	builtIns := nativeBuiltInExtensions(nil)
	if paths := load(builtIns...); !slices.Contains(paths, "builtin:codemode") {
		t.Fatalf("without a third party, paths = %q; want builtin:codemode", paths)
	}
	paths := load(append([]extension.InlineExtension{thirdParty}, builtIns...)...)
	if slices.Contains(paths, "builtin:codemode") || !slices.Contains(paths, "<inline:other-codemode>") || !slices.Contains(paths, "builtin:tool-search") {
		t.Fatalf("with a third-party codemode tool, paths = %q; want it to replace builtin:codemode and leave tool-search", paths)
	}
}
