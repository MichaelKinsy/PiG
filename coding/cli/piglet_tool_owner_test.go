package cli

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi: packages/coding-agent/src/core/extensions/types.ts:2044 (RegisteredTool.sourceInfo).
func TestPigletToolOwnerNamesTheRegisteringEntry(t *testing.T) {
	registered := func(source string, info extension.SourceInfo) extension.RegisteredTool {
		return extension.RegisteredTool{Definition: extension.ToolDefinition{Name: "t"}, Source: source, SourceInfo: info}
	}
	extensions := []extension.Extension{
		{Name: "first", Tools: map[string]extension.RegisteredTool{"plain": registered("", extension.SourceInfo{}), "scoped": registered("mcp:docs", extension.SourceInfo{}), "object": registered("", extension.SourceInfo{Path: "/p"})}},
		{Name: "second", Tools: map[string]extension.RegisteredTool{"other": registered("", extension.SourceInfo{})}},
	}
	owner := pigletToolOwner(func() []extension.Extension { return extensions })
	for tool, want := range map[string]string{
		"plain":   "first",    // no per-tool source: the registering extension
		"scoped":  "mcp:docs", // pig additive D23: the tool's own source attribution wins
		"object":  "first",    // a provenance object names no entry
		"other":   "second",
		"read":    "", // registered by no extension: scoped as a built-in
		"missing": "",
	} {
		if got := owner(extension.ToolInfo{Name: tool}); got != want {
			t.Errorf("owner(%s) = %q, want %q", tool, got, want)
		}
	}
}
