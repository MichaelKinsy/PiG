package tui

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
)

// treeAppKeybindingDefs are the app.tree.* rows of upstream KEYBINDINGS
// (coding-agent core/keybindings.ts). Production installs them from the
// coding agent's table; TestTreeAppKeybindingFixtureMatchesInventory pins
// this fixture to the pinned Pi inventory.
func treeAppKeybindingDefs(platform KeybindingPlatform) map[string]TUIKeybindingDef {
	return map[string]TUIKeybindingDef{
		"app.tree.foldOrUp": {DefaultKeys: PlatformKeys{
			Other: []string{"ctrl+left", "alt+left"}, Darwin: []string{"alt+left", "ctrl+left"},
		}.For(platform), Description: "Fold tree branch or move up"},
		"app.tree.unfoldOrDown": {DefaultKeys: PlatformKeys{
			Other: []string{"ctrl+right", "alt+right"}, Darwin: []string{"alt+right", "ctrl+right"},
		}.For(platform), Description: "Unfold tree branch or move down"},
		"app.tree.editLabel":            {DefaultKeys: []string{"shift+l"}, Description: "Edit tree label"},
		"app.tree.toggleLabelTimestamp": {DefaultKeys: []string{"shift+t"}, Description: "Toggle tree label timestamps"},
		"app.tree.filter.default":       {DefaultKeys: []string{"ctrl+d"}, Description: "Tree filter: default view"},
		"app.tree.filter.noTools":       {DefaultKeys: []string{"ctrl+t"}, Description: "Tree filter: hide tool results"},
		"app.tree.filter.userOnly":      {DefaultKeys: []string{"ctrl+u"}, Description: "Tree filter: user messages only"},
		"app.tree.filter.labeledOnly":   {DefaultKeys: []string{"ctrl+l"}, Description: "Tree filter: labeled entries only"},
		"app.tree.filter.all":           {DefaultKeys: []string{"ctrl+a"}, Description: "Tree filter: show all entries"},
		"app.tree.filter.cycleForward":  {DefaultKeys: []string{"ctrl+o"}, Description: "Tree filter: cycle forward"},
		"app.tree.filter.cycleBackward": {DefaultKeys: []string{"shift+ctrl+o"}, Description: "Tree filter: cycle backward"},
	}
}

// useTreeKeybindings installs the TUI table plus the app.tree.* rows with the
// given user overrides, as the coding agent does before /tree opens, and
// restores the previous registry when the test ends.
func useTreeKeybindings(t *testing.T, userBindings map[string][]string) {
	t.Helper()
	previous := GetTUIKeybindings()
	t.Cleanup(func() { SetTUIKeybindings(previous) })
	defs := TUIKeybindingDefinitionsFor(HostKeybindingPlatform())
	maps.Copy(defs, treeAppKeybindingDefs(HostKeybindingPlatform()))
	SetTUIKeybindings(NewKeybindingsManager(defs, userBindings))
}

// Tree labels follow app.tree.editLabel and app.tree.toggleLabelTimestamp
// rather than matching L and T literally.
func TestTreeSelectLabelKeysFollowRebinding(t *testing.T) {
	useTreeKeybindings(t, map[string][]string{
		"app.tree.editLabel":            {"ctrl+e"},
		"app.tree.toggleLabelTimestamp": {"alt+t"},
	})
	root := &fakeNode{id: "r", kids: []TreeNode{&fakeNode{id: "x", label: "entry"}}}
	ts := NewTreeSelect("", root)
	ts.OnLabelEdit = func(string, string) {}

	ts.HandleInput("T")
	if ts.showLabelTimestamps {
		t.Fatal("T toggled timestamps after app.tree.toggleLabelTimestamp moved to alt+t")
	}
	if ts.searchQuery != "T" {
		t.Fatalf("an unbound T must type into the search, got query %q", ts.searchQuery)
	}
	ts.HandleInput("\x1b") // Esc clears the search
	ts.HandleInput("\x1bt")
	if !ts.showLabelTimestamps {
		t.Fatal("alt+t did not toggle label timestamps")
	}

	ts.HandleInput("L")
	if ts.editingLabel {
		t.Fatal("L entered label editing after app.tree.editLabel moved to ctrl+e")
	}
	ts.HandleInput("\x1b")
	ts.HandleInput("\x05")
	if !ts.editingLabel {
		t.Fatal("ctrl+e did not enter label editing")
	}
}

func TestTreeSelectFoldAndFilterKeysFollowRebinding(t *testing.T) {
	useTreeKeybindings(t, map[string][]string{
		"app.tree.foldOrUp":       {"alt+h"},
		"app.tree.filter.noTools": {"alt+n"},
	})
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "a", label: "A", kids: []TreeNode{
			&fakeNode{id: "a1", label: "A1"},
			&fakeNode{id: "a2", label: "A2"},
		}},
	}}
	ts := NewTreeSelect("", root)
	ts.cursor = 0
	ts.HandleInput("\x1b[1;5D") // the default ctrl+left no longer folds
	if ts.foldedNodes["a"] {
		t.Fatal("ctrl+left folded after app.tree.foldOrUp moved to alt+h")
	}
	ts.HandleInput("\x1bh")
	if !ts.foldedNodes["a"] {
		t.Fatal("alt+h did not fold the branch")
	}
	ts.HandleInput("\x14") // ctrl+t no longer selects no-tools
	if ts.filterMode == "no-tools" {
		t.Fatal("ctrl+t selected no-tools after app.tree.filter.noTools moved to alt+n")
	}
	ts.HandleInput("\x1bn")
	if ts.filterMode != "no-tools" {
		t.Fatalf("alt+n: filterMode = %q, want no-tools", ts.filterMode)
	}
}

func TestTreeAppKeybindingFixtureMatchesInventory(t *testing.T) {
	_, data := currentBehaviorInputs(t)
	var inventory upstreamKeybindingInventory
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, platform := range KeybindingPlatforms {
		fixture := treeAppKeybindingDefs(platform)
		seen := 0
		for _, binding := range inventory.Keybindings {
			if !strings.HasPrefix(binding.ID, "app.tree.") {
				continue
			}
			seen++
			got, ok := fixture[binding.ID]
			if !ok || !slices.Equal(got.DefaultKeys, binding.Defaults[string(platform)]) || got.Description != binding.Description {
				t.Errorf("%s %s fixture = %#v, inventory = %v %q", platform, binding.ID, got, binding.Defaults[string(platform)], binding.Description)
			}
		}
		if seen != len(fixture) {
			t.Errorf("%s: fixture has %d app.tree.* rows, inventory %d", platform, len(fixture), seen)
		}
	}
}
