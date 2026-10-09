package cli

// pi: packages/coding-agent/src/cli/config-selector.ts

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports .upstream/v0.99.1/packages/coding-agent/test/package-command-paths.test.ts:529-553 ("toggles built-in extensions in config
// global mode"): the Built-in group lists each built-in extension by name, and toggling writes `-builtin:<name>` then
// `+builtin:<name>` to the user settings.
func TestUpstreamConfigTogglesBuiltinExtensionsInGlobalMode(t *testing.T) {
	_, cwd, agentDir := resourceExtensionFixture(t)
	sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	selector, err := newScopedConfigSelector(cwd, agentDir, sm, sm, false, true, []string{"llama.cpp", "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	rendered := strings.Join(selector.Render(80), "\n")
	if !strings.Contains(rendered, "Built-in") || !strings.Contains(rendered, "llama.cpp") {
		t.Fatalf("selector does not list the built-in extensions:\n%s", rendered)
	}
	selector.HandleInput(" ")
	if got := sm.GetGlobalSettings().Extensions; !slices.Equal(got, []string{"-builtin:llama.cpp"}) {
		t.Fatalf("after the first toggle extensions = %q, want [-builtin:llama.cpp]", got)
	}
	selector.HandleInput(" ")
	if got := sm.GetGlobalSettings().Extensions; !slices.Equal(got, []string{"+builtin:llama.cpp"}) {
		t.Fatalf("after the second toggle extensions = %q, want [+builtin:llama.cpp]", got)
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/test/package-command-paths.test.ts:555-588 ("cycles project built-in extension
// overrides in config local mode"): a project override of a globally disabled built-in cycles inherit, `+builtin:mcp`,
// `-builtin:mcp`, inherit, and never writes the bare path (config-selector.ts:688-690).
func TestUpstreamConfigCyclesProjectBuiltinExtensionOverridesInLocalMode(t *testing.T) {
	_, cwd, agentDir := resourceExtensionFixture(t)
	global := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	if err := global.SetExtensionPaths([]string{"-builtin:mcp"}); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	selector, err := newScopedConfigSelector(cwd, agentDir, global, sm, true, true, []string{"mcp"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]string{{"+builtin:mcp"}, {"-builtin:mcp"}, {}} {
		selector.HandleInput(" ")
		if got := sm.GetProjectSettings().Extensions; !slices.Equal(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Fatalf("project extensions = %q, want %q", got, want)
		}
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/src/core/package-manager.ts:190-198 (resourcePrecedenceRank): built-in extensions
// load after file and package extensions, whichever scope enables them.
func TestResourcePrecedenceRankPlacesBuiltinExtensionsLast(t *testing.T) {
	builtin := func(scope string) tui.ResourceItem {
		return tui.ResourceItem{ResourceType: tui.ResourceExtensions, Source: "builtin", Scope: scope, Origin: "top-level"}
	}
	pkg := tui.ResourceItem{ResourceType: tui.ResourceExtensions, Source: "npm:x", Scope: "project", Origin: "package"}
	for _, scope := range []string{"user", "project"} {
		if got := resourcePrecedenceRank(builtin(scope)); got != 5 {
			t.Errorf("rank(builtin, %s) = %d, want 5", scope, got)
		}
	}
	if resourcePrecedenceRank(builtin("project")) <= resourcePrecedenceRank(pkg) {
		t.Error("a built-in extension does not rank after a package extension")
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/src/modes/interactive/components/config-selector.ts:88-90,138-139: the group of a
// built-in resource is labelled by its scope and its item is named without the `builtin:` prefix.
func TestBuiltinResourceGroupLabelAndDisplayName(t *testing.T) {
	items := []tui.ResourceItem{
		{Path: "builtin:mcp", Enabled: true, ResourceType: tui.ResourceExtensions, Scope: "user", Origin: "top-level", Source: "builtin"},
		{Path: "builtin:llama.cpp", Enabled: true, ResourceType: tui.ResourceExtensions, Scope: "project", Origin: "top-level", Source: "builtin"},
	}
	groups := tui.BuildResourceGroups(items)
	var labels, names []string
	for _, group := range groups {
		labels = append(labels, group.Label)
		for _, subgroup := range group.Subgroups {
			for _, item := range subgroup.Items {
				names = append(names, item.DisplayName)
			}
		}
	}
	if want := []string{"Built-in", "Built-in (project override)"}; !slices.Equal(labels, want) {
		t.Errorf("labels = %q, want %q", labels, want)
	}
	if want := []string{"mcp", "llama.cpp"}; !slices.Equal(names, want) {
		t.Errorf("display names = %q, want %q", names, want)
	}
}
