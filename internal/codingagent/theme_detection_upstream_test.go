package codingagent

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// recordingQueryRenderer records the options each terminal background query receives.
type recordingQueryRenderer struct {
	tui.Renderer
	options []tui.TerminalColorQueryOptions
}

func (r *recordingQueryRenderer) QueryTerminalBackgroundColor(options tui.TerminalColorQueryOptions) <-chan tui.TerminalBackgroundColorResult {
	r.options = append(r.options, options)
	return r.Renderer.QueryTerminalBackgroundColor(options)
}

// packages/coding-agent/test/theme-detection.test.ts:44-101 (detectTerminalBackgroundTheme) and :103-141 (detectTerminalThemeForAuto). Upstream awaits Promise-returning queries; PiG issues the same queries through startupThemeDetection and settles from the input-loop replies or the deadline. The query deadline is the shared startupThemeQueryTimeout (upstream's callers pass timeoutMs: 100, startup-ui.ts:103 and theme-controller.ts:62,74) rather than a per-call parameter.
func TestThemeDetectionUpstream(t *testing.T) {
	t.Run("uses the queried terminal background before environment hints", func(t *testing.T) {
		var output bytes.Buffer
		renderer := &recordingQueryRenderer{Renderer: tui.NewWithOutput(&output, 80, 24)}
		detection := newStartupThemeDetection(nil, map[string]string{"COLORFGBG": "15;0"}, renderer)
		detection.backgroundOnly = true
		detection.start(func(string) error { t.Fatal("background-only detection wrote a color-scheme query"); return nil })
		if len(renderer.options) != 1 || time.Duration(renderer.options[0].TimeoutMs*float64(time.Millisecond)) != startupThemeQueryTimeout {
			t.Fatalf("query options=%+v, want one query with %v", renderer.options, startupThemeQueryTimeout)
		}
		if output.String() != terminalBackgroundQuery {
			t.Fatalf("queries=%q", output.String())
		}
		if consumed, settled := detection.consume("\x1b]11;rgb:fafa/fafa/fafa\x07"); !consumed || !settled {
			t.Fatalf("OSC 11 reply consumed=%v settled=%v", consumed, settled)
		}
		if got := detection.terminalTheme(); got != tui.TerminalTheme("light") {
			t.Fatalf("theme=%q, want the queried light background over COLORFGBG 15;0", got)
		}
		// source "terminal background", confidence "high": the answered background is what finishThemeDetection persists.
		if detection.background == nil {
			t.Fatal("terminal background answer not retained")
		}
		if startupThemeQueryTimeout != 100*time.Millisecond {
			t.Fatalf("startupThemeQueryTimeout=%v, want upstream's timeoutMs 100 (startup-ui.ts:103, theme-controller.ts:62,74)", startupThemeQueryTimeout)
		}
	})
	t.Run("falls back to environment hints when the terminal query returns no color", func(t *testing.T) {
		detection := newStartupThemeDetection(nil, map[string]string{"COLORFGBG": "15;0"}, tui.NewWithOutput(&bytes.Buffer{}, 80, 24))
		detection.backgroundOnly = true
		detection.start(func(string) error { return nil })
		detection.timeout()
		if got := detection.terminalTheme(); got != tui.TerminalTheme("dark") {
			t.Fatalf("theme=%q, want dark from COLORFGBG", got)
		}
		if got := tui.DetectTerminalBackground(tui.TerminalThemeDetectionOptions{Env: map[string]string{"COLORFGBG": "15;0"}}); got.Source != "COLORFGBG" || got.Confidence != "high" {
			t.Fatalf("environment detection=%+v", got)
		}
	})
	t.Run("falls back to environment hints when the terminal query fails", func(t *testing.T) {
		detection := newStartupThemeDetection(nil, map[string]string{"COLORFGBG": "0;15"}, tui.NewWithOutput(themeErrorWriter{errors.New("terminal write failed")}, 80, 24))
		detection.backgroundOnly = true
		detection.start(func(string) error { return nil })
		if !detection.readBackground() {
			t.Fatal("failed query did not settle before the deadline")
		}
		if !detection.backgroundAnswered || detection.background != nil {
			t.Fatalf("backgroundAnswered=%v background=%v, want an answered query without a color", detection.backgroundAnswered, detection.background)
		}
		if got := detection.terminalTheme(); got != tui.TerminalTheme("light") {
			t.Fatalf("theme=%q, want light from COLORFGBG", got)
		}
		if got := tui.DetectTerminalBackground(tui.TerminalThemeDetectionOptions{Env: map[string]string{"COLORFGBG": "0;15"}}); got.Source != "COLORFGBG" || got.Confidence != "high" {
			t.Fatalf("environment detection=%+v", got)
		}
	})
	t.Run("starts both queries and returns the preferred color-scheme result without waiting", func(t *testing.T) {
		var output bytes.Buffer
		renderer := &recordingQueryRenderer{Renderer: tui.NewWithOutput(&output, 80, 24)}
		detection := newStartupThemeDetection(new("light/dark"), nil, renderer)
		var schemeWrites []string
		detection.start(func(sequence string) error { schemeWrites = append(schemeWrites, sequence); return nil })
		if len(schemeWrites) != 1 || schemeWrites[0] != terminalColorSchemeQuery || len(renderer.options) != 1 {
			t.Fatalf("scheme writes=%q background queries=%d, want both started", schemeWrites, len(renderer.options))
		}
		// The background query never answers; the color-scheme reply settles alone.
		if consumed, settled := detection.consume("\x1b[?997;1n"); !consumed || !settled {
			t.Fatalf("scheme reply consumed=%v settled=%v", consumed, settled)
		}
		if got := detection.terminalTheme(); got != tui.TerminalTheme("dark") || detection.backgroundAnswered {
			t.Fatalf("theme=%q backgroundAnswered=%v, want dark without the background", got, detection.backgroundAnswered)
		}
	})
	t.Run("uses the background result when the color-scheme query fails", func(t *testing.T) {
		detection := newStartupThemeDetection(new("light/dark"), map[string]string{}, tui.NewWithOutput(&bytes.Buffer{}, 80, 24))
		detection.start(func(string) error { return errors.New("color-scheme query failed") })
		if consumed, settled := detection.consume("\x1b]11;rgb:fafa/fafa/fafa\x07"); !consumed || !settled {
			t.Fatalf("background reply consumed=%v settled=%v", consumed, settled)
		}
		if got := detection.terminalTheme(); got != tui.TerminalTheme("light") {
			t.Fatalf("theme=%q, want light", got)
		}
	})
}
