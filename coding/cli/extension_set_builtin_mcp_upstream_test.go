//go:build !pig_strip_mcp

// Upstream resource-loader tests whose built-in extension is named `mcp`: a pig_strip_mcp build filters `builtin:mcp` out
// (enabledBuiltinExtensionPaths), so they run only in a build with MCP.

package cli

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports .upstream/v0.99.1/packages/coding-agent/test/resource-loader.test.ts:1107-1124 ("should load built-in extensions after file
// extensions with and without trust resolution"): a project override gives the built-in project scope, which must not move it ahead.
func TestUpstreamResourceLoaderLoadsBuiltinExtensionsAfterFileExtensions(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	writeResourceTestFiles(t, root, map[string]string{
		"agent/extensions/user.ts":   "export default function() {}",
		"project/.pig/settings.json": `{"extensions":["+builtin:mcp"]}`,
	})
	loader := newExtensionSetTestLoader(t, cwd, agentDir, Args{}, extension.NamedInlineExtension{Name: "mcp", Builtin: true, Factory: commandFactory("", "", nil)})
	want := []string{filepath.Join(agentDir, "extensions", "user.ts"), "builtin:mcp"}
	requirePaths(t, reloadExtensionSet(t, loader, func(extensionSetResult) (bool, error) { return true, nil }).paths(), want)
	requirePaths(t, reloadExtensionSet(t, loader, nil).paths(), want)
}

// Ports .upstream/v0.99.1/packages/coding-agent/test/resource-loader.test.ts:1126-1147 ("should disable built-in extensions with
// noExtensions unless loaded with -e builtin:<name>").
func TestUpstreamResourceLoaderNoExtensionsDisablesBuiltinsUnlessLoadedExplicitly(t *testing.T) {
	_, cwd, agentDir := resourceExtensionFixture(t)
	var loaded []string
	record := func(name string) func() { return func() { loaded = append(loaded, name) } }
	loader := newExtensionSetTestLoader(t, cwd, agentDir, Args{NoExtensions: true, Extensions: []string{"builtin:mcp", "builtin:missing"}},
		extension.NamedInlineExtension{Name: "mcp", Builtin: true, Factory: commandFactory("", "", record("mcp"))},
		extension.NamedInlineExtension{Name: "llama", Builtin: true, Factory: commandFactory("", "", record("llama"))},
	)
	result := reloadExtensionSet(t, loader, nil)
	requirePaths(t, result.paths(), []string{"builtin:mcp"})
	if want := []extensionSetError{{Path: "builtin:missing", Error: "Unknown built-in extension: builtin:missing"}}; !slices.Equal(result.Errors, want) {
		t.Errorf("errors = %+v, want %+v", result.Errors, want)
	}
	if !slices.Equal(loaded, []string{"mcp"}) {
		t.Errorf("loaded factories = %q, want [mcp]", loaded)
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/test/resource-loader.test.ts:1149-1183 ("should apply project built-in extension
// overrides after trust resolves"): built-in extensions wait until project settings are known, so the pre-trust set holds only the
// inline extension, and the inline extension is not loaded a second time.
func TestUpstreamResourceLoaderAppliesProjectBuiltinOverridesAfterTrust(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	writeResourceTestFiles(t, root, map[string]string{
		"agent/settings.json":        `{"extensions":["-builtin:mcp"]}`,
		"project/.pig/settings.json": `{"extensions":["+builtin:mcp","-builtin:llama"]}`,
	})
	var loaded []string
	record := func(name string) func() { return func() { loaded = append(loaded, name) } }
	loader := newExtensionSetTestLoader(t, cwd, agentDir, Args{},
		extension.NamedInlineExtension{Name: "mcp", Builtin: true, Factory: commandFactory("", "", record("mcp"))},
		extension.NamedInlineExtension{Name: "plain", Factory: commandFactory("", "", record("plain"))},
		extension.NamedInlineExtension{Name: "llama", Builtin: true, Factory: commandFactory("", "", record("llama"))},
	)
	var preTrust []string
	result := reloadExtensionSet(t, loader, func(pre extensionSetResult) (bool, error) {
		preTrust = pre.paths()
		return true, nil
	})
	requirePaths(t, preTrust, []string{"<inline:plain>"})
	requirePaths(t, result.paths(), []string{"builtin:mcp", "<inline:plain>"})
	if !slices.Equal(loaded, []string{"plain", "mcp"}) {
		t.Errorf("loaded factories = %q, want [plain mcp]", loaded)
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:906-975 (applyExtensionSourceInfo,
// findSourceInfoForPath, getDefaultSourceInfoForPath): a built-in extension's path is synthetic, so the extension and its
// commands get {path, source "builtin", scope "temporary", origin "top-level"}, even when a project entry enabled it (the
// resolved resource then has scope "project", which only the config selector reads).
func TestBuiltinExtensionSourceInfoIsSynthetic(t *testing.T) {
	for _, tc := range []struct{ name, global, project string }{
		{"default", "", ""},
		{"project override", `{"extensions":["-builtin:mcp"]}`, `{"extensions":["+builtin:mcp"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, cwd, agentDir := resourceExtensionFixture(t)
			files := map[string]string{}
			if tc.global != "" {
				files["agent/settings.json"] = tc.global
			}
			if tc.project != "" {
				files["project/.pig/settings.json"] = tc.project
			}
			writeResourceTestFiles(t, root, files)
			loader := newExtensionSetTestLoader(t, cwd, agentDir, Args{}, extension.NamedInlineExtension{Name: "mcp", Builtin: true, Factory: commandFactory("mcp", "built-in mcp", nil)})
			builtins, errs, _ := loader.loadBuiltinsAfter(nil)
			if len(errs) != 0 || len(builtins) != 1 {
				t.Fatalf("builtins = %d, errors = %+v", len(builtins), errs)
			}
			want := codingagent.PiSourceInfo{Path: "builtin:mcp", Source: "builtin", Scope: "temporary", Origin: "top-level"}
			if got := codingagent.PiSourceInfoValue(builtins[0].SourceInfo); got != want {
				t.Errorf("extension sourceInfo = %+v, want %+v", got, want)
			}
			if got := codingagent.PiSourceInfoValue(builtins[0].Commands["mcp"].SourceInfo); got != want {
				t.Errorf("command sourceInfo = %+v, want %+v", got, want)
			}
		})
	}
}
