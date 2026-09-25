package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestEditorStatusBorderOptInAndWidth(t *testing.T) {
	editor := NewEditor()
	indicator := &StatusIndicator{Kind: "working", Loader: NewStyledLoader(ActiveTheme().Accent, ActiveTheme().Muted, "Working", nil)}
	editor.SetWorkingStatusIndicator(indicator)
	if got := widthx.StripAnsi(editor.Render(20)[1]); got != strings.Repeat("─", 20) {
		t.Fatalf("opt-out border=%q", got)
	}
	editor.EmbedWorkingStatus = true
	editor.ThinkingLevel = "high"
	if got := widthx.StripAnsi(editor.Render(20)[1]); got != "── ⠋ Working ───────" {
		t.Fatalf("working border=%q", got)
	}
	labels := []string{"Working", "Compacting context... (escape to cancel)", "Auto-compacting... (escape to cancel)", "Context overflow detected, Auto-compacting... (escape to cancel)", "Summarizing branch... (escape to cancel)", "Retrying (1/3) in 3s... (escape to cancel)"}
	for _, label := range labels {
		indicator.Message = label
		for _, width := range []int{1, 4, 10, 20, 80, 120} {
			if got := widthx.VisibleWidth(editor.Render(width)[1]); got != width {
				t.Fatalf("%q width %d rendered %d", label, width, got)
			}
		}
	}
	editor.SetWorkingStatusIndicator(nil)
	if got := widthx.StripAnsi(editor.Render(20)[1]); got != strings.Repeat("─", 20) {
		t.Fatalf("cleared border=%q", got)
	}
}

func TestEditorStatusPreservesCenteredOverflow(t *testing.T) {
	editor := NewEditor()
	editor.EmbedWorkingStatus = true
	editor.SetWorkingStatusIndicator(&StatusIndicator{Kind: "working", Loader: NewLoader("Working")})
	for _, tc := range []struct {
		width int
		want  string
	}{
		{40, "── ⠋ Working ── ↑ 3 more ───────────────"},
		{20, "── ⠋ ───────────────"},
		{22, "── ⠋ ─ ↑ 3 more ──────"},
		{10, "── ⠋ ─────"},
		{4, "───⠋"},
		{1, "⠋"},
	} {
		got := widthx.StripAnsi(editor.renderStatusBorder(tc.width, 3, strings.Repeat("─", tc.width)))
		if got != tc.want {
			t.Errorf("width %d: %q want %q", tc.width, got, tc.want)
		}
	}
}

func TestIdleStatusReservesStandaloneRows(t *testing.T) {
	lines := (&IdleStatus{}).Render(20)
	if len(lines) != 2 || lines[0] != strings.Repeat(" ", 20) || lines[1] != lines[0] {
		t.Fatalf("idle rows=%#v", lines)
	}
}

func BenchmarkEditorStatusBorder(b *testing.B) {
	editor := NewEditor()
	editor.EmbedWorkingStatus = true
	editor.SetWorkingStatusIndicator(&StatusIndicator{Kind: "working", Loader: NewLoader("Working")})
	b.ReportAllocs()
	for b.Loop() {
		editor.Render(120)
	}
}

func TestStatusBorderUsesJavaScriptTrimEnd(t *testing.T) {
	for _, tc := range []struct{ message, want string }{
		{"message\ufeff", "message"},
		{"message\u0085", "message\u0085"},
	} {
		status := &StatusIndicator{Loader: &Loader{Message: tc.message, Frames: []string{}, IndicatorVerbatim: true}}
		if got := status.RenderInBorder(30); got != tc.want {
			t.Errorf("message %q => %q; want %q", tc.message, got, tc.want)
		}
	}
}

// The per-child render cache must skip an editor whose output is unchanged and
// repaint it when only its embedded status indicator changed. Pi's Loader
// requests a render on every frame, and Pi's editor re-renders its border each
// frame, so a spinner tick always reaches the screen.
func TestEditorStatusIndicatorChangeDefeatsChildCache(t *testing.T) {
	editor := NewEditor()
	editor.EmbedWorkingStatus = true
	indicator := &StatusIndicator{Kind: "working", Loader: NewLoader("Working")}
	editor.SetWorkingStatusIndicator(indicator)
	root := NewContainer(editor)
	border := func() string { return widthx.StripAnsi(root.Render(40)[1]) }

	if got := border(); !strings.Contains(got, DefaultSpinnerFrames[0]+" Working") {
		t.Fatalf("first border=%q", got)
	}
	if editor.IsDirty() {
		t.Fatal("a rendered, unchanged editor stays dirty; every frame would re-render it")
	}
	indicator.Tick()
	if !editor.IsDirty() {
		t.Fatal("a spinner tick does not dirty the embedding editor")
	}
	if got := border(); !strings.Contains(got, DefaultSpinnerFrames[1]+" Working") {
		t.Fatalf("border after tick=%q; the cached frame hid the spinner advance", got)
	}
	if editor.IsDirty() || indicator.IsDirty() {
		t.Fatal("rendering did not consume the tick")
	}
	editor.EmbedWorkingStatus = false
	indicator.Tick()
	if editor.IsDirty() {
		t.Fatal("an editor that does not embed the status is dirtied by it")
	}
}

// Loader.SetMessage and SetIndicator mirror upstream setMessage/setIndicator:
// each change repaints the embedding editor, including a hidden indicator that
// will never tick again.
func TestLoaderRelabelDefeatsChildCache(t *testing.T) {
	editor := NewEditor()
	editor.EmbedWorkingStatus = true
	indicator := &StatusIndicator{Kind: "retry", Loader: NewLoader("Retrying (1/3) in 3s...")}
	editor.SetWorkingStatusIndicator(indicator)
	root := NewContainer(editor)
	border := func() string { return widthx.StripAnsi(root.Render(60)[1]) }
	border()

	indicator.SetMessage("Retrying (1/3) in 2s...")
	if got := border(); !strings.Contains(got, "⠋ Retrying (1/3) in 2s...") {
		t.Fatalf("border after SetMessage=%q", got)
	}
	indicator.Tick()
	indicator.SetIndicator([]string{}, true)
	if got := border(); !strings.Contains(got, "── Retrying (1/3) in 2s...") {
		t.Fatalf("border after hiding the indicator=%q", got)
	}
	indicator.SetIndicator(nil, false)
	if indicator.Frame != 0 || len(indicator.Frames) != len(DefaultSpinnerFrames) || indicator.IndicatorVerbatim {
		t.Fatalf("nil frames did not restore the default indicator: %+v", indicator.Loader)
	}
	if got := border(); !strings.Contains(got, DefaultSpinnerFrames[0]+" Retrying") {
		t.Fatalf("border after restoring the default indicator=%q", got)
	}
}
