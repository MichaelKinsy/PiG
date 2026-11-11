package tui_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/quiet-tools/tui"
	maintui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	chordhost "github.com/MichaelKinsy/PiG/chord/next/host"
)

// grantLog records the runtime's requests for launcher actions (Pi's `runtime.onGrant` of the settings tests).
type grantLog struct {
	mu    sync.Mutex
	items []string
}

func (g *grantLog) add(action string, payload any) {
	encoded, _ := json.Marshal([]any{action, payload})
	g.mu.Lock()
	g.items = append(g.items, string(encoded))
	g.mu.Unlock()
}

func (g *grantLog) list() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.items...)
}

func (g *grantLog) count() int { return len(g.list()) }

// settingsRuntime is the runtime with the core facet, a viewport of 100x40 and a recording `onGrant` (Pi's `runtimeWith` of tui-settings.test.ts).
func settingsRuntime(t *testing.T, facets ...chordhost.Facet) (*tuitest.Fixture, *grantLog) {
	t.Helper()
	f := tuitest.New(t, tuitest.Options{Facets: append([]chordhost.Facet{maintui.CoreFacet}, facets...), Columns: 100, Rows: 40})
	grants := &grantLog{}
	f.Do(func() {
		f.Runtime.OnGrant = func(action string, payload any) runtime.GrantResult {
			grants.add(action, payload)
			return runtime.GrantResult{OK: true}
		}
	})
	f.Poll(func() bool {
		for _, item := range f.Runtime.Commands.List(nil) {
			if item.Slash == "settings" {
				return true
			}
		}
		return false
	})
	return f, grants
}

func screenText(f *tuitest.Fixture) string {
	f.TUI.RenderNow()
	return plain(f.Terminal.Viewport())
}

func waitFor(f *tuitest.Fixture, text string) {
	f.T.Helper()
	f.Poll(func() bool { return strings.Contains(screenText(f), text) })
}

func localSettings(settings prefs.TuiSettings) *prefs.TuiLocalPreferences {
	return &prefs.TuiLocalPreferences{Keybindings: map[string][]string{}, Settings: settings, Themes: nil}
}

func TestSettingsCollectsChangesAndProposesOnePatchTheThemePreviewNeverTouchesTheLiveTheme(t *testing.T) {
	f, grants := settingsRuntime(t)
	var live any
	f.Do(func() {
		local := prefs.EmptyPreferences()
		local.Theme = "dark"
		f.Runtime.Appearance.Update(runtime.AppearanceUpdate{Local: &local})
		live = f.Runtime.Theme().Current()
		f.Runtime.Ctx.Commands().RunSlash("/settings")
	})
	waitFor(f, "Hide thinking")
	// Theme: the picker previews "light" in its own pane; the runtime's theme stays.
	f.Send("\r")
	waitFor(f, "Preview")
	if !strings.Contains(f.Screen(), "→ ✓ dark") {
		t.Fatalf("screen:\n%s", f.Screen())
	}
	f.Send("\x1b[B")
	f.Poll(func() bool { return regexp.MustCompile(`→ +light`).MatchString(screenText(f)) })
	f.Do(func() {
		if any(f.Runtime.Theme().Current()) != live {
			t.Error("the preview is isolated")
		}
	})
	f.Send("\r")
	waitFor(f, "Hide thinking")
	f.Do(func() {
		if any(f.Runtime.Theme().Current()) != live {
			t.Error("the live theme changed")
		}
	})
	// Hide thinking: true (p2's default) → false.
	f.Send("\x1b[B")
	f.Send("\r")
	if grants.count() != 0 {
		t.Fatalf("nothing is proposed while the list is open: %v", grants.list())
	}
	f.Send("\x1b")
	f.Poll(func() bool { return grants.count() == 1 })
	if got := grants.list()[0]; got != `["savePrefs",{"patch":{"settings":{"hideThinkingBlock":false},"theme":"light"}}]` {
		t.Fatalf("grant = %s", got)
	}
}

// Ports apps/p2/test/p2/tui-settings.test.ts "/settings filters its rows as you type, like pi's settings".
func TestSettingsFiltersItsRowsAsYouTypeLikePisSettings(t *testing.T) {
	f, grants := settingsRuntime(t, tui.Facet)
	f.Do(func() { f.Runtime.Ctx.Commands().RunSlash("/settings") })
	waitFor(f, "Hide thinking")
	f.Send("tool calls")
	f.Poll(func() bool { return regexp.MustCompile(`→ Tool calls +quiet`).MatchString(screenText(f)) })
	if strings.Contains(screenText(f), "Hide thinking") {
		t.Fatalf("rows that do not match are hidden:\n%s", f.Screen())
	}
	f.Send("\r")
	f.Send("\x1b")
	f.Poll(func() bool { return grants.count() == 1 })
	if got := grants.list()[0]; got != `["savePrefs",{"patch":{"settings":{"plugins":{"quiet-tools":{"toolCalls":"full"}}}}}]` {
		t.Fatalf("grant = %s", got)
	}
}

func TestSettingsListsPluginRowsTuiSettingFieldsAndSavesThemUnderPlugins(t *testing.T) {
	f, grants := settingsRuntime(t, tui.Facet)
	f.Do(func() { f.Runtime.Ctx.Commands().RunSlash("/settings") })
	waitFor(f, "Hide thinking")
	// The plugin row comes after the theme and the 15 built-in rows.
	for range 16 {
		f.Send("\x1b[B")
	}
	f.Poll(func() bool { return regexp.MustCompile(`→ Tool calls +quiet`).MatchString(screenText(f)) })
	f.Send("\r")
	f.Send("\x1b")
	f.Poll(func() bool { return grants.count() == 1 })
	if got := grants.list()[0]; got != `["savePrefs",{"patch":{"settings":{"plugins":{"quiet-tools":{"toolCalls":"full"}}}}}]` {
		t.Fatalf("grant = %s", got)
	}
}

func TestHideThinkingBlockSetsTheThinkingViewTheToggleHoldsUntilTheSettingChangesAndAcrossAReload(t *testing.T) {
	f, _ := settingsRuntime(t)
	yes, no := true, false
	update := func(rt *runtime.TuiRuntime, settings prefs.TuiSettings) {
		rt.Appearance.Update(runtime.AppearanceUpdate{Local: localSettings(settings)})
	}
	f.Do(func() {
		rt := f.Runtime
		if rt.ShowThinking {
			t.Error("unset: hidden")
		}
		update(rt, prefs.TuiSettings{HideThinkingBlock: &no})
		if !rt.ShowThinking {
			t.Error("false shows the thinking")
		}
		rt.Ctx.SetShowThinking(false)
		update(rt, prefs.TuiSettings{HideThinkingBlock: &no})
		if rt.ShowThinking {
			t.Error("an unchanged setting keeps the toggle")
		}
		next := runtime.NewTuiRuntime(rt.Host, f.TUI, nil)
		defer next.Dispose()
		snapshot := maintui.SnapshotRuntime(rt)
		encoded, _ := json.Marshal(snapshot)
		var restored maintui.Snapshot
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Error(err)
		}
		maintui.RestoreRuntime(next, restored)
		update(next, prefs.TuiSettings{HideThinkingBlock: &no})
		if next.ShowThinking {
			t.Error("a reload keeps the toggle")
		}
		update(rt, prefs.TuiSettings{})
		if rt.ShowThinking {
			t.Error("an unset setting hides it again")
		}
		rt.Ctx.SetShowThinking(true)
		update(rt, prefs.TuiSettings{HideThinkingBlock: &no})
		update(rt, prefs.TuiSettings{HideThinkingBlock: &yes})
		if rt.ShowThinking {
			t.Error("a changed setting wins over the toggle")
		}
	})
}

func TestKeybindingsRebindsThroughSavePrefsLauncherOwnedKeysAreListedButNotEditable(t *testing.T) {
	f, grants := settingsRuntime(t)
	f.Do(func() { f.Runtime.Ctx.Commands().RunSlash("/keybindings") })
	waitFor(f, "Keybindings")
	for _, char := range "command palette" {
		f.Send(string(char))
	}
	waitFor(f, "Command palette")
	f.Send("\r")
	waitFor(f, "Press the new key")
	f.Send("\x0b")
	f.Poll(func() bool { return grants.count() == 1 })
	if got := grants.list()[0]; got != `["savePrefs",{"patch":{"keybindings":{"p2.palette":"ctrl+k"}}}]` {
		t.Fatalf("grant = %s", got)
	}
	f.Do(func() {
		f.Runtime.CloseOverlays()
		f.Runtime.Ctx.Commands().RunSlash("/keybindings")
	})
	waitFor(f, "Keybindings")
	for _, char := range "external editor" {
		f.Send(string(char))
	}
	waitFor(f, "launcher-owned")
	f.Send("\r")
	f.Poll(func() bool {
		for _, notice := range f.Runtime.Notices {
			if strings.Contains(notice.Text, "Launcher-owned") {
				return true
			}
		}
		return false
	})
	if grants.count() != 1 {
		t.Fatalf("a launcher-owned key was proposed: %v", grants.list())
	}
}
