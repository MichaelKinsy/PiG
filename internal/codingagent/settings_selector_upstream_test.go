package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// initSettingsSelectorTheme is the test file's beforeAll: initTheme("dark") and setKeybindings(new KeybindingsManager()).
func initSettingsSelectorTheme(t *testing.T) {
	t.Helper()
	previousKeys, previousTheme := tui.GetTUIKeybindings(), tui.ActiveTheme().Name
	_ = DefaultKeybindingsManager()
	tui.SetThemeByName("dark")
	t.Cleanup(func() { tui.SetTUIKeybindings(previousKeys); tui.SetThemeByName(previousTheme) })
}

// packages/ai/src/providers/faux.ts:141-154,668 (FauxProviderHandle.appendResponses, unregister).
// Pi: packages/coding-agent/src/modes/interactive/components/settings-selector.ts:115 (SettingsCallbacks.onModelThinkingLevelChange); packages/coding-agent/src/modes/interactive/components/settings-selector.ts:116 (SettingsCallbacks.onModelThinkingLevelRemove).
func TestSettingsSelectorUpstream(t *testing.T) {
	initSettingsSelectorTheme(t)

	// packages/coding-agent/test/settings-selector.test.ts:24
	t.Run("cycles through fullscreen settings", func(t *testing.T) {
		var exitOutput, scrollbar, copyOnSelect, wheelScrollLines []string
		config := SettingsConfig{
			FullscreenExitOutput:       "transcript",
			FullscreenScrollbar:        "auto",
			FullscreenCopyOnSelect:     true,
			FullscreenWheelScrollLines: WheelScrollLines{Lines: 7},
			DefaultModel:               "not set",
		}
		callbacks := SettingsCallbacks{
			OnFullscreenExitOutputChange:   func(output FullscreenExitOutput) { exitOutput = append(exitOutput, string(output)) },
			OnFullscreenScrollbarChange:    func(mode string) { scrollbar = append(scrollbar, mode) },
			OnFullscreenCopyOnSelectChange: func(enabled bool) { copyOnSelect = append(copyOnSelect, fmt.Sprint(enabled)) },
			OnFullscreenWheelScrollLinesChange: func(lines WheelScrollLines) {
				wheelScrollLines = append(wheelScrollLines, wheelScrollLinesLabel(lines))
			},
		}
		cycle := func(label string, count int) {
			list := NewSettingsSelectorComponent(config, callbacks).GetSettingsList()
			for _, char := range label {
				list.HandleInput(string(char))
			}
			for range count {
				list.HandleInput("\r")
			}
		}
		cycle("Fullscreen exit output", 2)
		if want := []string{"resume-hint", "transcript"}; !slices.Equal(exitOutput, want) {
			t.Fatalf("exit output changes = %v, want %v", exitOutput, want)
		}
		cycle("Fullscreen scrollbar", 3)
		if want := []string{"always", "hidden", "auto"}; !slices.Equal(scrollbar, want) {
			t.Fatalf("scrollbar changes = %v, want %v", scrollbar, want)
		}
		cycle("Fullscreen copy on select", 2)
		if want := []string{"false", "true"}; !slices.Equal(copyOnSelect, want) {
			t.Fatalf("copy on select changes = %v, want %v", copyOnSelect, want)
		}
		// #9758: custom values from settings.json stay in the cycle.
		cycle("Fullscreen wheel scrolling", 3)
		if want := []string{"10", "auto", "1"}; !slices.Equal(wheelScrollLines, want) {
			t.Fatalf("wheel scroll lines changes = %v, want %v", wheelScrollLines, want)
		}
	})
	// packages/coding-agent/test/settings-selector.test.ts:65: the system theme comes first, then automatic.
	t.Run("keeps the configured fixed theme marked while browsing", func(t *testing.T) {
		config := SettingsConfig{DefaultModel: "not set", CurrentTheme: "dark", TerminalTheme: "dark", AvailableThemes: []string{"system", "dark", "light"}}
		list := NewSettingsSelectorComponent(config, SettingsCallbacks{OnThemePreview: func(string) {}, OnCancel: func() {}}).GetSettingsList()
		list.SelectItem("theme")
		list.HandleInput("\r")
		output := stripANSITest(strings.Join(list.Render(120), "\n"))
		if !regexp.MustCompile(` {4}system +Theme created from your terminal's colors\n {4}automatic +Use separate themes`).MatchString(output) {
			t.Fatalf("the list does not put the system theme before automatic:\n%s", output)
		}
		if !strings.Contains(output, "→ ✓ dark") {
			t.Fatalf("the fixed theme is not marked:\n%s", output)
		}
		list.HandleInput("\x1b[B")
		output = stripANSITest(strings.Join(list.Render(120), "\n"))
		for _, marker := range []string{"  ✓ dark", "→   light"} {
			if !strings.Contains(output, marker) {
				t.Fatalf("missing %q after browsing:\n%s", marker, output)
			}
		}
	})
	// packages/coding-agent/test/settings-selector.test.ts:85
	t.Run("keeps a configured automatic theme marked while browsing", func(t *testing.T) {
		config := SettingsConfig{DefaultModel: "not set", CurrentTheme: "light/dark", TerminalTheme: "dark", AvailableThemes: []string{"dark", "light", "other"}}
		list := NewSettingsSelectorComponent(config, SettingsCallbacks{OnThemePreview: func(string) {}, OnCancel: func() {}}).GetSettingsList()
		list.SelectItem("theme")
		list.HandleInput("\r")
		list.HandleInput("\r")
		output := stripANSITest(strings.Join(list.Render(120), "\n"))
		for _, marker := range []string{"Automatic Theme", "Choose themes for terminal light and dark appearance.", "Light/dark detection requires terminal support.", "Light Theme", "→ ✓ light"} {
			if !strings.Contains(output, marker) {
				t.Fatalf("missing %q:\n%s", marker, output)
			}
		}
		list.HandleInput("\x1b[B")
		output = stripANSITest(strings.Join(list.Render(120), "\n"))
		for _, marker := range []string{"  ✓ light", "→   other"} {
			if !strings.Contains(output, marker) {
				t.Fatalf("missing %q after browsing:\n%s", marker, output)
			}
		}
	})
	// packages/coding-agent/test/settings-selector.test.ts:110; the harness model comes from the same faux model factory semantics.
	t.Run("keeps the configured per-model thinking level marked while browsing", func(t *testing.T) {
		faux := ai.NewFauxProvider(ai.FauxConfig{Models: []ai.FauxModelDefinition{{ID: "thinking-model", Reasoning: true}}})
		t.Cleanup(faux.Unregister)
		model := faux.GetModel("thinking-model")
		key := modelSpec(model)
		config := SettingsConfig{
			DefaultModel:           key,
			AvailableDefaultModels: []*ai.Model{model},
			ThinkingLevel:          "high",
			ModelThinkingLevels:    map[string]string{key: "medium"},
		}
		callbacks := SettingsCallbacks{
			OnCancel:                   func() {},
			OnModelThinkingLevelChange: func(string, string, string) { t.Fatal("browsing applied a thinking level") },
			OnModelThinkingLevelRemove: func(string, string) { t.Fatal("browsing cleared an override") },
		}
		list := NewSettingsSelectorComponent(config, callbacks).GetSettingsList()
		list.SelectItem("model-thinking")
		list.HandleInput("\r")
		list.HandleInput("\r")
		assertSettingsMarkers(t, list, "→ ✓ medium", "    (clear override)")
		list.HandleInput("\x1b[B")
		assertSettingsMarkers(t, list, "  ✓ medium", "→   high")
	})
}

// ThemeSubmenu.createThemeSelect restores the parent's pending automatic pair on cancel, not the edited branch's fixed theme or the saved original pair.
// Pi: packages/coding-agent/src/modes/interactive/components/settings-selector.ts:117 (SettingsCallbacks.onThemeChange).
func TestAutomaticThemeSubmenuCancelRestoresPendingPair(t *testing.T) {
	restoreStartupTheme(t)
	previous := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previous) })
	for _, trueColor := range []bool{true, false} {
		t.Run(fmt.Sprintf("truecolor=%t", trueColor), func(t *testing.T) {
			capabilities := previous
			capabilities.TrueColor = trueColor
			tui.SetCapabilities(capabilities)
			assertAutomaticThemeSubmenuCancelRestoresPendingPair(t)
		})
	}
}

func assertAutomaticThemeSubmenuCancelRestoresPendingPair(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		name, appearance, want string
		keys                   []string
	}{
		{"dark terminal", "dark", "dark", []string{"\r", "\x1b[B", "\x1b"}},
		{"pending light edit", "light", "other", []string{"\r", "\x1b[B", "\r", "\x1b[B", "\r", "\x1b[B", "\x1b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var previews []string
			var active []string
			list := newThemeBrowsingList(t, "light/dark", tc.appearance, []string{"dark", "light", "other"}, func(setting string) {
				previews = append(previews, setting)
				name, _ := tui.ResolveThemeSettingPresence(&setting, tui.GetTerminalTheme())
				tui.SetThemeByName(name)
				active = append(active, tui.ActiveTheme().Name)
			})
			list.SelectItem("theme")
			list.HandleInput("\r")
			for _, key := range tc.keys {
				list.HandleInput(key)
			}
			if len(previews) == 0 || len(active) == 0 {
				t.Fatalf("browsing previewed nothing: %v", previews)
			}
			if got := active[len(active)-1]; got != tc.want {
				t.Fatalf("parent preview=%q (settings %v) after child cancel, want %q", got, previews, tc.want)
			}
			if !strings.Contains(stripANSITest(strings.Join(list.Render(120), "\n")), "Automatic Theme") {
				t.Fatal("the automatic menu is not back after the child cancel")
			}
		})
	}
}

func BenchmarkAutomaticThemeChildPicker(b *testing.B) {
	names := []string{"dark", "light", "other"}
	menu := newThemeSubmenu("light/dark", "dark", names, SettingsCallbacks{}, func(*string) {})
	menu.HandleInput("\r")
	b.ReportAllocs()
	for b.Loop() {
		if rows := menu.Render(120); len(rows) == 0 {
			b.Fatal("empty automatic theme picker")
		}
	}
}

// newThemeBrowsingList returns the selector's list for a registry of names with the given terminal appearance; onPreview receives each previewed theme setting and no change may reach OnThemeChange.
func newThemeBrowsingList(t *testing.T, current, appearance string, names []string, onPreview func(string)) *tui.SettingsList {
	t.Helper()
	oldRegistry, oldTheme := tui.ActiveThemeRegistry(), tui.ActiveTheme().Name
	t.Cleanup(func() { tui.SetThemeRegistry(oldRegistry); tui.SetThemeByName(oldTheme) })
	registry := tui.NewThemeRegistry()
	for _, name := range names {
		if registry.Get(name) == nil {
			// A theme's retained JSON must have the same identity when terminal color conversion rebuilds it.
			data, err := os.ReadFile(filepath.Join("..", "..", "tui", "theme_dark.json"))
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			document["name"] = name
			data, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "theme.json")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			theme, err := tui.LoadThemeFile(path)
			if err != nil {
				t.Fatal(err)
			}
			registry.Add(theme)
		}
	}
	tui.SetThemeRegistry(registry)
	tui.SetThemeByName("dark")
	tui.SetTerminalColorScheme(tui.TerminalTheme(appearance))
	t.Cleanup(func() { tui.SetTerminalColorScheme("") })
	config := SettingsConfig{DefaultModel: "not set", CurrentTheme: current, TerminalTheme: tui.GetTerminalTheme(), AvailableThemes: registry.Names()}
	callbacks := SettingsCallbacks{
		OnThemePreview: onPreview,
		OnThemeChange:  func(setting string) { t.Fatalf("browsing saved theme %q", setting) },
		OnCancel:       func() {},
	}
	return NewSettingsSelectorComponent(config, callbacks).GetSettingsList()
}

func assertSettingsMarkers(t *testing.T, component tui.Component, markers ...string) {
	t.Helper()
	text := stripANSITest(strings.Join(component.Render(120), "\n"))
	for _, marker := range markers {
		if !strings.Contains(text, marker) {
			t.Fatalf("missing %q: %q", marker, text)
		}
	}
}
