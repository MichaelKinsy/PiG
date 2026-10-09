//go:build !pig_strip_llama_cpp && !pig_strip_mcp && !pig_strip_codemode && !pig_strip_tool_search && !pig_strip_pig_login

package cli

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// builtinSelectionTools loads the production built-in rows (nativeBuiltInExtensions, plus the given third-party inline extensions) through the
// extension set exactly as a session does, binds the loaded extensions to an in-process runner and returns the load paths and the tool each
// registered. docs/specs/extension-factory-trust.md, required test 3.
func builtinSelectionTools(t *testing.T, flags Args, settings string, third ...extension.InlineExtension) ([]string, map[string]extension.ToolDefinition) {
	t.Helper()
	_, cwd, agentDir := resourceExtensionFixture(t)
	if settings != "" {
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	probe := newExtensionSetTestLoader(t, cwd, agentDir, flags)
	loader := newExtensionSetTestLoader(t, cwd, agentDir, flags, append(slices.Clone(third), nativeBuiltInExtensions(probe.Settings)...)...)
	result := reloadExtensionSet(t, loader, nil)
	if len(result.Errors) != 0 {
		t.Fatalf("load errors = %+v", result.Errors)
	}
	runner := inproc.NewRunner(result.Extensions, cwd)
	t.Cleanup(func() { runner.Invalidate("") })
	tools := map[string]extension.ToolDefinition{}
	for _, tool := range runner.Tools() {
		tools[tool.Definition.Name] = tool.Definition
	}
	return result.paths(), tools
}

func toolFactory(name, description string) extension.ExtensionFactory {
	return func(pi extension.API) error {
		pi.RegisterTool(extension.ToolDefinition{
			Name: name, Label: name, Description: description, Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
			Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
				return extension.AgentToolResult{}, nil
			},
		})
		return nil
	}
}

// codemode/index.ts:31-48 and tool-search/index.ts register their tool with `active: false`: the production built-in rows add `codemode` and
// `tool_search` to the runner's registry but leave both out of the default active set until `defaultTools` or setActiveTools selects them.
func TestProductionBuiltInRowsRegisterTheirToolsInactive(t *testing.T) {
	paths, tools := builtinSelectionTools(t, Args{}, "")
	if want := []string{"builtin:llama.cpp", "builtin:codemode", "builtin:tool-search", "builtin:mcp", "builtin:pig-login"}; !slices.Equal(paths, want) {
		t.Fatalf("paths = %q, want %q", paths, want)
	}
	for _, name := range []string{"codemode", "tool_search"} {
		tool, ok := tools[name]
		if !ok {
			t.Errorf("built-in tool %q is not registered", name)
			continue
		}
		if tool.DefaultActive == nil || *tool.DefaultActive {
			t.Errorf("built-in tool %q DefaultActive = %v, want an explicit false", name, tool.DefaultActive)
		}
	}
}

// package-manager.ts:970-1000 and resource-loader.ts:703-735: the `extensions` setting `-builtin:<name>` and `--no-extensions` remove a built-in row,
// and `-e builtin:<name>` loads one even under --no-extensions. The positive control (the row and its tool present without the setting) makes the
// removal observable.
func TestBuiltInRowsAreRemovedBySettingAndNoExtensionsAndLoadedByExplicitSelection(t *testing.T) {
	paths, tools := builtinSelectionTools(t, Args{}, "")
	if !slices.Contains(paths, "builtin:codemode") || tools["codemode"].Name == "" {
		t.Fatalf("positive control: paths %q, codemode tool %q", paths, tools["codemode"].Name)
	}

	paths, tools = builtinSelectionTools(t, Args{}, `{"extensions":["-builtin:codemode"]}`)
	if slices.Contains(paths, "builtin:codemode") || tools["codemode"].Name != "" || !slices.Contains(paths, "builtin:tool-search") || tools["tool_search"].Name == "" {
		t.Errorf("-builtin:codemode: paths %q, tools %v; want only codemode removed", paths, slices.Sorted(maps.Keys(tools)))
	}

	paths, tools = builtinSelectionTools(t, Args{NoExtensions: true}, "")
	if len(paths) != 0 || len(tools) != 0 {
		t.Errorf("--no-extensions: paths %q, tools %v; want none", paths, slices.Sorted(maps.Keys(tools)))
	}

	paths, tools = builtinSelectionTools(t, Args{NoExtensions: true, Extensions: []string{"builtin:codemode"}}, "")
	if !slices.Equal(paths, []string{"builtin:codemode"}) || tools["codemode"].Name == "" || len(tools) != 1 {
		t.Errorf("--no-extensions -e builtin:codemode: paths %q, tools %v; want only codemode", paths, slices.Sorted(maps.Keys(tools)))
	}
}

// runner.ts:369-379 getAllRegisteredTools: when two extensions register a tool of one name the earlier-loaded registration wins, and a third-party
// extension that registers the name of a replaceable built-in's tool takes it over: the built-in row is omitted (resource-loader.ts:114-157).
func TestThirdPartyToolOfABuiltInToolsNameReplacesIt(t *testing.T) {
	paths, tools := builtinSelectionTools(t, Args{}, "", extension.NamedInlineExtension{Name: "third-party", Factory: toolFactory("codemode", "third-party codemode")})
	// resource-loader.ts:114-157 omitReplacedExtensions: the replaceable codemode row is left out whole, and the other rows stay.
	if slices.Contains(paths, "builtin:codemode") || !slices.Contains(paths, "builtin:tool-search") {
		t.Fatalf("paths = %q, want the replaced codemode row omitted and tool-search kept", paths)
	}
	if got := tools["codemode"]; got.Description != "third-party codemode" || got.DefaultActive != nil {
		t.Fatalf("codemode tool = %q (DefaultActive %v), want the third-party registration", got.Description, got.DefaultActive)
	}
	if tools["tool_search"].Name == "" {
		t.Fatal("the built-in tool_search row was lost")
	}
}
