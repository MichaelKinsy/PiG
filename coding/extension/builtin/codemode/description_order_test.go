package codemode_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
)

// The catalog rules upstream states in tool.ts (selectCatalog, createCodemodeDescription) that the upstream tests
// assert only partly: cheapest tool first inside a group, groups without a namespace first, then namespaces by name.

func TestCodemodeDescriptionPlacesTheCheapestToolOfAGroupFirst(t *testing.T) {
	long := agentTool("z_long", strings.Repeat("Long ", 200))
	short := agentTool("a_short", "Short.")
	// The long tool is registered first; a budget that fits only one section must pick the short one.
	budget := 60
	description := codemode.CreateDescription([]extension.AgentTool{long, short}, codemode.DescriptionOptions{InlineBudget: &budget})
	mustContain(t, description, "Nested tools:", "### `a_short`")
	mustNotContain(t, description, "### `z_long`")
}

func TestCodemodeDescriptionOrdersGroupsWithoutANamespaceThenNamespacesByName(t *testing.T) {
	zeta, alpha, plain := agentTool("zeta_tool", "Z."), agentTool("alpha_tool", "A."), agentTool("plain_tool", "P.")
	description := codemode.CreateDescription([]extension.AgentTool{zeta, alpha, plain}, codemode.DescriptionOptions{Namespaces: map[string]extension.ToolNamespace{
		"zeta_tool": {Name: "zeta"}, "alpha_tool": {Name: "Alpha"},
	}})
	plainAt, alphaAt, zetaAt := strings.Index(description, "### `plain_tool`"), strings.Index(description, "## Alpha"), strings.Index(description, "## zeta")
	if plainAt < 0 || plainAt >= alphaAt || alphaAt >= zetaAt {
		t.Fatalf("order: plain %d, Alpha %d, zeta %d in\n%s", plainAt, alphaAt, zetaAt, description)
	}
}
