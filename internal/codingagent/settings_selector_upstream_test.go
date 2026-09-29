package codingagent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestSettingsSelectorUpstream(t *testing.T) {
	previousKeys, previousTheme := tui.GetTUIKeybindings(), tui.ActiveTheme().Name
	_ = DefaultKeybindingsManager()
	tui.SetThemeByName("dark")
	t.Cleanup(func() { tui.SetTUIKeybindings(previousKeys); tui.SetThemeByName(previousTheme) })

	// packages/coding-agent/test/settings-selector.test.ts:25
	t.Run("cycles through fullscreen settings", func(t *testing.T) {
		for _, tc := range []struct {
			label string
			want  []string
			read  func(*SettingsManager) string
		}{
			{"Fullscreen exit output", []string{"resume-hint", "transcript"}, (*SettingsManager).GetFullscreenExitOutput},
			{"Fullscreen scrollbar", []string{"always", "hidden", "auto"}, (*SettingsManager).GetFullscreenScrollbar},
			{"Fullscreen copy on select", []string{"false", "true"}, func(sm *SettingsManager) string { return fmt.Sprint(sm.GetFullscreenCopyOnSelect()) }},
		} {
			t.Run(tc.label, func(t *testing.T) {
				sm := NewSettingsManager(t.TempDir(), t.TempDir())
				if err := sm.UpdateGlobal(func(s *Settings) {
					s.FullscreenExitOutput = "transcript"
					s.FullscreenScrollbar = "auto"
					s.FullscreenCopyOnSelect = new(true)
				}); err != nil {
					t.Fatal(err)
				}
				var changes []string
				sc := &SlashContext{SettingsManager: sm, Append: func(s string) { t.Fatalf("unexpected output: %s", s) }, OnSettingApplied: func(_ string, value string) {
					changes = append(changes, value)
					if saved := tc.read(sm); saved != value {
						t.Fatalf("saved value=%q, callback=%q", saved, value)
					}
				}}
				sc.ShowSettingsList = func(items []tui.SettingItem, onChange func(string, string) string) {
					list := tui.NewSettingsList(items)
					for _, char := range tc.label {
						list.HandleInput(string(char))
					}
					for range tc.want {
						list.HandleInput("\r")
						if !list.Done() || list.ChangedID == "" {
							t.Fatalf("setting %q did not cycle", tc.label)
						}
						id, value := list.ChangedID, list.ChangedValue
						list.Reset()
						list.UpdateValue(id, onChange(id, value))
					}
				}
				if err := settingsHandlerTUI(sc); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(changes, tc.want) {
					t.Fatalf("changes=%v, want %v", changes, tc.want)
				}
			})
		}
	})
	// packages/coding-agent/test/settings-selector.test.ts:60
	t.Run("keeps the configured fixed theme marked while browsing", func(t *testing.T) {
		frames := captureSettingsThemeBrowsing(t, "dark", "dark", []string{"dark", "light"}, "\x1b[B", "\x1b")
		assertSettingsFrameMarkers(t, frames, 0, "    Automatic", "→ ✓ dark")
		assertSettingsFrameMarkers(t, frames, 1, "  ✓ dark", "→   light")
	})
	// packages/coding-agent/test/settings-selector.test.ts:85
	t.Run("keeps a configured automatic theme marked while browsing", func(t *testing.T) {
		frames := captureSettingsThemeBrowsing(t, "light/dark", "dark", []string{"dark", "light", "other"}, "\r", "\x1b[B", "\x1b", "\x1b")
		assertSettingsFrameMarkers(t, frames, 1, "Automatic Theme", "Choose themes for terminal light and dark appearance.", "Light/dark detection requires terminal support.", "Light Theme", "→ ✓ light")
		assertSettingsFrameMarkers(t, frames, 2, "Automatic Theme", "Light Theme", "  ✓ light", "→   other")
	})
	// packages/coding-agent/test/settings-selector.test.ts:110; the harness model comes from the same faux model factory semantics.
	t.Run("keeps the configured per-model thinking level marked while browsing", func(t *testing.T) {
		faux := ai.NewFauxProvider(ai.FauxConfig{Models: []ai.FauxModelDefinition{{ID: "thinking-model", Reasoning: true}}})
		t.Cleanup(faux.Unregister)
		model := faux.GetModel("thinking-model")
		key := modelSpec(model)
		sm := NewSettingsManager(t.TempDir(), t.TempDir())
		if err := sm.UpdateGlobal(func(s *Settings) {
			s.DefaultProvider, s.DefaultModel = model.ProviderMeta.ProviderID, model.ID
			s.DefaultThinkingLevel = "high"
			s.ModelThinkingLevels = map[string]string{key: "medium"}
		}); err != nil {
			t.Fatal(err)
		}
		sc := &SlashContext{SettingsManager: sm, Append: func(s string) { t.Fatalf("unexpected output: %s", s) }}
		sc.ModelThinkingSubmenu = func(_ string, done func(*string)) tui.Component {
			return newModelThinkingSubmenu(sm.Get(), []*ai.Model{model}, "", func(*ai.Model, string) { t.Fatal("browsing applied a thinking level") }, func() { done(nil) })
		}
		sc.ShowSettingsList = func(items []tui.SettingItem, _ func(string, string) string) {
			list := tui.NewSettingsList(items)
			selectSettingsItem(t, list, items, "model-thinking")
			list.HandleInput("\r")
			list.HandleInput("\r")
			assertSettingsMarkers(t, list, "→ ✓ medium", "    (clear override)")
			list.HandleInput("\x1b[B")
			assertSettingsMarkers(t, list, "  ✓ medium", "→   high")
			list.HandleInput("\x1b")
			list.HandleInput("\x1b")
		}
		if err := settingsHandlerTUI(sc); err != nil {
			t.Fatal(err)
		}
		if got := sm.Get().ModelThinkingLevels[key]; got != "medium" {
			t.Fatalf("browsing changed override to %q", got)
		}
	})
}

// ThemeSubmenu.createThemeSelect restores the parent's pending automatic pair on cancel, not the edited branch's fixed theme or the saved original pair.
func TestAutomaticThemeSubmenuCancelRestoresPendingPair(t *testing.T) {
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
		checkpoint             int
	}{
		{"dark terminal", "dark", "dark", []string{"\r", "\x1b[B", "\x1b", "\x1b[C", "\x1b"}, 4},
		{"pending light edit", "light", "other", []string{"\r", "\x1b[B", "\r", "\x1b[B", "\r", "\x1b[B", "\x1b", "\x1b[C", "\x1b"}, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := captureSettingsThemeBrowsing(t, "light/dark", tc.appearance, []string{"dark", "light", "other"}, tc.keys...)
			for _, frame := range frames {
				if frame.consumed == tc.checkpoint && strings.Contains(frame.text, "Automatic Theme") {
					if frame.theme != tc.want {
						t.Fatalf("parent preview=%q after child cancel, want %q", frame.theme, tc.want)
					}
					return
				}
			}
			t.Fatalf("no parent frame at checkpoint %d: %+v", tc.checkpoint, frames)
		})
	}
}

func BenchmarkAutomaticThemeChildPicker(b *testing.B) {
	menu := newAutomaticThemeMenu("light", "dark")
	menu.submenu = tui.NewSelectSubmenu("Light Theme", "Select the theme to use for light terminal appearance", themeSelectItems([]string{"dark", "light", "other"}, "light"), "light")
	b.ReportAllocs()
	for b.Loop() {
		if rows := menu.Render(120); len(rows) == 0 {
			b.Fatal("empty automatic theme picker")
		}
	}
}

func selectSettingsItem(t *testing.T, list *tui.SettingsList, items []tui.SettingItem, id string) {
	t.Helper()
	for index, item := range items {
		if item.ID == id {
			for range index {
				list.HandleInput("\x1b[B")
			}
			return
		}
	}
	t.Fatalf("settings row %q is absent", id)
}

type settingsSelectorFrame struct {
	consumed int
	text     string
	theme    string
}

type settingsCaptureRenderer struct {
	*tui.TUI
	capture func()
}

func (r *settingsCaptureRenderer) Render()          { r.capture() }
func (r *settingsCaptureRenderer) ForceFullRender() { r.capture() }

func captureSettingsThemeBrowsing(t *testing.T, current, appearance string, names []string, keys ...string) []settingsSelectorFrame {
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
	sm := NewSettingsManager(t.TempDir(), t.TempDir())
	if err := sm.UpdateGlobal(func(s *Settings) { s.Theme = current }); err != nil {
		t.Fatal(err)
	}
	input := make(chan []byte, len(keys))
	for _, key := range keys {
		input <- []byte(key)
	}
	close(input)
	m := &InteractiveMode{opts: InteractiveOptions{SettingsManager: sm, Settings: sm.Get()}, editor: tui.NewEditor(), editorContainer: tui.NewContainer(), modalInputCh: input}
	m.themeState.terminalTheme = tui.TerminalTheme(appearance)
	var frames []settingsSelectorFrame
	m.tuiInst = &settingsCaptureRenderer{TUI: tui.NewWithOutput(io.Discard, 120, 30), capture: func() {
		frames = append(frames, settingsSelectorFrame{consumed: len(keys) - len(input), text: stripANSITest(strings.Join(m.editorContainer.Render(120), "\n")), theme: tui.ActiveTheme().Name})
	}}
	t.Cleanup(m.tuiInst.Stop)
	sc := m.buildSlashContext(t.Context())
	sc.OnSettingApplied = func(string, string) { t.Fatal("browsing saved a theme") }
	sc.ShowSettingsList = func(items []tui.SettingItem, onChange func(string, string) string) {
		list := tui.NewSettingsList(items)
		selectSettingsItem(t, list, items, "theme")
		list.HandleInput("\r")
		if !list.Done() {
			t.Fatal("theme row did not open")
		}
		if saved := onChange(list.ChangedID, list.ChangedValue); saved != current {
			t.Fatalf("saved theme=%q, want %q", saved, current)
		}
	}
	if err := settingsHandlerTUI(sc); err != nil {
		t.Fatal(err)
	}
	return frames
}

func assertSettingsFrameMarkers(t *testing.T, frames []settingsSelectorFrame, consumed int, markers ...string) {
	t.Helper()
	for _, frame := range frames {
		if frame.consumed != consumed || slices.ContainsFunc(markers, func(marker string) bool { return !strings.Contains(frame.text, marker) }) {
			continue
		}
		return
	}
	t.Fatalf("no frame after %d keys contains %q: %+v", consumed, markers, frames)
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
