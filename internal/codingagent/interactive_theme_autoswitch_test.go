package codingagent

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// newThemeDispatchMode is a dispatch-test mode whose owner-loop tasks queue for the test to service.
func newThemeDispatchMode(t *testing.T) (*InteractiveMode, context.Context) {
	t.Helper()
	restoreStartupTheme(t)
	m, ctx := newCustomEditorDispatchMode(t)
	m.installRenderDispatcher()
	m.uiTaskCh = make(chan func(), 64)
	m.backgroundCtx, m.runCtx = ctx, ctx
	m.themeState.output = io.Discard
	t.Cleanup(func() { m.disposeTheme(); m.backgroundTasks.Wait() })
	return m, ctx
}

// upstream 0.99.1 theme-controller.ts applyTerminalColorSchemeChange applies each changed scheme synchronously, without persisting the selected half of an automatic pair.
func TestInteractiveAutomaticThemeNotifications(t *testing.T) {
	t.Setenv("COLORFGBG", "15;0")
	m, ctx := newThemeDispatchMode(t)
	m.opts.Settings.Theme = "light/dark"
	dir := t.TempDir()
	m.opts.SettingsManager = NewSettingsManager(t.TempDir(), dir)
	if err := m.opts.SettingsManager.SetTheme("light/dark"); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	m.themeState.output = &output
	m.initTheme()
	m.applyThemeFromSettings(ctx)
	if !strings.HasSuffix(output.String(), "\x1b[?2031h") {
		t.Errorf("automatic notifications not enabled: %q", output.String())
	}
	m.extensionShortcutListener = func(string) bool { t.Fatal("scheme report reached extension shortcut"); return false }
	for _, step := range []struct{ report, want string }{
		{"\x1b[?997;2n", "light"},
		{"\x1b[?997;2n", "light"},
		{"\x1b[?997;1n", "dark"},
		{"\x1b[?997;1n\x1b[?997;2n", "light"},
	} {
		if err := m.dispatchKey(ctx, step.report); err != nil {
			t.Fatal(err)
		}
		if got := tui.ActiveTheme().Name; got != step.want {
			t.Fatalf("report %q: theme = %s, want %s", step.report, got, step.want)
		}
	}
	// Modal input bypasses dispatchInputChunk, but still consumes reports before listeners.
	if err := m.passTerminalInput(ctx, "\x1b[?997;1n", nil, func(context.Context, string) error { t.Fatal("report reached modal"); return nil }); err != nil {
		t.Fatal(err)
	}
	if tui.ActiveTheme().Name != "dark" {
		t.Fatal("modal report did not switch theme")
	}
	saved, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil || !strings.Contains(string(saved), `"theme": "light/dark"`) {
		t.Fatalf("automatic setting overwritten: %s, %v", saved, err)
	}
	m.stopInteractiveTui()
	if !strings.HasSuffix(output.String(), "\x1b[?2031l") {
		t.Errorf("notifications not disabled: %q", output.String())
	}
}

// upstream 0.99.1 tui.ts consumes scheme reports even with a fixed theme and no notifications.
func TestInteractiveFixedThemeConsumesSchemeReports(t *testing.T) {
	m, ctx := newThemeDispatchMode(t)
	m.opts.Settings.Theme = "dark"
	m.initTheme()
	m.applyThemeFromSettings(ctx)
	m.extensionShortcutListener = func(string) bool { t.Fatal("scheme report reached listener"); return false }
	if err := m.dispatchKey(ctx, "\x1b[?997;2n"); err != nil {
		t.Fatal(err)
	}
	if tui.ActiveTheme().Name != "dark" {
		t.Fatal("fixed theme changed")
	}
}

func TestInteractiveThemeStateTransitions(t *testing.T) {
	t.Setenv("COLORFGBG", "15;0")
	m, ctx := newThemeDispatchMode(t)
	var output bytes.Buffer
	m.themeState.output = &output
	m.opts.Settings.Theme = "light/dark"
	m.initTheme()
	m.applyThemeFromSettings(ctx)
	m.consumeTerminalThemeInput("\x1b[?997;2n")
	spy := &themeInvalidationSpy{Renderer: m.tuiInst}
	m.tuiInst = spy
	// theme-controller.ts applyTerminalColorSchemeChange re-applies only when the appearance changed; preview does not replace the active selection or switch the mode off.
	m.consumeTerminalThemeInput("\x1b[?997;2n")
	if spy.invalidates != 0 {
		t.Fatal("duplicate report invalidated the UI")
	}
	m.previewTheme("dark")
	m.consumeTerminalThemeInput("\x1b[?997;2n")
	if tui.ActiveTheme().Name != "dark" {
		t.Fatal("duplicate report discarded the preview")
	}
	m.previewTheme("light/dark")
	if tui.ActiveTheme().Name != "light" {
		t.Fatal("preview resolved against environment instead of latest report")
	}
	m.consumeTerminalThemeInput("\x1b[?997;1n")
	if tui.ActiveTheme().Name != "dark" {
		t.Fatal("preview disabled auto sync")
	}

	ui := &ExtUIContext{m: m}
	if result := ui.SetTheme("light"); !result.Success {
		t.Fatal(result)
	}
	if m.themeState.autoSyncEnabled.Load() {
		t.Fatal("explicit extension theme kept automatic notifications")
	}
	if err := m.dispatchKey(ctx, "\x1b[?997;1n"); err != nil {
		t.Fatal(err)
	}
	if tui.ActiveTheme().Name != "light" {
		t.Fatal("report overrode explicit extension theme")
	}
	m.opts.Settings.Theme = "dark"
	m.applyThemeFromSettings(ctx)
	if tui.ActiveTheme().Name != "light" {
		t.Fatal("reload discarded explicit selection")
	}
	if result := ui.SetTheme("light/dark"); result.Success {
		t.Fatal("extension name API accepted a setting instead of a theme name")
	}
}

type themeInvalidationSpy struct {
	tui.Renderer
	invalidates int
}

func (s *themeInvalidationSpy) Invalidate() { s.invalidates++; s.Renderer.Invalidate() }

// upstream 0.99.1 theme-controller.ts applyFromSettings does not wait for the terminal: the modal owner loop applies the colors when they arrive, without a keystroke.
func TestInteractiveThemeSettingQueriesWithoutBlockingModal(t *testing.T) {
	t.Setenv("COLORFGBG", "15;0")
	m, ctx := newThemeDispatchMode(t)
	pending := make(chan tui.TerminalColorsResult, 1)
	answers := []<-chan tui.TerminalColorsResult{pending}
	renderer := &colorQueryRenderer{Renderer: m.tuiInst, answer: func() <-chan tui.TerminalColorsResult {
		if len(answers) == 0 {
			return settledColors(tui.TerminalColors{})()
		}
		answer := answers[0]
		answers = answers[1:]
		return answer
	}}
	m.tuiInst = renderer
	m.initTheme()
	m.buildSlashContext(ctx).OnSettingApplied("theme", "light/dark")
	if len(renderer.options) != 1 {
		t.Fatalf("queries = %d, want the setting to start one terminal query", len(renderer.options))
	}
	white := tui.RgbColor{R: 255, G: 255, B: 255}
	pending <- tui.TerminalColorsResult{Colors: tui.TerminalColors{Background: &white}}
	input := make(chan []byte, 1)
	m.backgroundTasks.Go(func() {
		// The colors apply, and render once, before the keystroke arrives.
		for renderer.renders.Load() == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
		}
		input <- []byte("x")
	})
	if got, ok := m.readModalInput(input); !ok || string(got) != "x" {
		t.Fatalf("input = %q", got)
	}
	if tui.ActiveTheme().Name != "light" || !m.themeState.autoSyncEnabled.Load() {
		t.Fatal("modal did not apply the reported colors")
	}
	m.buildSlashContext(ctx).OnSettingApplied("theme", "dark")
	if m.themeState.autoSyncEnabled.Load() {
		t.Fatal("fixed setting kept automatic sync")
	}
}

// upstream 0.99.1 theme-controller.ts requestTerminalColors passes onLateReply, and dispose disables notifications and ignores later reports.
func TestInteractiveThemeLateRepliesAndShutdown(t *testing.T) {
	m, ctx := newThemeDispatchMode(t)
	renderer := &colorQueryRenderer{Renderer: m.tuiInst}
	m.tuiInst = renderer
	m.opts.Settings.Theme = "light/dark"
	m.initTheme()
	m.applyThemeFromSettings(ctx)
	m.applyThemeFromSettings(ctx)
	if len(renderer.options) != 2 || renderer.options[1].OnLateReply == nil {
		t.Fatalf("queries = %+v", renderer.options)
	}
	white := tui.RgbColor{R: 255, G: 255, B: 255}
	renderer.options[1].OnLateReply(tui.TerminalColors{Background: &white})
	if tui.ActiveTheme().Name != "light" {
		t.Fatalf("theme = %q, want light after a late white background", tui.ActiveTheme().Name)
	}
	m.disposeTheme()
	m.backgroundTasks.Wait()
	if m.themeState.autoSyncEnabled.Load() {
		t.Fatal("shutdown retained the subscription")
	}
	if !m.consumeTerminalThemeInput("\x1b[?997;1n") || tui.ActiveTheme().Name != "light" {
		t.Fatal("report after shutdown changed the theme or reached input")
	}
}

func TestInteractiveThemeRebindsRenderer(t *testing.T) {
	restoreStartupTheme(t)
	t.Setenv("COLORFGBG", "15;0")
	m := newSwitchTuiProbe(t)
	m.installRenderDispatcher()
	t.Cleanup(func() { m.stopInteractiveTui(); m.teardownCurrentTui() })
	m.opts.Settings.Theme = "light/dark"
	output := m.rendererOut.(*bytes.Buffer)
	m.initTheme()
	m.applyThemeFromSettings(t.Context())
	m.consumeTerminalThemeInput("\x1b[?997;1n")
	for _, mode := range []string{"fullscreen", "regular"} {
		output.Reset()
		if !m.switchTuiMode(mode, false) {
			t.Fatal("renderer switch refused")
		}
		bytes := output.String()
		disable, enable := strings.Index(bytes, "\x1b[?2031l"), strings.Index(bytes, "\x1b[?2031h")
		if disable < 0 || enable <= disable {
			t.Fatalf("notification lifecycle = %q", bytes)
		}
		m.consumeTerminalThemeInput("\x1b[?997;2n")
		if tui.ActiveTheme().Name != "light" {
			t.Fatal("renderer switch lost subscription")
		}
		m.consumeTerminalThemeInput("\x1b[?997;1n")
		if tui.ActiveTheme().Name != "dark" {
			t.Fatal("renderer switch lost reverse transition")
		}
	}
}

func TestTerminalColorSchemeReportParser(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  tui.TerminalTheme
	}{
		{"\x1b[?997;1n", "dark"}, {"\x1b[?997;2n", "light"},
		{"\x1b[?997;1n\x1b[?997;2n", "light"}, {"\x1b[?997;2n\x1b[?997;1n", "dark"},
		{"", ""}, {"\x1b[?997;0n", ""}, {"\x1b[?997;3n", ""},
		{"\x1b[?997;2", ""}, {"x\x1b[?997;2n", ""}, {"\x1b[?997;2nx", ""},
		{"\x1b[I", ""}, {"\x1b]11;#ffffff\a", ""},
	} {
		if got := tui.ParseTerminalColorSchemeReport(tc.input); got != tc.want {
			t.Errorf("parse(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// upstream 0.99.1 theme-controller.ts TERMINAL_QUERY_TIMEOUT_MS: every startup and reload color query waits 100 ms for the terminal and accepts late replies.
func TestThemeControllerQueriesWithTheUpstreamTimeout(t *testing.T) {
	f := themeControllerNew(t, "", nil)
	f.m.applyThemeFromSettings(f.ctx)
	f.flush(t)
	if len(f.renderer.options) == 0 {
		t.Fatal("applyThemeFromSettings did not query the terminal colors")
	}
	for _, options := range f.renderer.options {
		if options.TimeoutMs != 100 || options.OnLateReply == nil {
			t.Errorf("query options = timeout %v, late reply set %v; want 100 ms with a late reply hook", options.TimeoutMs, options.OnLateReply != nil)
		}
	}
}
