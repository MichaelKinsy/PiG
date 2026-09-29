package codingagent

import (
	"bytes"
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func nativeThemeControllerFixture(t *testing.T, configured string, initial *string) (*InteractiveMode, *bytes.Buffer, context.Context) {
	t.Helper()
	restoreStartupTheme(t)
	manager := NewSettingsManager(t.TempDir(), t.TempDir())
	if err := manager.SetTheme(configured); err != nil {
		t.Fatal(err)
	}
	m := NewInteractiveMode(InteractiveOptions{
		CWD: t.TempDir(), AgentDir: t.TempDir(),
		Settings: manager.Get(), SettingsManager: manager, InitialThemeSetting: initial,
	})
	ctx := t.Context()
	output := new(bytes.Buffer)
	m.tuiInst = tui.NewWithOutput(output, 100, 30)
	m.editor = tui.NewEditor()
	m.chatContainer = tui.NewContainer()
	m.keybindings = DefaultKeybindingsManager()
	m.installRenderDispatcher()
	m.backgroundCtx = ctx
	m.themeState.output = output
	tui.SetThemeSettingPresence(m.getThemeSelection())
	t.Cleanup(func() { m.disposeTheme(); m.backgroundTasks.Wait() })
	return m, output, ctx
}

func assertNativeControllerTheme(t *testing.T, want string) {
	t.Helper()
	if got := tui.ActiveTheme().Name; got != want {
		t.Fatalf("active theme = %q, want %q", got, want)
	}
}

// Upstream packages/coding-agent/test/theme-controller.test.ts:49-145, adapted to the existing driver-owned controller instead of introducing another controller implementation.
func TestThemeControllerNativeUpstream(t *testing.T) {
	t.Run("uses the initial theme without persisting it", func(t *testing.T) {
		m, output, ctx := nativeThemeControllerFixture(t, "dark", new("light"))
		assertNativeControllerTheme(t, "light")
		if got := m.getThemeSelection(); got == nil || *got != "light" {
			t.Fatal("initial selection lost")
		}
		m.applyThemeFromSettings(ctx)
		if output.Len() != 0 || m.opts.SettingsManager.GetTheme() != "dark" {
			t.Fatalf("initial selection queried or persisted: output=%q setting=%q", output, m.opts.SettingsManager.GetTheme())
		}
	})
	t.Run("resolves a theme pair and follows terminal appearance changes", func(t *testing.T) {
		t.Setenv("COLORFGBG", "15;0")
		m, output, ctx := nativeThemeControllerFixture(t, "dark/light", new("light/dark"))
		assertNativeControllerTheme(t, "dark")
		m.applyThemeFromSettings(ctx)
		m.consumeTerminalThemeInput("\x1b[?997;2n")
		assertNativeControllerTheme(t, "light")
		if !bytes.Contains(output.Bytes(), []byte("\x1b[?2031h")) {
			t.Fatal("terminal notifications not enabled")
		}
		m.consumeTerminalThemeInput("\x1b[?997;1n")
		assertNativeControllerTheme(t, "dark")
	})
	t.Run("disables terminal appearance updates when disposed", func(t *testing.T) {
		m, output, ctx := nativeThemeControllerFixture(t, "light/dark", nil)
		m.applyThemeFromSettings(ctx)
		m.consumeTerminalThemeInput("\x1b[?997;2n")
		m.disposeTheme()
		if !bytes.HasSuffix(output.Bytes(), []byte("\x1b[?2031l")) || m.themeState.autoSyncEnabled.Load() {
			t.Fatal("disposed controller retained terminal subscription")
		}
		m.consumeTerminalThemeInput("\x1b[?997;1n")
		assertNativeControllerTheme(t, "light")
	})
	t.Run("detects the current terminal appearance when selecting a theme pair", func(t *testing.T) {
		t.Setenv("COLORFGBG", "")
		m, output, ctx := nativeThemeControllerFixture(t, "dark", nil)
		assertNativeControllerTheme(t, "dark")
		m.buildSlashContext(ctx).OnSettingApplied("theme", "light/dark")
		m.consumeTerminalThemeInput("\x1b[?997;2n")
		assertNativeControllerTheme(t, "light")
		if bytes.Count(output.Bytes(), []byte(terminalColorSchemeQuery)) != 1 {
			t.Fatalf("query count: %q", output)
		}
	})
	t.Run("lets an explicit selection replace the initial theme", func(t *testing.T) {
		m, _, ctx := nativeThemeControllerFixture(t, "dark", new("light"))
		m.applyThemeFromSettings(ctx)
		if result := (&ExtUIContext{m: m}).SetTheme("dark"); !result.Success {
			t.Fatal(result)
		}
		second := NewSettingsManager(t.TempDir(), t.TempDir())
		if err := second.SetTheme("light"); err != nil {
			t.Fatal(err)
		}
		m.opts.SettingsManager = second
		m.applyThemeFromSettings(ctx)
		if got := m.getThemeSelection(); got == nil || *got != "dark" {
			t.Fatal("explicit selection replaced by manager setting")
		}
		assertNativeControllerTheme(t, "dark")
	})
	t.Run("reloads theme settings when no initial theme was supplied", func(t *testing.T) {
		m, _, ctx := nativeThemeControllerFixture(t, "dark", nil)
		m.applyThemeFromSettings(ctx)
		m.opts.SettingsManager.ApplyOverrides(Settings{Theme: "light"})
		m.applyThemeFromSettings(ctx)
		assertNativeControllerTheme(t, "light")
		second := NewSettingsManager(t.TempDir(), t.TempDir())
		if err := second.SetTheme("dark"); err != nil {
			t.Fatal(err)
		}
		m.opts.SettingsManager = second
		m.applyThemeFromSettings(ctx)
		assertNativeControllerTheme(t, "dark")
	})
}
