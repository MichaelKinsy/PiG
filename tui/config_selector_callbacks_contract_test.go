package tui

import (
	"errors"
	"strings"
	"testing"
)

// Pi persists a toggle inside ResourceList (toggleResource); Pig's selector reports it through OnToggle (global scope) and OnOverride (project scope) and
// then applies the result to the item. config-selector.ts:492-501: a toggle changes the item and fires the callback once, only on an item row, in the
// project scope for any item and in the global scope for items whose scope is not project; a project override that cannot be saved changes nothing.
// moveTo presses down or up until the highlighted row is item.
func moveTo(t *testing.T, s *ConfigSelectorComponent, item *ResourceItem) {
	t.Helper()
	for range 2 * len(s.filtered) {
		if s.filtered[s.cursor].item == item {
			return
		}
		s.HandleInput("\x1b[B")
	}
	for range 2 * len(s.filtered) {
		if s.filtered[s.cursor].item == item {
			return
		}
		s.HandleInput("\x1b[A")
	}
	t.Fatalf("no row for %s", item.Path)
}

func TestConfigSelectorToggleCallbacks(t *testing.T) {
	restoreKeybindingsAfterTest(t)
	SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), nil))
	newSelector := func(scope string) (*ConfigSelectorComponent, *ResourceItem, *ResourceItem) {
		user := &ResourceItem{Path: "/agent/extensions/a.ts", Enabled: true, ResourceType: ResourceExtensions, Scope: "user", Origin: "top-level", Source: "local", InheritedEnabled: true, Inherited: true, Override: "inherit"}
		project := &ResourceItem{Path: "/work/.pi/extensions/b.ts", Enabled: true, ResourceType: ResourceExtensions, Scope: "project", Origin: "top-level", Source: "local", InheritedEnabled: true, Override: "inherit"}
		groups := BuildResourceGroups([]ResourceItem{*user, *project})
		for _, g := range groups {
			for _, sg := range g.Subgroups {
				for _, item := range sg.Items {
					switch item.Path {
					case user.Path:
						user = item
					case project.Path:
						project = item
					}
				}
			}
		}
		return NewScopedConfigSelector(groups, groups, 24, scope, true), user, project
	}
	type toggled struct {
		path    string
		enabled bool
	}
	t.Run("global scope calls OnToggle once with the new state", func(t *testing.T) {
		s, user, _ := newSelector("global")
		var calls []toggled
		s.OnToggle = func(item *ResourceItem, enabled bool) { calls = append(calls, toggled{item.Path, enabled}) }
		s.OnOverride = func(*ResourceItem, string) error { t.Fatal("global toggle used OnOverride"); return nil }
		moveTo(t, s, user)
		s.HandleInput(" ")
		s.HandleInput("\r")
		if len(calls) != 2 || calls[0] != (toggled{user.Path, false}) || calls[1] != (toggled{user.Path, true}) || !user.Enabled {
			t.Fatalf("calls = %+v enabled=%v, want disable then enable of %s", calls, user.Enabled, user.Path)
		}
	})
	t.Run("global scope does not toggle a project-scope item", func(t *testing.T) {
		s, _, project := newSelector("global")
		calls := 0
		s.OnToggle = func(*ResourceItem, bool) { calls++ }
		moveTo(t, s, project)
		s.HandleInput(" ")
		s.HandleInput("\r")
		if calls != 0 || !project.Enabled {
			t.Fatalf("calls=%d enabled=%v, want 0 and true", calls, project.Enabled)
		}
	})
	t.Run("project scope toggles any item through OnOverride", func(t *testing.T) {
		s, user, project := newSelector("project")
		var states []string
		s.OnOverride = func(item *ResourceItem, state string) error { states = append(states, item.Path+"="+state); return nil }
		s.OnToggle = func(*ResourceItem, bool) { t.Fatal("project toggle used OnToggle") }
		moveTo(t, s, user)
		s.HandleInput(" ")
		moveTo(t, s, project)
		s.HandleInput("\r")
		if len(states) != 2 || states[0] != user.Path+"=unload" || states[1] != project.Path+"=unload" || user.Override != "unload" || user.Enabled || project.Override != "unload" || project.Enabled {
			t.Fatalf("states=%v user=%s/%v project=%s/%v", states, user.Override, user.Enabled, project.Override, project.Enabled)
		}
	})
	t.Run("a project override that cannot be saved changes nothing", func(t *testing.T) {
		s, user, _ := newSelector("project")
		s.OnOverride = func(*ResourceItem, string) error { return errors.New("read-only settings") }
		moveTo(t, s, user)
		s.HandleInput(" ")
		if user.Override != "inherit" || !user.Enabled {
			t.Fatalf("override=%s enabled=%v after a failed save, want inherit and true", user.Override, user.Enabled)
		}
	})
	t.Run("an empty list ignores the toggle keys", func(t *testing.T) {
		s := NewScopedConfigSelector(nil, nil, 24, "global", true)
		s.OnToggle = func(*ResourceItem, bool) { t.Fatal("empty list toggled") }
		s.HandleInput(" ")
		s.HandleInput("\r")
		s.HandleInput("\x1b[B")
		s.HandleInput("\x1b[A")
		s.HandleInput("\x1b[5~")
		s.HandleInput("\x1b[6~")
	})
}

// config-selector.ts isDimmedItem, getItemSuffix and renderCheckbox read the override state only in the project scope: getProjectOverrideState answers
// "inherit" in the global scope, so an item that carries inheritance or an override is drawn as a plain global item there (a green [x], no suffix, no dimming).
func TestConfigSelectorGlobalScopeIgnoresProjectOverrideState(t *testing.T) {
	restoreKeybindingsAfterTest(t)
	SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), nil))
	previousCaps, previousTheme := GetCapabilities(), ActiveTheme()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetTheme("dark")
	theme := ActiveTheme()
	for _, override := range []string{"inherit", "load", "unload"} {
		item := ResourceItem{Path: "/agent/extensions/a.ts", Enabled: true, ResourceType: ResourceExtensions, Scope: "user", Origin: "top-level", Source: "local", Inherited: true, InheritedEnabled: true, Override: override}
		groups := BuildResourceGroups([]ResourceItem{item})
		s := NewScopedConfigSelector(groups, groups, 24, "global", true)
		var row string
		for _, line := range s.Render(100) {
			if plain := stripANSI(line); strings.Contains(plain, "a.ts") {
				row = line
			}
		}
		if row == "" {
			t.Fatalf("override %s: no item row in %q", override, s.Render(100))
		}
		// A bold name after a green [x]: neither dimmed nor suffixed.
		if want := theme.Success + "[x]\x1b[39m \x1b[1ma.ts\x1b[22m"; !strings.Contains(row, want) || strings.Contains(row, "project") || strings.Contains(row, "inherited global") {
			t.Errorf("override %s: global row = %q, want it to hold %q and no suffix", override, row, want)
		}
	}
}
