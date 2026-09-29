package codingagent

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi theme-controller.ts:159-173 applies each changed scheme synchronously,
// without persisting the selected half of an automatic pair.
func TestInteractiveAutomaticThemeNotifications(t *testing.T) {
	restoreStartupTheme(t)
	t.Setenv("COLORFGBG", "15;0")
	m, ctx := newCustomEditorDispatchMode(t)
	m.installRenderDispatcher()
	m.opts.Settings.Theme = "light/dark"
	dir := t.TempDir()
	m.opts.SettingsManager = NewSettingsManager(t.TempDir(), dir)
	if err := m.opts.SettingsManager.SetTheme("light/dark"); err != nil {
		t.Fatal(err)
	}
	m.inputReadCh = make(chan inputChunk, 1)
	m.inputReadCh <- inputChunk{data: []byte("\x1b[?997;1n")}
	var output bytes.Buffer
	if err := m.initializeTerminalTheme(ctx, &output); err != nil {
		t.Fatal(err)
	}
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

// Pi tui.ts:1006-1012 consumes scheme reports even with a fixed theme and no query.
func TestInteractiveFixedThemeConsumesSchemeReports(t *testing.T) {
	restoreStartupTheme(t)
	m, ctx := newCustomEditorDispatchMode(t)
	m.opts.Settings.Theme = "dark"
	tui.SetTheme("dark")
	m.extensionShortcutListener = func(string) bool { t.Fatal("scheme report reached listener"); return false }
	if err := m.dispatchKey(ctx, "\x1b[?997;2n"); err != nil {
		t.Fatal(err)
	}
	if tui.ActiveTheme().Name != "dark" {
		t.Fatal("fixed theme changed")
	}
}

func TestInteractiveThemeStateTransitions(t *testing.T) {
	restoreStartupTheme(t)
	t.Setenv("COLORFGBG", "15;0")
	m, ctx := newCustomEditorDispatchMode(t)
	m.installRenderDispatcher()
	var output bytes.Buffer
	m.themeState.output = &output
	m.opts.Settings.Theme = "light/dark"
	q := m.beginThemeDetection(&output)
	m.consumeTerminalThemeInput("\x1b]11;#000000\a")
	if q.detection.settled {
		t.Fatal("background settled before the scheme query")
	}
	m.consumeTerminalThemeInput("\x1b[?997;2n")
	spy := &themeInvalidationSpy{Renderer: m.tuiInst}
	m.tuiInst = spy
	// theme-controller.ts:159-173 suppresses duplicate reports; preview does not
	// replace the active selection or switch the mode off (114-122).
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
	if len(m.themeState.queries) != 0 {
		t.Fatal("settled queries retained after OSC reply")
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
	m.beginThemeDetection(&output)
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

func TestInteractiveThemeSettingQueriesWithoutBlockingModal(t *testing.T) {
	restoreStartupTheme(t)
	t.Setenv("COLORFGBG", "15;0")
	m, ctx := newCustomEditorDispatchMode(t)
	m.installRenderDispatcher()
	m.runCtx, m.backgroundCtx = ctx, ctx
	m.uiTaskCh = make(chan func())
	m.themeState.output = io.Discard
	defer func() { m.disposeTheme(); m.backgroundTasks.Wait() }()
	m.buildSlashContext(ctx).OnSettingApplied("theme", "light/dark")
	if len(m.themeState.queries) != 1 {
		t.Fatal("setting did not start terminal detection")
	}
	m.consumeTerminalThemeInput("\x1b]11;#ffffff\a")
	// The modal owner loop must service the query deadline without a keystroke.
	input := make(chan []byte, 1)
	queryDone := m.themeState.queries[0].done
	m.backgroundTasks.Go(func() {
		select {
		case <-queryDone:
			input <- []byte("x")
		case <-ctx.Done():
		}
	})
	if got, ok := m.readModalInput(input); !ok || string(got) != "x" {
		t.Fatalf("input = %q", got)
	}
	if tui.ActiveTheme().Name != "light" || !m.themeState.autoSyncEnabled.Load() {
		t.Fatal("modal did not finish background fallback")
	}
	m.backgroundTasks.Wait()
	m.buildSlashContext(ctx).OnSettingApplied("theme", "dark")
	if m.themeState.autoSyncEnabled.Load() {
		t.Fatal("fixed setting kept automatic sync")
	}
}

func TestInteractiveThemePendingQueriesAndShutdown(t *testing.T) {
	restoreStartupTheme(t)
	m, ctx := newCustomEditorDispatchMode(t)
	m.installRenderDispatcher()
	m.backgroundCtx, m.runCtx = ctx, ctx
	m.themeState.output = io.Discard
	m.opts.Settings.Theme = "light/dark"
	first := m.beginThemeDetection(io.Discard)
	first.detection.timeout()
	m.finishThemeDetection(first)
	second := m.beginThemeDetection(io.Discard)
	m.consumeTerminalThemeInput("\x1b]11;#ffffff\a")
	if second.detection.backgroundAnswered {
		t.Fatal("late OSC reply answered the newer query")
	}
	m.consumeTerminalThemeInput("\x1b]11;#000000\a")
	m.consumeTerminalThemeInput("\x1b[?997;2n")
	if tui.ActiveTheme().Name != "light" || len(m.themeState.queries) != 0 {
		t.Fatal("scheme did not settle newer query")
	}
	m.applyThemeFromSettings(ctx)
	m.disposeTheme()
	m.backgroundTasks.Wait()
	if m.themeState.autoSyncEnabled.Load() || len(m.themeState.queries) != 0 {
		t.Fatal("shutdown retained query or subscription")
	}
	m.consumeTerminalThemeInput("\x1b[?997;1n")
	if tui.ActiveTheme().Name != "light" {
		t.Fatal("report after shutdown changed theme")
	}
}

func TestInteractiveThemeRebindsRenderer(t *testing.T) {
	restoreStartupTheme(t)
	m := newSwitchTuiProbe(t)
	m.installRenderDispatcher()
	t.Cleanup(func() { m.stopInteractiveTui(); m.teardownCurrentTui() })
	m.opts.Settings.Theme = "light/dark"
	output := m.rendererOut.(*bytes.Buffer)
	m.beginThemeDetection(output)
	m.consumeTerminalThemeInput("\x1b]11;#000000\a")
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

func BenchmarkInteractiveAutomaticThemeNotifications(b *testing.B) {
	previous := tui.ActiveTheme().Name
	b.Cleanup(func() { tui.SetTheme(previous) })
	m := &InteractiveMode{tuiInst: tui.NewWithOutput(io.Discard, 100, 35)}
	m.installRenderDispatcher()
	m.opts.Settings.Theme = "light/dark"
	q := m.beginThemeDetection(io.Discard)
	q.detection.timeout()
	m.finishThemeDetection(q)
	m.consumeTerminalThemeInput("\x1b]11;#000000\a")
	b.Cleanup(m.disposeTheme)
	b.ReportAllocs()
	for b.Loop() {
		m.consumeTerminalThemeInput("\x1b[?997;2n")
		m.consumeTerminalThemeInput("\x1b[?997;1n")
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
