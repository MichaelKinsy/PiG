package tui

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigSelectorScopeSwitchReappliesSearch(t *testing.T) {
	global := []ResourceItem{
		{Path: "/global/prompts/alpha.md", ResourceType: ResourcePrompts, DisplayName: "alpha.md", Enabled: true, Scope: "user", Origin: "top-level", Source: "auto"},
		{Path: "/global/prompts/beta.md", ResourceType: ResourcePrompts, DisplayName: "beta.md", Enabled: true, Scope: "user", Origin: "top-level", Source: "auto"},
	}
	project := []ResourceItem{
		{Path: "/project/prompts/alpha-local.md", ResourceType: ResourcePrompts, DisplayName: "alpha-local.md", Enabled: true, Scope: "project", Origin: "top-level", Source: "auto"},
		{Path: "/project/prompts/gamma.md", ResourceType: ResourcePrompts, DisplayName: "gamma.md", Enabled: true, Scope: "project", Origin: "top-level", Source: "auto"},
	}
	cs := NewScopedConfigSelector(BuildResourceGroups(global), BuildResourceGroups(project), 0, "global", true)
	cs.HandleInput("alpha")
	assertFiltered := func(want, unwanted string) {
		t.Helper()
		rendered := strings.Join(cs.Render(100), "\n")
		if !strings.Contains(rendered, "> ") || !strings.Contains(rendered, want) {
			t.Fatalf("scope filter missing first match %q:\n%s", want, rendered)
		}
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("scope filter retained nonmatch %q:\n%s", unwanted, rendered)
		}
	}
	assertFiltered("alpha.md", "beta.md")
	cs.HandleInput("\t")
	assertFiltered("alpha-local.md", "gamma.md")
	cs.HandleInput("\t")
	assertFiltered("alpha.md", "beta.md")
}

func TestConfigSelectorProjectStatesMatchUpstreamRendering(t *testing.T) {
	items := []ResourceItem{
		{Path: "/global/prompts/inherited.md", ResourceType: ResourcePrompts, DisplayName: "inherited.md", Enabled: true, Scope: "user", Origin: "top-level", Source: "auto", Override: "inherit", Inherited: true, InheritedEnabled: true},
		{Path: "/global/prompts/load.md", ResourceType: ResourcePrompts, DisplayName: "load.md", Enabled: true, Scope: "user", Origin: "top-level", Source: "auto", Override: "load", Inherited: true, InheritedEnabled: false},
		{Path: "/global/prompts/unload.md", ResourceType: ResourcePrompts, DisplayName: "unload.md", Enabled: false, Scope: "user", Origin: "top-level", Source: "auto", Override: "unload", Inherited: true, InheritedEnabled: true},
	}
	groups := BuildResourceGroups(items)
	cs := NewScopedConfigSelector(groups, groups, 0, "project", true)
	rendered := strings.Join(cs.Render(120), "\n")
	theme := ActiveTheme()
	reset := "\x1b[0m"
	for _, want := range []string{
		theme.Dim + "[x]" + reset + " " + theme.Dim + "inherited.md" + reset + theme.Dim + "  inherited global" + reset,
		theme.Success + "[+]" + reset + " load.md" + theme.Muted + "  project load" + reset,
		theme.Warning + "[-]" + reset + " unload.md" + theme.Muted + "  project unload" + reset,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("project-state render missing %q:\n%s", want, rendered)
		}
	}
}

func TestConfigSelectorTypedFilterNarrowsToMatchingResource(t *testing.T) {
	resources := []ResourceItem{
		{Path: "/agent/skills/sample-skill/SKILL.md", ResourceType: ResourceSkills, DisplayName: "sample-skill", Enabled: true, Scope: "user", Origin: "top-level", Source: "auto"},
		{Path: "/agent/prompts/alpha.md", ResourceType: ResourcePrompts, DisplayName: "alpha.md", Enabled: true, Scope: "user", Origin: "top-level", Source: "auto"},
		{Path: "/agent/prompts/beta.md", ResourceType: ResourcePrompts, DisplayName: "beta.md", Enabled: true, Scope: "user", Origin: "top-level", Source: "auto"},
		{Path: "/agent/themes/zinc.json", ResourceType: ResourceThemes, DisplayName: "zinc.json", Enabled: true, Scope: "user", Origin: "top-level", Source: "auto"},
	}
	cs := NewConfigSelector(BuildResourceGroups(resources), 0)

	cs.HandleInput("alpha")

	joined := strings.Join(cs.Render(100), "\n")
	for _, want := range []string{"> alpha", "Prompts", "alpha.md"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("filtered render missing %q:\n%s", want, joined)
		}
	}
	for _, unwanted := range []string{"Skills", "Themes", "sample-skill", "beta.md", "zinc.json"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("filtered render unexpectedly contains %q:\n%s", unwanted, joined)
		}
	}
}

func TestConfigSelectorScrollIndicatorCountsOnlyItems(t *testing.T) {
	resources := []ResourceItem{
		{Path: "/pkg/ext-a.ts", ResourceType: ResourceExtensions, DisplayName: "ext-a", Scope: "user", Origin: "package", Source: "pkg-a"},
		{Path: "/pkg/ext-b.ts", ResourceType: ResourceExtensions, DisplayName: "ext-b", Scope: "user", Origin: "package", Source: "pkg-a"},
		{Path: "/pkg/skill/SKILL.md", ResourceType: ResourceSkills, DisplayName: "skill-a", Scope: "user", Origin: "package", Source: "pkg-a"},
	}
	cs := NewConfigSelector(BuildResourceGroups(resources), 0)
	cs.maxVisible = 2
	cs.filtered = append([]flatEntry{}, cs.flatItems...)

	// Move selection to the second actual item. filtered includes headers, so this
	// would be larger than 2 if headers were incorrectly counted.
	itemSeen := 0
	for i, e := range cs.filtered {
		if e.entryType != "item" {
			continue
		}
		itemSeen++
		if itemSeen == 2 {
			cs.cursor = i
			break
		}
	}

	lines := cs.Render(80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "(2/3)") {
		t.Fatalf("expected item-only scroll indicator '(2/3)', got:\n%s", joined)
	}
}

func TestConfigSelectorMaxVisibleTracksTerminalRows(t *testing.T) {
	resources := []ResourceItem{{Path: "/pkg/ext-a.ts", ResourceType: ResourceExtensions, DisplayName: "ext-a", Scope: "user", Origin: "package", Source: "pkg-a"}}
	cs := NewConfigSelector(BuildResourceGroups(resources), 12)
	if got := cs.maxVisible; got != 5 {
		t.Fatalf("maxVisible = %d, want 5", got)
	}
	cs.SetTerminalRows(30)
	if got := cs.maxVisible; got != 22 {
		t.Fatalf("maxVisible after resize = %d, want 22", got)
	}
	fallback := NewConfigSelector(BuildResourceGroups(resources), 0)
	if got := fallback.maxVisible; got != 16 {
		t.Fatalf("fallback maxVisible = %d, want 16", got)
	}
}

func TestBuildResourceGroups_GroupByBaseDirAndFormatAutoLabels(t *testing.T) {
	oldHome := t.TempDir()
	// The home directory is HOME on Unix and USERPROFILE on Windows, as for
	// upstream's os.homedir().
	t.Setenv("HOME", oldHome)
	t.Setenv("USERPROFILE", oldHome)

	resources := []ResourceItem{
		{Path: filepath.Join(oldHome, ".pig", "agent", "prompts", "a.md"), ResourceType: ResourcePrompts, Scope: "user", Origin: "top-level", Source: "auto", BaseDir: filepath.Join(oldHome, ".pig", "agent")},
		{Path: filepath.Join(oldHome, ".config", "pig", "prompts", "b.md"), ResourceType: ResourcePrompts, Scope: "user", Origin: "top-level", Source: "auto", BaseDir: filepath.Join(oldHome, ".config", "pig")},
		{Path: filepath.Join(oldHome, "work", ".pig", "themes", "c.json"), ResourceType: ResourceThemes, Scope: "project", Origin: "top-level", Source: "auto", BaseDir: filepath.Join(oldHome, "work", ".pig")},
	}

	groups := BuildResourceGroups(resources)
	if len(groups) != 3 {
		t.Fatalf("len(groups) = %d, want 3", len(groups))
	}

	labels := []string{groups[0].Label, groups[1].Label, groups[2].Label}
	joined := strings.Join(labels, "\n")
	for _, want := range []string{
		"User (~/.config/pig/)",
		"User (~/.pig/agent/)",
		"Project (~/work/.pig/)",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("labels missing %q:\n%s", want, joined)
		}
	}

	seen := map[string]bool{}
	for _, g := range groups {
		if seen[g.Key] {
			t.Fatalf("duplicate group key %q", g.Key)
		}
		seen[g.Key] = true
	}
}
