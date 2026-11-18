//go:build !pig_strip_mcp && !pig_strip_node_extensions

package cli

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
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
