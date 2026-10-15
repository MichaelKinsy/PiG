package tui

// pi: packages/tui/src/tui.ts (renderFakeCursor, resolveFakeCursors), packages/tui/test/tui-render.test.ts "TUI fake cursor", tui-alt-screen.test.ts

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// renderedFrame returns the bytes a main-screen frame wrote, with the hardware cursor setting applied and the frame the setting
// requested run, as Pi's next-tick render does.
func renderedFrame(t *testing.T, showHardwareCursor bool, setup func(ui *TuiMainScreen)) string {
	t.Helper()
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 5)
	var pending func()
	ui.afterFunc = func(_ time.Duration, fn func()) stoppableTimer {
		pending = fn
		return stoppedOracleTimer{}
	}
	if showHardwareCursor {
		ui.SetShowHardwareCursor(true)
	}
	setup(ui)
	ui.Render()
	if pending != nil {
		run := pending
		pending = nil
		run()
	}
	// Pi's terminal.showCursor/hideCursor bypass the write log the upstream tests read, so the trailing cursor visibility sequence is not part of the writes.
	return strings.TrimSuffix(strings.TrimSuffix(out.String(), "\x1b[?25h"), "\x1b[?25l")
}

func TestResolveFakeCursorsTable(t *testing.T) {
	marker, start, end := widthx.CursorMarker, widthx.FakeCursorStart, widthx.FakeCursorEnd
	for _, tc := range []struct {
		name     string
		line     string
		hardware bool
		want     string
	}{
		{"reverse video without the hardware cursor", marker + start + "a" + end + "bc", false, marker + "\x1b[7ma\x1b[27mbc"},
		{"focused cursor dropped with the hardware cursor", "> " + marker + start + "a" + end + "bc", true, "> " + marker + "abc"},
		{"unfocused cursor kept with the hardware cursor", "> " + start + "a" + end + "bc", true, "> \x1b[7ma\x1b[27mbc"},
		{"truncation dropped the end marker", "ab" + marker + start + "c", true, "ab" + marker + "c"},
		{"truncation without the hardware cursor", "ab" + marker + start + "c", false, "ab" + marker + "\x1b[7mc"},
		{"only the first focused cursor is dropped", marker + start + "a" + end + marker + start + "b" + end, true, marker + "a" + marker + "\x1b[7mb\x1b[27m"},
		{"a wide grapheme cursor", marker + start + "日" + end + "本", true, marker + "日本"},
		{"a line without markers", "plain\x1b[31mred", true, "plain\x1b[31mred"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := widthx.ResolveFakeCursors([]string{tc.line}, tc.hardware)[0]; got != tc.want {
				t.Fatalf("ResolveFakeCursors(%q, %v) = %q, want %q", tc.line, tc.hardware, got, tc.want)
			}
		})
	}
}

// Pi's resolveFakeCursors maps every line to a new array; Pig returns the frame itself when no line holds a marker and a copy otherwise, and
// never modifies a borrowed frame.
func TestResolveFakeCursorsDoesNotModifyOrCopyNeedlessly(t *testing.T) {
	plain := []string{"a", "b"}
	if got := widthx.ResolveFakeCursors(plain, true); &got[0] != &plain[0] {
		t.Fatal("a frame without markers is returned as given")
	}
	if allocs := testing.AllocsPerRun(100, func() { widthx.ResolveFakeCursors(plain, true) }); allocs != 0 {
		t.Fatalf("a frame without markers allocates %v times, want 0", allocs)
	}
	marked := []string{"a", widthx.RenderFakeCursor("b")}
	before := slices.Clone(marked)
	got := widthx.ResolveFakeCursors(marked, false)
	if !slices.Equal(marked, before) || got[1] != "\x1b[7mb\x1b[27m" || got[0] != "a" {
		t.Fatalf("marked frame = %q (argument %q), want a copy resolved to reverse video", got, marked)
	}
}

func TestMainScreenDrawsTheFocusedFakeCursorAndOmitsItWhenTheHardwareCursorIsShown(t *testing.T) {
	// tui-render.test.ts: "draws the focused fake cursor in reverse video and omits it when the hardware cursor is shown"
	build := func(ui *TuiMainScreen) {
		input := NewInput(InputOptions{})
		input.SetValue("abc")
		input.SetFocused(true)
		ui.Add(input)
	}
	if got := renderedFrame(t, false, build); !strings.Contains(got, "> \x1b[7ma\x1b[27mbc") {
		t.Fatalf("fake cursor missing from %q", got)
	}
	got := renderedFrame(t, true, build)
	if strings.Contains(got, "\x1b[7m") || strings.Contains(got, "\x1b_pi:") {
		t.Fatalf("hardware cursor shown, but the frame still holds the fake cursor or a marker: %q", got)
	}
	if !strings.Contains(got, "> abc") {
		t.Fatalf("the cursor text must stay: %q", got)
	}
}

func TestMainScreenKeepsTheFakeCursorOfAnUnfocusedInput(t *testing.T) {
	// tui-render.test.ts: "keeps the fake cursor of an unfocused input when the hardware cursor is shown"
	got := renderedFrame(t, true, func(ui *TuiMainScreen) {
		input := NewInput(InputOptions{})
		input.SetValue("abc")
		ui.Add(input)
	})
	if !strings.Contains(got, "> \x1b[7ma\x1b[27mbc") {
		t.Fatalf("unfocused fake cursor missing from %q", got)
	}
}

func TestMainScreenOmitsAFocusedFakeCursorWhoseEndMarkerTruncationDropped(t *testing.T) {
	// tui-render.test.ts: "omits the focused fake cursor when truncation dropped its end marker"
	got := renderedFrame(t, true, func(ui *TuiMainScreen) {
		ui.Add(&oracleLines{lines: []string{widthx.SliceByColumn("ab"+widthx.CursorMarker+widthx.RenderFakeCursor("c")+"def", 0, 3, true)}})
	})
	if strings.Contains(got, "\x1b[7m") {
		t.Fatalf("truncated focused fake cursor still drawn: %q", got)
	}
	if !strings.HasSuffix(got, "\x1b[3G") {
		t.Fatalf("the hardware cursor belongs at column 3: %q", got)
	}
}

func TestMainScreenKeepsTheCursorWhenAnOverlayEndsDirectlyLeftOfIt(t *testing.T) {
	// tui-render.test.ts: "keeps the cursor when an overlay ends directly left of it"
	for _, hardware := range []bool{false, true} {
		got := renderedFrame(t, hardware, func(ui *TuiMainScreen) {
			ui.Add(&oracleLines{lines: []string{"abc" + widthx.CursorMarker + widthx.RenderFakeCursor("d") + "ef"}})
			ui.ShowOverlay(&oracleLines{lines: []string{"XXX"}}, OverlaySpec{Row: &OverlayValue{Value: 0}, Col: &OverlayValue{Value: 0}, Width: &OverlayValue{Value: 3}}.Options())
		})
		if !strings.Contains(got, "XXX") {
			t.Fatalf("hardware=%v: overlay missing from %q", hardware, got)
		}
		if has := strings.Contains(got, "\x1b[7md"); has == hardware {
			t.Fatalf("hardware=%v: reverse-video cursor present = %v in %q", hardware, has, got)
		}
		if !strings.HasSuffix(got, "\x1b[4G") {
			t.Fatalf("hardware=%v: the hardware cursor belongs at column 4: %q", hardware, got)
		}
	}
}

// The alt screen resolves fake cursors before highlighting: the selection's inverse reapplies after the cursor cell ends
// (tui-alt-screen.test.ts "keeps the selection highlight after a fake cursor cell").
func TestAltScreenKeepsTheSelectionHighlightAfterAFakeCursorCell(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
	h.tui.Add(NewText("> " + widthx.RenderFakeCursor("a") + "bcd"))
	h.start()
	h.send("\x1b[<0;1;1M", "\x1b[<32;6;1M", "\x1b[<3;6;1m")
	if !h.wrote("\x1b[27m\x1b[7mbcd") {
		t.Fatalf("selection inverse must be reapplied after the fake cursor ends: %q", h.out.String())
	}
}

// The document the alt screen prints on the main screen when it stops has its fake cursors resolved, as Pi 1.2.0
// tui-alt-screen.ts afterTerminalStop does with resolveFakeCursors(this.render(width)): no marker reaches the terminal.
func TestAltScreenStopPrintsTheDocumentWithItsFakeCursorResolved(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
	h.tui.Add(NewText("> " + widthx.RenderFakeCursor("a") + "bcd"))
	h.start()
	h.render()
	h.out.Reset()
	h.tui.Stop()
	document := h.out.String()
	if !strings.Contains(document, "> \x1b[7ma\x1b[27mbcd") || strings.Contains(document, widthx.FakeCursorStart) || strings.Contains(document, widthx.FakeCursorEnd) {
		t.Fatalf("stop document = %q, want the cursor cell in reverse video and no marker", document)
	}
}

// An overlay component's fake cursor is resolved too (tui.ts compositeOverlays: resolveFakeCursors(component.render(width))), so no marker
// reaches the terminal.
func TestMainScreenResolvesTheFakeCursorOfAnOverlayComponent(t *testing.T) {
	got := renderedFrame(t, false, func(ui *TuiMainScreen) {
		ui.Add(&oracleLines{lines: []string{"base"}})
		ui.ShowOverlay(&oracleLines{lines: []string{"x" + widthx.RenderFakeCursor("z") + "y"}}, OverlaySpec{Row: &OverlayValue{Value: 0}, Col: &OverlayValue{Value: 6}, Width: &OverlayValue{Value: 3}}.Options())
	})
	if !strings.Contains(got, "\x1b[7mz") || strings.Contains(got, "\x1b_pi:") {
		t.Fatalf("overlay fake cursor not resolved: %q", got)
	}
}
