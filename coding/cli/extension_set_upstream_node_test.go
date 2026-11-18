//go:build !pig_strip_node_extensions

package cli

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// Ports .upstream/v0.99.1/packages/coding-agent/test/resource-loader.test.ts:43-57 ("should not treat a project manifest as the owner
// of a project extension"): a project package.json that depends on a host-provided package is not an extension package.
func TestUpstreamResourceLoaderProjectManifestDoesNotOwnProjectExtension(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	writeResourceTestFiles(t, root, map[string]string{
		"project/package.json":                         `{"dependencies":{"@earendil-works/pi-coding-agent":"1.0.0"}}`,
		"project/.pig/extensions/project-extension.ts": "export default function() {}",
	})
	result := loadProductionExtensionSetIn(t, cwd, agentDir, []string{"--approve"})
	requirePaths(t, result.paths(), append([]string{filepath.Join(cwd, ".pig", "extensions", "project-extension.ts")}, builtinPaths()...))
	if len(result.Diagnostics) != 0 {
		t.Fatalf("a project manifest that depends on a host package produced diagnostics %+v", result.Diagnostics)
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/test/resource-loader.test.ts:59-86 ("should warn about host dependencies in an
// extension package manifest", regression for #9863).
func TestUpstreamResourceLoaderWarnsAboutHostDependenciesInPackageManifest(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	packageRoot := filepath.Join(root, "extension-package")
	writeResourceTestFiles(t, root, map[string]string{
		"extension-package/package.json":                    `{"dependencies":{"@earendil-works/pi-coding-agent":"1.0.0"}}`,
		"extension-package/extensions/package-extension.ts": "export default function() {}",
		"agent/settings.json":                               `{"packages":[` + jsonString(packageRoot) + `]}`,
	})
	result := loadProductionExtensionSetIn(t, cwd, agentDir, nil)
	want := []codingagent.AgentSessionRuntimeDiagnostic{{
		Type: "warning",
		Message: fmt.Sprintf(`Extension package "%s": %s`, filepath.Join(packageRoot, "package.json"),
			`Host-provided extension packages must be declared in peerDependencies with a "*" range, not dependencies: @earendil-works/pi-coding-agent. Installed copies can bypass the extension loader and create duplicate runtime modules.`),
	}}
	var got []codingagent.AgentSessionRuntimeDiagnostic
	for _, diagnostic := range result.Diagnostics {
		if strings.Contains(diagnostic.Message, "Host-provided extension packages") {
			got = append(got, diagnostic)
		}
	}
	requirePaths(t, result.paths(), append([]string{filepath.Join(packageRoot, "extensions", "package-extension.ts")}, builtinPaths()...))
	if !slices.Equal(got, want) {
		t.Fatalf("warnings = %+v; want %+v", got, want)
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/test/resource-loader.test.ts:1040-1083 ("should leave out replaceable extensions
// whose names another extension registers"): a third-party MCP extension registering /mcp replaces the built-in one.
func TestUpstreamResourceLoaderLeavesOutReplaceableExtensions(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	writeResourceTestFiles(t, root, map[string]string{
		"agent/extensions/other-mcp.ts": `export default function(pi) {
  pi.registerCommand("mcp", { description: "other mcp", handler: async () => {} });
}`,
	})
	// The production build's own replaceable built-ins: another extension that registers /mcp takes the command, the built-in mcp is left out.
	// A replaceable inline extension whose command a non-replaceable built-in (llama.cpp) also registers is left out too; a Piglet that strips
	// llama.cpp has no such built-in, so the inline extension stays, and it is the one that owns the command.
	result := loadProductionExtensionSetIn(t, cwd, agentDir, nil,
		extension.NamedInlineExtension{Name: "llama", Replaceable: true, Factory: commandFactory("llama", "inline llama", nil)},
	)
	// The settings extension, then every built-in in index order except the replaced mcp; <inline:llama> is left out.
	want := []string{filepath.Join(agentDir, "extensions", "other-mcp.ts")}
	for _, name := range cliBuiltinExtensionNames() {
		if name != "mcp" {
			want = append(want, "builtin:"+name)
		}
	}
	llamaStripped := pigstrip.Has(pigstrip.ListExtensions, "llama.cpp")
	llamaDescription := "Manage llama.cpp router models"
	if llamaStripped {
		want = append(want, "<inline:llama>")
		llamaDescription = "inline llama"
	}
	requirePaths(t, result.paths(), want)
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Type == "error" {
			t.Fatalf("load error: %s", diagnostic.Message)
		}
	}
	runner := inproc.NewRunner(result.Extensions, cwd, result.build.Runtime)
	t.Cleanup(func() { runner.Invalidate("") })
	for name, want := range map[string]string{"mcp": "other mcp", "llama": llamaDescription} {
		if got, ok := runner.Command(name); !ok || got.Description != want {
			t.Errorf("command %q = %+v found=%t; want %q", name, got, ok, want)
		}
	}
}
