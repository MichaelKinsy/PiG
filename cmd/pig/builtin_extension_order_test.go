package main

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// llamaBuiltInEntry is the production llama.cpp entry of builtInExtensions, the first entry of extensions/index.ts.
func llamaBuiltInEntry(t *testing.T) inlineExtension {
	t.Helper()
	for _, input := range builtInExtensions {
		if input.Name == llamaBuiltinName {
			return input
		}
	}
	t.Fatalf("builtInExtensions has no %q entry; extensions/index.ts lists llama.cpp first", llamaBuiltinName)
	return inlineExtension{}
}

func commandNames(runner *inproc.Runner) []string {
	var names []string
	for _, command := range runner.Commands() {
		names = append(names, command.InvocationName)
	}
	return names
}

// Ports .upstream/v0.99.2/packages/coding-agent/src/extensions/index.ts:7-14 with src/core/package-manager.ts:970-986 and
// src/core/resource-loader.ts:703-735: builtInExtensions is [llama.cpp, codemode, tool-search, mcp] and the loader runs the
// enabled `builtin:<name>` paths in that order, so /llama is listed before /mcp (Pi 0.99.2 `get_commands` probe: llama, then mcp).
func TestBuiltinExtensionsLoadInIndexOrderWithLlamaFirst(t *testing.T) {
	_, cwd, agentDir := resourceExtensionFixture(t)
	loader := newExtensionSetTestLoader(t, cwd, agentDir, CLIFlags{},
		llamaBuiltInEntry(t),
		inlineExtension{Name: "mcp", Builtin: true, Replaceable: true, Factory: commandFactory("mcp", "Manage MCP servers", nil)},
	)
	result := reloadExtensionSet(t, loader, nil)
	requirePaths(t, result.paths(), []string{"builtin:llama.cpp", "builtin:mcp"})
	runner := inproc.NewRunner(result.Extensions, cwd)
	t.Cleanup(func() { runner.Invalidate("") })
	if got, want := commandNames(runner), []string{"llama", "mcp"}; !slices.Equal(got, want) {
		t.Fatalf("commands = %q, want %q", got, want)
	}
	llama, _ := runner.Command("llama")
	if llama.Description != "Manage llama.cpp router models" {
		t.Errorf("/llama description = %q", llama.Description)
	}
}

// Ports .upstream/v0.99.2/packages/coding-agent/src/core/package-manager.ts:970-986,996-1006 and resource-loader.ts:703-735:
// `-e builtin:<name>` paths come first, in the order given, then the enabled settings paths in builtInExtensions order. The
// production list is llama.cpp, codemode, tool-search, mcp, so an explicit mcp precedes llama.cpp and a file extension sits
// between the explicit and the default built-ins.
func TestExplicitBuiltinExtensionsPrecedeTheIndexOrderedOnes(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	writeResourceTestFiles(t, root, map[string]string{"agent/extensions/user.ts": "export default function() {}"})
	loader := newExtensionSetTestLoader(t, cwd, agentDir, CLIFlags{Extensions: []string{"builtin:mcp"}},
		llamaBuiltInEntry(t),
		inlineExtension{Name: "mcp", Builtin: true, Replaceable: true, Factory: commandFactory("mcp", "Manage MCP servers", nil)},
	)
	result := reloadExtensionSet(t, loader, nil)
	requirePaths(t, result.paths(), []string{"builtin:mcp", filepath.Join(agentDir, "extensions", "user.ts"), "builtin:llama.cpp"})
}

// Ports .upstream/v0.99.2/packages/coding-agent/src/extensions/index.ts:8 (llama.cpp has no `replaceable`) with
// src/core/extensions/runner.ts:resolveRegisteredCommands: another extension that registers /llama does not replace the built-in
// one, so both stay and take the invocation names llama:1 and llama:2 in load order (file extension first).
func TestBuiltinLlamaIsNotReplaceableByAnotherLlamaCommand(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	writeResourceTestFiles(t, root, map[string]string{"agent/extensions/user.ts": "export default function() {}"})
	loader := newExtensionSetTestLoader(t, cwd, agentDir, CLIFlags{}, llamaBuiltInEntry(t))
	result := reloadExtensionSet(t, loader, nil)
	var llamaExt extension.Extension
	for _, ext := range result.Extensions {
		if ext.Path == "builtin:llama.cpp" {
			llamaExt = ext
		}
	}
	if llamaExt.Replaceable || !llamaExt.Hidden {
		t.Fatalf("built-in llama.cpp extension = replaceable %t hidden %t, want a hidden extension that nothing replaces", llamaExt.Replaceable, llamaExt.Hidden)
	}
	other := extension.Extension{Path: "/x/other.ts", Commands: map[string]extension.RegisteredCommand{"llama": {Name: "llama"}}, CommandOrder: []string{"llama"}}
	runner := inproc.NewRunner([]extension.Extension{other, llamaExt}, cwd)
	t.Cleanup(func() { runner.Invalidate("") })
	if got, want := commandNames(runner), []string{"llama:1", "llama:2"}; !slices.Equal(got, want) {
		t.Fatalf("commands = %q, want %q", got, want)
	}
}
