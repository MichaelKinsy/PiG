package codingagent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// themeQueryOutputRenderer keeps this fixture's discarded paint output separate from its asserted control writer while running the real renderer-owned query queue.
type themeQueryOutputRenderer struct {
	tui.Renderer
	queries *tui.TUI
}

func (r *themeQueryOutputRenderer) QueryTerminalBackgroundColor(options tui.TerminalColorQueryOptions) <-chan tui.TerminalBackgroundColorResult {
	return r.queries.QueryTerminalBackgroundColor(options)
}

func (r *themeQueryOutputRenderer) ConsumeOsc11BackgroundResponse(data string) bool {
	return r.queries.ConsumeOsc11BackgroundResponse(data)
}

func setThemeQueryTestOutput(m *InteractiveMode, output io.Writer) {
	m.tuiInst = &themeQueryOutputRenderer{Renderer: m.tuiInst, queries: tui.NewWithOutput(output, 80, 24)}
}

// theme-controller.ts:57-81: stock settings query OSC 11, not CSI 996,
// then persist only an OSC or COLORFGBG answer (never the dark fallback).
func TestInteractiveStockThemeDetection(t *testing.T) {
	for _, tc := range []struct {
		name, setting, hint, reply, want, saved string
	}{
		{"white beats dark env", "", "15;0", "\x1b]11;rgb:ffff/ffff/ffff\x07", "light", "light"},
		{"black beats light env", "", "0;15", "\x1b]11;#000000\x1b\\", "dark", "dark"},
		{"rgba white", "", "15;0", "\x1b]11;rgba:ffff/ffff/ffff/ffff\x07", "light", "light"},
		{"invalid falls back to env", "", "0;15", "\x1b]11;invalid\x07", "light", "light"},
		{"silent falls back to env", "", "0;15", "", "light", "light"},
		{"BOM whitespace in env", "", "\ufeff15\ufeff", "", "light", "light"},
		{"NEXT LINE is not whitespace", "", "\u008515\u0085", "", "dark", ""},
		{"silent dark is not saved", "", "", "", "dark", ""},
		{"invalid dark is not saved", "", "", "\x1b]11;invalid\x07", "dark", ""},
		{"fixed theme does not query", "dark", "0;15", "", "dark", ""},
		{"auto pair prefers color scheme", "light/dark", "0;15", "\x1b[?997;1n", "dark", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restoreStartupTheme(t)
			t.Setenv("COLORFGBG", tc.hint)
			m, ctx := newCustomEditorDispatchMode(t)
			m.opts.Settings.Theme = tc.setting
			tui.SetThemeSettingPresence(m.opts.Settings.themeSetting())
			dir := t.TempDir()
			m.opts.SettingsManager = NewSettingsManager(t.TempDir(), dir)
			m.opts.SettingsManager.ApplyOverrides(Settings{Theme: tc.setting})
			m.inputReadCh = make(chan inputChunk, 2)
			m.inputErrCh = make(chan error, 1)
			m.inputReadCh <- inputChunk{data: []byte("x")}
			if tc.reply != "" {
				m.inputReadCh <- inputChunk{data: []byte(tc.reply)}
			}
			var output bytes.Buffer
			setThemeQueryTestOutput(m, &output)
			if err := m.initializeTerminalTheme(ctx, &output); err != nil {
				t.Fatal(err)
			}
			wantQuery := terminalBackgroundQuery
			if tc.setting == "dark" {
				wantQuery = ""
			}
			if tc.setting == "light/dark" {
				wantQuery = terminalColorSchemeQuery + terminalBackgroundQuery + "\x1b[?2031h"
			}
			if output.String() != wantQuery {
				t.Fatalf("queries = %q, want %q", output.String(), wantQuery)
			}
			if got := tui.ActiveTheme().Name; got != tc.want {
				t.Errorf("theme = %s, want %s", got, tc.want)
			}
			if tc.setting != "dark" && m.editor.Text() != "x" {
				t.Errorf("input = %q, want only x", m.editor.Text())
			}
			data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
			if tc.saved == "" {
				if err == nil && strings.Contains(string(data), `"theme"`) {
					t.Fatalf("unexpected persisted theme: %s", data)
				}
			} else if err != nil || !strings.Contains(string(data), `"theme": "`+tc.saved+`"`) {
				t.Fatalf("saved settings = %s, err=%v", data, err)
			}
			// A late reply is consumed before extension listeners/editor dispatch,
			// and does not replace the already selected fallback.
			if tc.reply == "" && tc.setting == "" {
				if err := m.dispatchKey(ctx, "\x1b]11;#ffffff\x07"); err != nil {
					t.Fatal(err)
				}
				if tui.ActiveTheme().Name != tc.want || m.editor.Text() != "x" {
					t.Fatal("late reply changed theme or editor")
				}
			}
		})
	}
}

func TestInteractiveLateBackgroundReplyPrecedesModalInput(t *testing.T) {
	m, ctx := newCustomEditorDispatchMode(t)
	q := m.beginThemeDetection(io.Discard)
	q.detection.timeout()
	m.finishThemeDetection(q)
	m.extensionShortcutListener = func(string) bool {
		t.Fatal("terminal reply reached an extension shortcut")
		return false
	}
	if err := m.passTerminalInput(ctx, "\x1b]11;#ffffff\x07", nil, func(context.Context, string) error {
		t.Fatal("terminal reply reached modal input")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInteractiveThemeDetectionCancellationAndErrors(t *testing.T) {
	for _, kind := range []string{"cancel", "read", "write", "save"} {
		t.Run(kind, func(t *testing.T) {
			restoreStartupTheme(t)
			m, _ := newCustomEditorDispatchMode(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m.inputReadCh = make(chan inputChunk, 1)
			m.inputErrCh = make(chan error, 1)
			output := io.Discard
			sentinel := errors.New("terminal failed")
			switch kind {
			case "cancel":
				cancel()
			case "read":
				m.inputErrCh <- sentinel
			case "write":
				t.Setenv("COLORFGBG", "0;15")
				output = themeErrorWriter{sentinel}
			case "save":
				dir := t.TempDir()
				m.opts.SettingsManager = NewSettingsManager(t.TempDir(), dir)
				if err := os.Mkdir(filepath.Join(dir, "settings.json"), 0o700); err != nil {
					t.Fatal(err)
				}
				m.inputReadCh <- inputChunk{data: []byte("\x1b]11;#ffffff\x07")}
			}
			setThemeQueryTestOutput(m, output)
			err := m.initializeTerminalTheme(ctx, output)
			if kind == "write" || kind == "save" {
				// theme.ts catches query rejection; settings-manager.ts enqueueWrite
				// records write errors without rejecting flush or ending startup.
				if err != nil || tui.ActiveTheme().Name != "light" {
					t.Fatalf("recoverable %s error: theme=%s err=%v", kind, tui.ActiveTheme().Name, err)
				}
				if kind == "save" && len(m.opts.SettingsManager.DrainErrors()) == 0 {
					t.Fatal("settings write error was not retained for diagnostics")
				}
			} else if err == nil {
				t.Fatal("terminal cancellation/read failure was swallowed")
			}
		})
	}
}

type themeErrorWriter struct{ err error }

func (w themeErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func BenchmarkInteractiveThemeDetection(b *testing.B) {
	previous := tui.ActiveTheme().Name
	b.Cleanup(func() { tui.SetTheme(previous) })
	m := &InteractiveMode{tuiInst: tui.NewWithOutput(io.Discard, 100, 35), inputReadCh: make(chan inputChunk, 1)}
	b.ReportAllocs()
	for b.Loop() {
		m.opts.Settings.Theme = ""
		m.inputReadCh <- inputChunk{data: []byte("\x1b]11;#ffffff\x07")}
		if err := m.initializeTerminalTheme(context.Background(), io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}
