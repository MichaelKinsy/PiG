//go:build !pig_strip_codemode && !pig_strip_tool_search

package cli

import (
	"encoding/json"
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

	load := func(args ...string) *productionExtensionSet {
		t.Helper()
		result := loadProductionExtensionSetIn(t, cwd, agentDir, append([]string{"--approve"}, args...))
		for _, diagnostic := range result.Diagnostics {
			if diagnostic.Type == "error" {
				t.Fatalf("load error: %s", diagnostic.Message)
			}
		}
		return result
	}

	result := load()
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
	removed := load()
	requirePaths(t, removed.paths(), builtinPaths("codemode"))
	writeResourceTestFiles(t, root, map[string]string{"project/.pig/settings.json": `{}`})

	none := load("--no-extensions")
	requirePaths(t, none.paths(), nil)
	explicit := load("--no-extensions", "-e", "builtin:codemode")
	requirePaths(t, explicit.paths(), []string{"builtin:codemode"})
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
		result := loadProductionExtensionSetIn(t, cwd, agentDir, nil, inline...)
		for _, diagnostic := range result.Diagnostics {
			if diagnostic.Type == "error" {
				t.Fatalf("load error: %s", diagnostic.Message)
			}
		}
		return result.paths()
	}
	requirePaths(t, load(), builtinPaths())
	requirePaths(t, load(thirdParty), append(builtinPaths("codemode"), "<inline:other-codemode>"))
}
