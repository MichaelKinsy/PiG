//go:build !pig_strip_tool_search

package builtin

import (
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/toolsearch"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// toolSearchEntries is the tool-search registry row, absent while tool-search is stripped at runtime as in a build without it.
// pig additive (D92): a runtime stripped tool-search registers nothing, like the pig_strip_tool_search build.
func toolSearchEntries() []Extension {
	if pigstrip.Has(pigstrip.ListExtensions, "tool-search") {
		return nil
	}
	return []Extension{{Name: "tool-search", Replaceable: true, Factory: toolsearch.CreateToolSearchExtension()}}
}
