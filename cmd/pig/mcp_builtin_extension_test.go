//go:build !pig_strip_mcp

package main

import (
	"slices"
	"testing"
)

// extensions/index.ts (Pi 0.99.2) lists llama.cpp, codemode, tool-search and mcp, in that order, as the built-in
// extensions of the CLI, and D2 adds PiG's own `pig-login` last: `pi config` shows each as a `builtin:<name>` resource, and the loader runs the ones that are enabled.
func TestCLIBuiltInExtensionsListMCPAfterToolSearch(t *testing.T) {
	want := []string{"llama.cpp", "codemode", "tool-search", "mcp", "pig-login"}
	if got := cliBuiltinExtensionNames(); !slices.Equal(got, want) {
		t.Errorf("cliBuiltinExtensionNames() = %q, want %q", got, want)
	}
	for _, input := range nativeBuiltInExtensions(nil) {
		if input.Name == "mcp" && (!input.Builtin || !input.Replaceable) {
			t.Errorf("mcp = %+v, want a replaceable built-in extension (extensions/index.ts: a third-party MCP extension that registers /mcp takes over)", input)
		}
	}
}
