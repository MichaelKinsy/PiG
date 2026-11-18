//go:build !pig_strip_llama_cpp && !pig_strip_mcp && !pig_strip_node_extensions

package cli

import (
	"path/filepath"
	"testing"
)

// Ports .upstream/v0.99.2/packages/coding-agent/src/core/package-manager.ts:970-986,996-1006 and resource-loader.ts:703-735:
// `-e builtin:<name>` paths come first, in the order given, then the enabled settings paths in builtInExtensions order. The
// production list is llama.cpp, codemode, tool-search, mcp, so an explicit mcp precedes llama.cpp and a file extension sits
// between the explicit and the default built-ins.
func TestExplicitBuiltinExtensionsPrecedeTheIndexOrderedOnes(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	writeResourceTestFiles(t, root, map[string]string{"agent/extensions/user.ts": "export default function() {}"})
	result := loadProductionExtensionSetIn(t, cwd, agentDir, []string{"-e", "builtin:mcp"})
	// Pi's extensionPaths: the -e paths, then the settings paths with the auto-discovered user.ts ahead of the default built-ins in index order
	// (package-manager.ts:969-986), each path once.
	want := []string{"builtin:mcp", filepath.Join(agentDir, "extensions", "user.ts")}
	for _, name := range cliBuiltinExtensionNames() {
		if name != "mcp" {
			want = append(want, "builtin:"+name)
		}
	}
	requirePaths(t, result.paths(), want)

	// A reload keeps that order (resource-loader.ts reload runs the same extensionPaths).
	reloaded, err := (&cliRuntimeBuilder{}).reloadBuildExtensions(t.Context(), result.build)
	if err != nil {
		t.Fatal(err)
	}
	var reloadedPaths []string
	for _, ext := range reloaded {
		reloadedPaths = append(reloadedPaths, ext.Path)
	}
	requirePaths(t, reloadedPaths, want)
}

// resource-loader.ts:566-575 with package-manager.ts:192-198 resourcePrecedenceRank: the -e sources come first, a built-in after the file ones, so
// `-e a.ts -e builtin:mcp` with a settings extension loads [a.ts, builtin:mcp, user.ts, the other built-ins]; a reload keeps that order.
func TestExplicitBuiltinExtensionFollowsTheDashEFilesAndPrecedesTheSettingsOnes(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	writeResourceTestFiles(t, root, map[string]string{
		"agent/extensions/user.ts": "export default function() {}",
		"cli/a.ts":                 "export default function() {}",
	})
	cliFile := filepath.Join(root, "cli", "a.ts")
	result := loadProductionExtensionSetIn(t, cwd, agentDir, []string{"-e", cliFile, "-e", "builtin:mcp"})
	want := []string{cliFile, "builtin:mcp", filepath.Join(agentDir, "extensions", "user.ts")}
	for _, name := range cliBuiltinExtensionNames() {
		if name != "mcp" {
			want = append(want, "builtin:"+name)
		}
	}
	requirePaths(t, result.paths(), want)
	reloaded, err := (&cliRuntimeBuilder{}).reloadBuildExtensions(t.Context(), result.build)
	if err != nil {
		t.Fatal(err)
	}
	var reloadedPaths []string
	for _, ext := range reloaded {
		reloadedPaths = append(reloadedPaths, ext.Path)
	}
	requirePaths(t, reloadedPaths, want)
}
