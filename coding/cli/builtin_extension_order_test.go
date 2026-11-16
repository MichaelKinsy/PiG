//go:build !pig_strip_llama_cpp && !pig_strip_mcp

// pi: packages/coding-agent/src/extensions/index.ts

package cli

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

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
	result := loadProductionExtensionSet(t, nil)
	// The production build enables every CLI built-in by default; their paths follow builtInExtensions' index order exactly.
	var want []string
	for _, name := range cliBuiltinExtensionNames() {
		want = append(want, "builtin:"+name)
	}
	if !slices.Contains(want, "builtin:llama.cpp") || !slices.Contains(want, "builtin:mcp") {
		t.Fatalf("builtInExtensions %q lacks llama.cpp or mcp", want)
	}
	requirePaths(t, result.paths(), want)
	runner := inproc.NewRunner(result.Extensions, result.build.CWD)
	t.Cleanup(func() { runner.Invalidate("") })
	names := commandNames(runner)
	if llama, mcp := slices.Index(names, "llama"), slices.Index(names, "mcp"); llama < 0 || mcp < 0 || llama > mcp {
		t.Fatalf("commands = %q, want /llama before /mcp", names)
	}
	llama, _ := runner.Command("llama")
	if llama.Description != "Manage llama.cpp router models" {
		t.Errorf("/llama description = %q", llama.Description)
	}
}

// Ports .upstream/v0.99.2/packages/coding-agent/src/extensions/index.ts:8 (llama.cpp has no `replaceable`) with
// src/core/extensions/runner.ts:resolveRegisteredCommands: another extension that registers /llama does not replace the built-in
// one, so both stay and take the invocation names llama:1 and llama:2 in load order (file extension first).
func TestBuiltinLlamaIsNotReplaceableByAnotherLlamaCommand(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	writeResourceTestFiles(t, root, map[string]string{"agent/extensions/user.ts": "export default function() {}"})
	result := loadProductionExtensionSetIn(t, cwd, agentDir, nil)
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

// Ports packages/coding-agent/test/resource-loader.test.ts "should skip disabledBuiltinExtensions even when settings or -e
// enable them" (1.0.4): --no-mcp is main.ts's `disabledBuiltinExtensions: ["mcp"]`, so the built-in mcp path is dropped
// although the project settings enable it and `-e builtin:mcp` names it, and its factory never runs.
// Pi: packages/coding-agent/src/cli/args.ts:38 (Args.noMcp).
func TestNoMcpSkipsTheBuiltinMcpExtensionEvenWhenSettingsOrDashEEnableIt(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	writeResourceTestFiles(t, root, map[string]string{"project/.pig/settings.json": `{"extensions":["+builtin:mcp"]}`})
	// Without --no-mcp the -e path and the project setting load the built-in mcp (first, as an explicit built-in); with it the path is dropped.
	requirePaths(t, loadProductionExtensionSetIn(t, cwd, agentDir, []string{"--approve", "-e", "builtin:mcp"}).paths(), append([]string{"builtin:mcp"}, builtinPaths("mcp")...))
	// A later built-in of one name wins (resource-loader.ts:378), so this mcp entry counts whether the dropped built-in's factory runs.
	mcpRuns := 0
	result := loadProductionExtensionSetIn(t, cwd, agentDir, []string{"--approve", "--no-mcp", "-e", "builtin:mcp"},
		extension.NamedInlineExtension{Name: "mcp", Builtin: true, Replaceable: true, Factory: func(extension.API) error { mcpRuns++; return nil }})
	if mcpRuns != 0 {
		t.Errorf("the --no-mcp built-in mcp factory ran %d times, want 0", mcpRuns)
	}
	requirePaths(t, result.paths(), builtinPaths("mcp"))
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Type == "error" {
			t.Fatalf("load error: %s", diagnostic.Message)
		}
	}
}

// Ports packages/coding-agent/test/llama-extension.test.ts:42 ("registers a native provider and /llama command") through the production
// loader: the built-in llama.cpp entry registers /llama with Pi's description and queues exactly one native provider, llama.cpp.
func TestBuiltinLlamaRegistersItsNativeProviderAndCommandThroughTheLoader(t *testing.T) {
	_, cwd, agentDir := resourceExtensionFixture(t)
	result := loadProductionExtensionSetIn(t, cwd, agentDir, nil)
	requirePaths(t, result.paths(), builtinPaths())
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Type == "error" {
			t.Fatalf("load error: %s", diagnostic.Message)
		}
	}
	command, ok := result.Extensions[0].Commands["llama"]
	if !ok || command.Description != "Manage llama.cpp router models" {
		t.Fatalf("/llama = %+v found=%t, want Pi's description", command, ok)
	}
	var ids []string
	for _, pending := range result.build.Runtime.PendingNativeProviderRegistrations() {
		ids = append(ids, pending.Provider.ID)
	}
	if !slices.Equal(ids, []string{"llama.cpp"}) {
		t.Fatalf("pending native providers = %q, want [llama.cpp]", ids)
	}
}
