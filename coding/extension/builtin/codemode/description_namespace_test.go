package codemode_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
)

// tool.ts DESCRIPTION_INTRO and DEFERRED_TOOLS_GUIDANCE of 0.99.2, which the catalog cases of tool-search.test.ts only
// match in part: the helper list documents describeNamespace(), and the guidance for tools that are not listed is
// always part of the description.
func TestCodemodeDescriptionDocumentsDescribeNamespaceAndAlwaysCarriesTheSearchGuidance(t *testing.T) {
	description := codemode.CreateDescription(nil, codemode.DescriptionOptions{})
	for _, want := range []string{
		"- `describeTool(name: string)`: resolves to the description and declaration of a nested tool, or `undefined`.\n" +
			"- `describeNamespace(name: string)`: resolves to `{ name, description?, instructions?, tools }` for a namespace of nested tools, such as an MCP server: its usage instructions and the names of its tools, or `undefined`.\n" +
			"- `console.log(...)`",
		"Some nested tools may be omitted from this description, such as deferred tools and MCP tools. They are still available on the global `tools` object and listed in `ALL_TOOLS`.\n" +
			"To find one, call `await searchTools(query)` (pass `{ namespace }` to search one namespace), or filter `ALL_TOOLS` by `name` and `description`. `await describeNamespace(name)` returns a namespace's usage instructions and the names of its tools.",
	} {
		if !strings.Contains(description, want) {
			t.Errorf("description lacks %q", want)
		}
	}
}
