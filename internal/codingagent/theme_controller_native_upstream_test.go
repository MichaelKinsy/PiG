package codingagent

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/test/theme-controller.test.ts (upstream 0.99.1), adapted to the driver-owned controller. The upstream `ui` mock maps to colorQueryRenderer: `queryTerminalColors` is the renderer's QueryTerminalColors, `setTerminalColorSchemeNotifications` is the CSI ? 2031 h/l write to the mode's output, and `emitTerminalColorScheme` is a scheme report (CSI ? 997 ; n) delivered to consumeTerminalThemeInput, the input boundary the renderer owns.

var (
	themeControllerDark  = tui.TerminalColors{Foreground: &tui.RgbColor{R: 248, G: 248, B: 242}, Background: &tui.RgbColor{R: 40, G: 42, B: 54}}
	themeControllerLight = tui.TerminalColors{Foreground: &tui.RgbColor{R: 30, G: 30, B: 30}, Background: &tui.RgbColor{R: 250, G: 250, B: 250}}
)

// colorQueryRenderer is the `queryTerminalColors` mock: every query is recorded and answered with the current answer, `{}` by default.
type colorQueryRenderer struct {
	tui.TUI
	options       []tui.TerminalColorQueryOptions
	answer        func() <-chan tui.TerminalColorsResult
	invalidations atomic.Int32
	renders       atomic.Int32
}

func settledColors(colors tui.TerminalColors) func() <-chan tui.TerminalColorsResult {
	return func() <-chan tui.TerminalColorsResult {
		result := make(chan tui.TerminalColorsResult, 1)
		result <- tui.TerminalColorsResult{Colors: colors}
		close(result)
		return result
	}
}

func (r *colorQueryRenderer) QueryTerminalColors(options tui.TerminalColorQueryOptions) <-chan tui.TerminalColorsResult {
	r.options = append(r.options, options)
	if r.answer == nil {
		return settledColors(tui.TerminalColors{})()
	}
	return r.answer()
}
func (r *colorQueryRenderer) Invalidate()           { r.invalidations.Add(1); r.TUI.Invalidate() }
func (r *colorQueryRenderer) RequestRender(...bool) { r.renders.Add(1); r.TUI.RequestRender() }

// syncedBuffer is the fixture's stdout: the renderer's render goroutine and the controller's escape writes share it, as they share process.stdout, so it serializes its callers.
type syncedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(data)
}

func (b *syncedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncedBuffer) Bytes() []byte { return []byte(b.String()) }

type themeControllerFixture struct {
	m        *InteractiveMode
	renderer *colorQueryRenderer
	output   *syncedBuffer
	ctx      context.Context
}

// themeControllerNew is `createController(createUi(), () => SettingsManager.inMemory(settings), initialThemeSetting)`. configured "" is an empty in-memory settings manager.
func themeControllerNew(t *testing.T, configured string, initial *string) *themeControllerFixture {
	t.Helper()
	restoreStartupTheme(t)
	t.Cleanup(func() { tui.SetTerminalColors(tui.TerminalColors{}); tui.SetTerminalColorScheme("") })
	manager := NewInMemorySettingsManager(Settings{Theme: configured})
	m := NewInteractiveMode(nil, InteractiveModeOptions{
		CWD: t.TempDir(), AgentDir: t.TempDir(),
		Settings: manager.Get(), SettingsManager: manager, InitialThemeSetting: initial,
	})
	ctx := t.Context()
	output := new(syncedBuffer)
	renderer := &colorQueryRenderer{TUI: tui.NewWithOutput(output, 100, 30)}
	m.tuiInst = renderer
	m.editor = tui.NewEditor()
	m.chatContainer = tui.NewContainer()
	m.keybindings = DefaultKeybindingsManager()
	m.backgroundCtx, m.runCtx = ctx, ctx
	m.themeState.output = output
	m.initTheme()
	t.Cleanup(func() { m.disposeTheme(); m.backgroundTasks.Wait() })
	return &themeControllerFixture{m: m, renderer: renderer, output: output, ctx: ctx}
}

// flush is `await flush()`: the latest color query completed and its colors applied.
func (f *themeControllerFixture) flush(t *testing.T) {
	t.Helper()
	if err := f.m.waitForTerminalColors(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func (f *themeControllerFixture) emitScheme(report string) { f.m.consumeTerminalThemeInput(report) }

const (
	schemeReportDark  = "\x1b[?997;1n"
	schemeReportLight = "\x1b[?997;2n"
)

func assertNativeControllerTheme(t *testing.T, want string) {
	t.Helper()
	if got := tui.ActiveTheme().Name; got != want {
		t.Fatalf("active theme = %q, want %q", got, want)
	}
}

func TestThemeControllerNativeUpstream(t *testing.T) {
	// theme-controller.test.ts:61
	t.Run("uses the initial theme without persisting it", func(t *testing.T) {
		f := themeControllerNew(t, "dark", new("light"))
		assertNativeControllerTheme(t, "light")
		if got := f.m.getThemeSelection(); got == nil || *got != "light" {
			t.Fatal("initial selection lost")
		}
		f.m.applyThemeFromSettings(f.ctx)
		f.flush(t)

		if len(f.renderer.options) != 1 {
			t.Errorf("terminal color queries = %d, want 1", len(f.renderer.options))
		}
		if got := f.m.opts.SettingsManager.GetTheme(); got != "dark" {
			t.Errorf("initial selection was persisted: setting = %q", got)
		}
	})

	// theme-controller.test.ts:78
	t.Run("applies the theme immediately and lets startup wait for the colors", func(t *testing.T) {
		f := themeControllerNew(t, "", nil)
		answer := make(chan tui.TerminalColorsResult, 1)
		f.renderer.answer = func() <-chan tui.TerminalColorsResult { return answer }
		f.m.applyThemeFromSettings(f.ctx)

		// Grayscale until the terminal answers.
		assertNativeControllerTheme(t, "system")
		if got := tui.ActiveTheme().GetFgAnsi("error"); got != "\x1b[39m" {
			t.Errorf("error before the colors arrive = %q, want the default foreground", got)
		}

		answer <- tui.TerminalColorsResult{Colors: themeControllerDark}
		close(answer)
		f.flush(t)
		if got := tui.ActiveTheme().GetFgAnsi("error"); !strings.HasPrefix(got, "\x1b[38;") {
			t.Errorf("error after the colors arrive = %q, want a color", got)
		}
	})

	// theme-controller.test.ts:98
	t.Run("falls back to palette indices, then applies colors that arrive after the timeout", func(t *testing.T) {
		f := themeControllerNew(t, "", nil)
		f.m.applyThemeFromSettings(f.ctx)
		f.flush(t)
		if got := tui.ActiveTheme().GetFgAnsi("error"); got != "\x1b[38;5;1m" {
			t.Fatalf("error after a timeout = %q, want palette index 1", got)
		}

		if len(f.renderer.options) != 1 || f.renderer.options[0].OnLateReply == nil {
			t.Fatalf("query options = %+v, want one query with a late-reply callback", f.renderer.options)
		}
		f.renderer.options[0].OnLateReply(themeControllerDark)
		if _, ok := tui.ActiveTheme().Colors()["error"].(tui.RgbColorValue); !ok {
			t.Errorf("error after the late reply = %#v, want an rgb color", tui.ActiveTheme().Colors()["error"])
		}
	})

	// theme-controller.test.ts:114
	t.Run("re-queries the colors on appearance changes and lets them decide", func(t *testing.T) {
		f := themeControllerNew(t, "", new("light/dark"))
		f.renderer.answer = settledColors(themeControllerLight)
		f.m.applyThemeFromSettings(f.ctx)
		if !strings.Contains(f.output.String(), "\x1b[?2031h") {
			t.Error("terminal color-scheme notifications were not enabled")
		}
		f.flush(t)
		assertNativeControllerTheme(t, "light")

		f.renderer.answer = settledColors(themeControllerDark)
		// The report says light, but the terminal renders dark.
		f.emitScheme(schemeReportLight)
		f.flush(t)
		assertNativeControllerTheme(t, "dark")
	})

	// theme-controller.test.ts:130
	t.Run("uses the reported scheme for the system theme when the terminal reports no colors", func(t *testing.T) {
		t.Setenv("COLORFGBG", "")
		f := themeControllerNew(t, "", nil)
		f.m.applyThemeFromSettings(f.ctx)
		f.flush(t)
		if got := tui.ActiveTheme().Appearance(); got != "dark" {
			t.Fatalf("appearance = %q, want dark", got)
		}

		f.emitScheme(schemeReportLight)
		if got := tui.ActiveTheme().Appearance(); got != "light" {
			t.Errorf("appearance after the report = %q, want light", got)
		}
		if got := tui.GetTerminalTheme(); got != "light" {
			t.Errorf("terminal theme = %q, want light", got)
		}
	})

	// theme-controller.test.ts:143
	t.Run("re-renders only when the reported colors change", func(t *testing.T) {
		f := themeControllerNew(t, "dark", nil)
		query := func(colors tui.TerminalColors) {
			f.renderer.answer = settledColors(colors)
			f.m.applyThemeFromSettings(f.ctx)
			f.flush(t)
		}

		query(themeControllerDark)
		// A timeout keeps the known colors; erasing them would count as a change and re-render.
		query(tui.TerminalColors{})
		clone := themeControllerDark
		clone.Foreground = &tui.RgbColor{R: 248, G: 248, B: 242}
		clone.Background = &tui.RgbColor{R: 40, G: 42, B: 54}
		query(clone)
		if got := f.renderer.renders.Load(); got != 1 {
			t.Errorf("requestRender calls = %d, want 1", got)
		}
	})

	// theme-controller.test.ts:159
	t.Run("disables terminal appearance updates when disposed", func(t *testing.T) {
		f := themeControllerNew(t, "light/dark", nil)
		f.m.applyThemeFromSettings(f.ctx)
		f.flush(t)

		f.m.disposeTheme()
		if !bytes.HasSuffix(f.output.Bytes(), []byte("\x1b[?2031l")) || f.m.themeState.autoSyncEnabled.Load() {
			t.Fatal("disposed controller retained terminal subscription")
		}
		f.emitScheme(schemeReportLight)
		if got := len(f.renderer.options); got != 1 {
			t.Errorf("a report after dispose queried the colors again: %d queries", got)
		}
	})

	// theme-controller.test.ts:171
	t.Run("lets an explicit selection replace the initial theme", func(t *testing.T) {
		f := themeControllerNew(t, "dark", new("light"))
		f.m.applyThemeFromSettings(f.ctx)

		if result := (&ExtUIContext{m: f.m}).SetTheme(extension.ThemeName("dark")); !result.Success {
			t.Fatal(result)
		}
		second := NewInMemorySettingsManager(Settings{Theme: "light"})
		f.m.opts.SettingsManager = second
		f.m.applyThemeFromSettings(f.ctx)
		f.flush(t)

		if got := f.m.getThemeSelection(); got == nil || *got != "dark" {
			t.Fatal("explicit selection replaced by manager setting")
		}
		assertNativeControllerTheme(t, "dark")
	})

	// theme-controller.test.ts:188
	t.Run("reloads theme settings when no initial theme was supplied", func(t *testing.T) {
		f := themeControllerNew(t, "dark", nil)
		f.m.applyThemeFromSettings(f.ctx)

		f.m.opts.SettingsManager.ApplyOverrides(Settings{Theme: "light"})
		f.m.applyThemeFromSettings(f.ctx)
		assertNativeControllerTheme(t, "light")

		second := NewInMemorySettingsManager(Settings{Theme: "light"})
		second.ApplyOverrides(Settings{Theme: "dark"})
		f.m.opts.SettingsManager = second
		f.m.applyThemeFromSettings(f.ctx)
		assertNativeControllerTheme(t, "dark")
	})
}
