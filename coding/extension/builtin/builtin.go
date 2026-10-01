// Package builtin is the registry of upstream's built-in extensions that PiG implements natively: the
// `builtin:<name>` extension paths of codemode and tool-search (docs/specs/builtin-codemode-tool-search.md).
//
// Ports packages/coding-agent/src/extensions/index.ts (builtInExtensions).
package builtin

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/toolsearch"
)

// PathPrefix starts the path of a built-in extension, as in `builtin:codemode`.
//
// Ports packages/coding-agent/src/core/source-info.ts (BUILTIN_PATH_PREFIX).
const PathPrefix = "builtin:"

// Extension is one built-in extension.
type Extension struct {
	// Name is the part of the path after PathPrefix.
	Name string
	// Replaceable leaves the extension out when another extension registers a tool, command or flag it registers.
	Replaceable bool
	// Factory builds the extension: the Go counterpart of the factory that is the module's default export.
	Factory func() (extension.Extension, error)
}

// Path is the extension path, `builtin:<name>`.
func (e Extension) Path() string { return PathPrefix + e.Name }

// Options configures the built-in extensions that need the session's settings or a cache location.
type Options struct {
	// Codemode configures the codemode extension (unused when built with the `nocodemode` tag).
	Codemode CodemodeOptions
	// Mcp configures the MCP extension (unused when built with the `pig_strip_mcp` tag).
	Mcp McpOptions
}

// All lists the built-in extensions in upstream's order.
func All(options Options) []Extension {
	entries := append(codemodeEntries(options), Extension{Name: "tool-search", Replaceable: true, Factory: toolsearch.Extension})
	return append(entries, mcpEntries(options)...)
}

// Resolve returns the built-in extension a `builtin:<name>` path names. An unknown name fails with upstream's
// message.
//
// Ports packages/coding-agent/src/core/resource-loader.ts (loadExtensionPaths).
func Resolve(path string, options Options) (Extension, error) {
	for _, entry := range All(options) {
		if entry.Path() == path {
			return entry, nil
		}
	}
	return Extension{}, fmt.Errorf("Unknown built-in extension: %s", path)
}
