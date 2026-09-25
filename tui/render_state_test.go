package tui

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type renderFuncComponent func(width int) []string

func (f renderFuncComponent) Render(width int) []string { return f(width) }
func (f renderFuncComponent) Invalidate()               {}

func TestTUIRender_NoContentChangeStillRepositionsCursor(t *testing.T) {
	var markerAt = 2
	comp := renderFuncComponent(func(width int) []string {
		base := "abc"
		return []string{base[:markerAt] + widthx.CursorMarker + base[markerAt:]}
	})
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 5)
	ui.Add(comp)
	ui.Render()
	out.Reset()

	// Same visible output "abc", different marker position.
	markerAt = 1
	ui.Render()
	got := out.String()
	if strings.Contains(got, "abc") {
		t.Fatalf("second render should not rewrite identical content, got %q", got)
	}
	if !strings.Contains(got, "\x1b[2G") {
		t.Fatalf("second render should reposition hardware cursor to col 2, got %q", got)
	}
}

func TestTUIRender_TermuxHeightChangeBypassesFullRedraw(t *testing.T) {
	old := os.Getenv("TERMUX_VERSION")
	_ = os.Setenv("TERMUX_VERSION", "1")
	defer func() { _ = os.Setenv("TERMUX_VERSION", old) }()

	comp := renderFuncComponent(func(width int) []string {
		return []string{"one", "two", "three"}
	})
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 4)
	ui.Add(comp)
	ui.Render()
	out.Reset()

	// Height change only. In Termux this should NOT force full clear/redraw.
	ui.height = 6
	ui.Render()
	got := out.String()
	if strings.Contains(got, "\x1b[2J\x1b[H\x1b[3J") {
		t.Fatalf("termux height change should bypass full redraw, got %q", got)
	}
}

func TestTUIRender_ResizeFullRedrawClearsScrollbackAndReplaysBuffer(t *testing.T) {
	lines := []string{"one", "two", "three", "four", "five"}
	comp := renderFuncComponent(func(width int) []string { return lines })
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 3)
	ui.Add(comp)
	ui.Render()
	out.Reset()

	ui.width = 30
	ui.Render()
	got := out.String()
	if !strings.Contains(got, "\x1b[2J\x1b[H\x1b[3J") {
		t.Fatalf("width resize should full-clear like upstream, got %q", got)
	}
	for _, want := range []string{"one", "two", "three", "four", "five"} {
		if !strings.Contains(got, want) {
			t.Fatalf("width resize missing visible line %q in %q", want, got)
		}
	}
}

func TestTUIRender_ClearOnShrinkForcesFullRedraw(t *testing.T) {
	lines := []string{"one", "two", "three"}
	comp := renderFuncComponent(func(width int) []string { return lines })
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 5)
	ui.SetClearOnShrink(true)
	ui.Add(comp)
	ui.Render()
	out.Reset()

	lines = []string{"one"}
	ui.Render()
	got := out.String()
	if !strings.Contains(got, "\x1b[2J\x1b[H\x1b[3J") {
		t.Fatalf("clearOnShrink should full-clear like upstream, got %q", got)
	}
}

// Pi 0.87.1's tui.ts initializes clearOnShrink to false and never reads
// PI_CLEAR_ON_SHRINK; only the coding agent's settings manager honors the
// variable and passes the result through setClearOnShrink.
func TestTUIClearOnShrinkIgnoresEnvironment(t *testing.T) {
	t.Setenv("PI_CLEAR_ON_SHRINK", "1")
	if NewWithOutput(&bytes.Buffer{}, 20, 5).GetClearOnShrink() {
		t.Fatal("NewWithOutput read PI_CLEAR_ON_SHRINK; upstream TUI defaults clearOnShrink to false")
	}
	if New().GetClearOnShrink() {
		t.Fatal("New read PI_CLEAR_ON_SHRINK; upstream TUI defaults clearOnShrink to false")
	}
}

func TestTUIRender_ShowHardwareCursorToggleAffectsOutput(t *testing.T) {
	comp := renderFuncComponent(func(width int) []string {
		return []string{"ab" + widthx.CursorMarker + "c"}
	})

	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 5)
	ui.Add(comp)
	ui.Render()
	if !strings.Contains(out.String(), "\x1b[?25l") {
		t.Fatalf("default render should hide hardware cursor, got %q", out.String())
	}

	out.Reset()
	ui.SetShowHardwareCursor(true)
	// SetShowHardwareCursor triggers a render after first render.
	got := out.String()
	if !strings.Contains(got, "\x1b[?25h") {
		t.Fatalf("enabling hardware cursor should emit show-cursor escape, got %q", got)
	}
}

func TestTUIRender_StateTrace_AppendScrollThenViewportFallback(t *testing.T) {
	lines := []string{"one", "two"}
	comp := renderFuncComponent(func(width int) []string { return lines })
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 3)
	ui.Add(comp)

	ui.Render()
	if ui.cursorRow != 1 || ui.hardwareCursorRow != 1 || ui.prevViewportTop != 0 || ui.maxLinesRendered != 2 {
		t.Fatalf("first render state = {cursor=%d hardware=%d viewport=%d max=%d}, want {1 1 0 2}",
			ui.cursorRow, ui.hardwareCursorRow, ui.prevViewportTop, ui.maxLinesRendered)
	}

	out.Reset()
	lines = []string{"one", "two", "three", "four"}
	ui.Render()
	if strings.Contains(out.String(), "\x1b[2J\x1b[H\x1b[3J") {
		t.Fatalf("append-only render should not full-clear, got %q", out.String())
	}
	if ui.cursorRow != 3 || ui.hardwareCursorRow != 3 || ui.prevViewportTop != 1 || ui.maxLinesRendered != 4 {
		t.Fatalf("append render state = {cursor=%d hardware=%d viewport=%d max=%d}, want {3 3 1 4}",
			ui.cursorRow, ui.hardwareCursorRow, ui.prevViewportTop, ui.maxLinesRendered)
	}

	out.Reset()
	lines = []string{"ONE", "two", "three", "four"}
	ui.Render()
	got := out.String()
	if !strings.Contains(got, "\x1b[2J\x1b[H\x1b[3J") {
		t.Fatalf("viewport fallback should full-clear like upstream, got %q", got)
	}
	if !strings.Contains(got, "ONE") {
		t.Fatalf("viewport fallback should replay full buffer after clear, got %q", got)
	}
	if ui.cursorRow != 3 || ui.hardwareCursorRow != 3 || ui.prevViewportTop != 1 || ui.maxLinesRendered != 4 {
		t.Fatalf("viewport-fallback state = {cursor=%d hardware=%d viewport=%d max=%d}, want {3 3 1 4}",
			ui.cursorRow, ui.hardwareCursorRow, ui.prevViewportTop, ui.maxLinesRendered)
	}
}

func TestTUIRender_StateTrace_DeletionOnlyKeepsViewportState(t *testing.T) {
	lines := []string{"one", "two", "three"}
	comp := renderFuncComponent(func(width int) []string { return lines })
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 4)
	ui.Add(comp)
	ui.Render()
	out.Reset()

	lines = []string{"one"}
	ui.Render()
	if strings.Contains(out.String(), "\x1b[2J\x1b[H\x1b[3J") {
		t.Fatalf("deletion-only render should clear rows in place, got %q", out.String())
	}
	if ui.cursorRow != 0 || ui.hardwareCursorRow != 0 || ui.prevViewportTop != 0 || ui.maxLinesRendered != 3 {
		t.Fatalf("deletion-only state = {cursor=%d hardware=%d viewport=%d max=%d}, want {0 0 0 3}",
			ui.cursorRow, ui.hardwareCursorRow, ui.prevViewportTop, ui.maxLinesRendered)
	}
}

func FuzzTUIRender_StateMachineInvariants(f *testing.F) {
	f.Add(uint8(0), uint8(2), uint8(4), uint8(1), uint8(3), uint8(5))
	f.Add(uint8(5), uint8(1), uint8(0), uint8(4), uint8(2), uint8(3))

	f.Fuzz(func(t *testing.T, a, b, c, d, e, g uint8) {
		counts := []uint8{a, b, c}
		heights := []uint8{d, e, g}
		lines := fuzzRenderLines(counts[0])
		comp := renderFuncComponent(func(width int) []string { return lines })
		var out bytes.Buffer
		ui := NewWithOutput(&out, 20, max(1, int(heights[0]%6)+1))
		ui.Add(comp)

		for i := range counts {
			lines = fuzzRenderLines(counts[i])
			ui.height = max(1, int(heights[i]%6)+1)
			ui.Render()

			if ui.prevHeight != ui.height {
				t.Fatalf("prevHeight=%d want %d", ui.prevHeight, ui.height)
			}
			if ui.prevWidth != ui.width {
				t.Fatalf("prevWidth=%d want %d", ui.prevWidth, ui.width)
			}
			if ui.cursorRow != max(0, len(ui.prevLines)-1) {
				t.Fatalf("cursorRow=%d want %d", ui.cursorRow, max(0, len(ui.prevLines)-1))
			}
			if ui.hardwareCursorRow < 0 {
				t.Fatalf("hardwareCursorRow=%d want >= 0", ui.hardwareCursorRow)
			}
			maxViewportTop := max(0, max(len(ui.prevLines), ui.height)-ui.height)
			if ui.prevViewportTop < 0 || ui.prevViewportTop > maxViewportTop {
				t.Fatalf("prevViewportTop=%d outside [0,%d]", ui.prevViewportTop, maxViewportTop)
			}
		}
	})
}

func fuzzRenderLines(n uint8) []string {
	count := int(n % 7)
	lines := make([]string, count)
	for i := range count {
		lines[i] = fmt.Sprintf("line-%d", i)
	}
	return lines
}

// TestTUICaptureRestoreRenderStateRoundTrip proves the main-screen render state
// survives a capture then restore: the inline-flow fields return to their
// captured values so the next render diffs against the pre-switch screen. This is
// the scrollback-preservation contract a live tui-mode switch depends on.
func TestTUICaptureRestoreRenderStateRoundTrip(t *testing.T) {
	var out bytes.Buffer
	tu := NewWithOutput(&out, 40, 10)
	tu.Add(NewText("line one"))
	tu.Add(NewText("line two"))
	tu.Render()

	captured := tu.CaptureRenderState()
	if len(captured.PrevLines) == 0 {
		t.Fatal("captured render state has no previous lines after a render")
	}

	// Clobber the live state as a renderer swap would.
	tu.mu.Lock()
	tu.prevLines = nil
	tu.cursorRow = 0
	tu.hardwareCursorRow = 0
	tu.maxLinesRendered = 0
	tu.prevViewportTop = 0
	tu.prevWidth = -1
	tu.prevHeight = -1
	tu.hasRendered = false
	tu.mu.Unlock()

	tu.RestoreRenderState(captured)

	tu.mu.Lock()
	defer tu.mu.Unlock()
	if len(tu.prevLines) != len(captured.PrevLines) {
		t.Fatalf("prevLines len = %d, want %d", len(tu.prevLines), len(captured.PrevLines))
	}
	if tu.cursorRow != captured.CursorRow || tu.hardwareCursorRow != captured.HardwareCursorRow {
		t.Fatalf("cursor rows = (%d,%d), want (%d,%d)", tu.cursorRow, tu.hardwareCursorRow, captured.CursorRow, captured.HardwareCursorRow)
	}
	if tu.maxLinesRendered != captured.MaxLinesRendered || tu.prevViewportTop != captured.PrevViewportTop {
		t.Fatalf("maxLines/viewportTop = (%d,%d), want (%d,%d)", tu.maxLinesRendered, tu.prevViewportTop, captured.MaxLinesRendered, captured.PrevViewportTop)
	}
	if tu.prevWidth != captured.PrevWidth || tu.prevHeight != captured.PrevHeight {
		t.Fatalf("width/height = (%d,%d), want (%d,%d)", tu.prevWidth, tu.prevHeight, captured.PrevWidth, captured.PrevHeight)
	}
	if !tu.hasRendered {
		t.Fatal("hasRendered = false after restoring non-empty content; next render would be a full redraw")
	}
}

// TestTUIRestoreRenderStateBlanksImageLines proves restore blanks image lines and
// clears the Kitty image-id set, mirroring upstream: those images are no longer on
// the terminal after a switch, so the differential must not assume they persist.
func TestTUIRestoreRenderStateBlanksImageLines(t *testing.T) {
	imageLine := EncodeKitty("Zm9v", 2, 1, AllocateImageID())
	if !IsImageLine(imageLine) {
		t.Fatalf("test setup: EncodeKitty output is not detected as an image line")
	}
	var out bytes.Buffer
	tu := NewWithOutput(&out, 40, 10)
	tu.previousKittyImageIDs = map[int]struct{}{7: {}}
	tu.RestoreRenderState(TUIRenderState{PrevLines: []string{"text", imageLine, "more"}})

	tu.mu.Lock()
	defer tu.mu.Unlock()
	if tu.prevLines[1] != "" {
		t.Fatalf("image line was not blanked on restore: %q", tu.prevLines[1])
	}
	if tu.prevLines[0] != "text" || tu.prevLines[2] != "more" {
		t.Fatalf("non-image lines were altered: %v", tu.prevLines)
	}
	if len(tu.previousKittyImageIDs) != 0 {
		t.Fatalf("Kitty image-id set not cleared on restore: %v", tu.previousKittyImageIDs)
	}
}

// TestTUIStopPreserveScreenOmitsFinalNewline proves the preserve-screen stop used
// by a live tui-mode switch does not emit the end-of-session cursor-park newline
// that plain Stop() writes, so a swap produces no final-session output.
func TestTUIStopPreserveScreenOmitsFinalNewline(t *testing.T) {
	var plain bytes.Buffer
	a := NewWithOutput(&plain, 40, 10)
	a.Add(NewText("line one"))
	a.Add(NewText("line two"))
	a.Render()
	plain.Reset()
	a.Stop()
	if plain.Len() == 0 {
		t.Fatal("plain Stop emitted nothing; test cannot distinguish preserve behavior")
	}

	var preserved bytes.Buffer
	b := NewWithOutput(&preserved, 40, 10)
	b.Add(NewText("line one"))
	b.Add(NewText("line two"))
	b.Render()
	preserved.Reset()
	b.StopWithOptions(StopOptions{PreserveScreen: true})
	if strings.Contains(preserved.String(), "\r\n") {
		t.Fatalf("preserve-screen stop emitted a final newline: %q", preserved.String())
	}
}
