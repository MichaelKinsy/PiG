package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// renderKittyFrame paints one full alt-screen frame whose second row is a
// Kitty image line and returns the bytes between the synchronized-output
// markers.
func renderKittyFrame(t *testing.T) string {
	t.Helper()
	prev := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(prev) })
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true})
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 20, 3, TuiAltScreenOptions{})
	tui.SetLayoutRoot(&stubComponent{lines: []string{"text", "\x1b_Gi=91,a=T,f=100;DATA\x1b\\", "tail"}})
	tui.Start()
	out.Reset()
	tui.previousScreen = nil
	tui.doRender()
	frame := out.String()
	start := strings.Index(frame, altBeginSynchronizedOutput)
	end := strings.Index(frame, altEndSynchronizedOutput)
	if start < 0 || end < start {
		t.Fatalf("no synchronized frame in %q", frame)
	}
	return frame[start+len(altBeginSynchronizedOutput) : end]
}

// packages/tui/test/tui-alt-screen.test.ts "does not leak cursor markers while selecting after the caret" (#9257, #9332, Pi 25490ea49):
// selection slicing must not duplicate cursor markers.
func TestAltScreenDoesNotLeakCursorMarkersWhileSelectingAfterTheCaret(t *testing.T) {
	copyOnSelect := false
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{CopyOnSelect: &copyOnSelect})
	h.tui.Add(NewText("ab" + widthx.CursorMarker + "cdefghij"))
	h.start()
	h.render()
	h.out.Reset()
	h.send("\x1b[<0;5;1M", "\x1b[<32;7;1M")
	output := h.out.String()
	if !strings.Contains(output, "\x1b[7m") {
		t.Fatalf("dragged text should be highlighted: %q", output)
	}
	if strings.Contains(output, widthx.CursorMarker) {
		t.Fatalf("internal cursor markers must not reach the terminal: %q", output)
	}
	h.send("\x1b[<0;7;1m")
}

// TestAltScreenClearsRowsBeforeKittyImages ports upstream clearRowsBeforeKittyImages and the Pi 4622edb79 test
// "draws Kitty images after clearing the rows they cover" (#11835): on every terminal, whatever its environment, a
// frame that places Kitty images erases every repainted row first and then draws rows without EL, so a later EL cannot
// erase image cells drawn earlier in the same frame.
func TestAltScreenClearsRowsBeforeKittyImages(t *testing.T) {
	for _, env := range []map[string]string{
		{"WEZTERM_PANE": "7", "TERM_PROGRAM": ""},
		{"WEZTERM_PANE": "", "TERM_PROGRAM": "WezTerm"},
		{"WEZTERM_PANE": "", "TERM_PROGRAM": "ghostty"},
		{"WEZTERM_PANE": "", "TERM_PROGRAM": ""},
	} {
		for k, v := range env {
			t.Setenv(k, v)
		}
		assertKittyFrameDrawsImagesLast(t, renderKittyFrame(t))
	}
}

const kittyRowClears = "\x1b[1;1H\x1b[2K\x1b[2;1H\x1b[2K\x1b[3;1H\x1b[2K"

// assertKittyFrameDrawsImagesLast checks that every row is cleared before the first
// row is drawn and that no EL follows once drawing starts.
func assertKittyFrameDrawsImagesLast(t *testing.T, frame string) {
	t.Helper()
	_, draws, ok := strings.Cut(frame, kittyRowClears)
	if !ok || !strings.HasPrefix(draws, "\x1b[1;1Htext") ||
		!strings.Contains(draws, "\x1b[2;1H\x1b_G") || !strings.Contains(draws, "\x1b[3;1Htail") {
		t.Fatalf("Kitty frame should clear all rows, then draw them:\n got %q", frame)
	}
	if strings.Contains(draws, "\x1b[2K") {
		t.Fatalf("Kitty frame must not erase rows while drawing:\n got %q", frame)
	}
	// 1.0.1 drawKittyImagesLast: image rows are drawn after every text row.
	if strings.Index(draws, "\x1b[2;1H\x1b_G") < strings.Index(draws, "\x1b[3;1Htail") {
		t.Fatalf("Kitty frame should draw images after text rows:\n got %q", frame)
	}
}

// packages/tui/test/tui-alt-screen.test.ts "redraws Kitty images after writes to covered rows" (#10319): a write to a row an
// unchanged image covers erases its cells in terminals that store images in cells, so the images are placed again after
// the row is written, without re-uploading them. Pi 4622edb79 drops the WezTerm environment from the test.
func TestAltScreenRedrawsKittyImagesAfterWritesToCoveredRows(t *testing.T) {
	prev := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(prev) })
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	const imageID = 10319
	imageLine := EncodeKitty("AAAA", 2, 3, imageID, false)
	RegisterKittyImageMetadata(KittyImageMetadata{ImageID: imageID, Columns: 2, Rows: 3, WidthPx: 100, HeightPx: 100})
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 20, 4, TuiAltScreenOptions{})
	root := &stubComponent{lines: []string{imageLine, "", "", "after"}}
	tui.SetLayoutRoot(root)
	tui.Start()
	tui.doRender()
	out.Reset()

	root.lines = []string{imageLine, "changed", "", "after"}
	tui.doRender()
	redraw := out.String()
	placement := strings.Index(redraw, "\x1b_Ga=p,q=2")
	if !strings.Contains(redraw, "\x1b_Ga=d,d=a,q=2\x1b\\") || placement < 0 || placement < strings.Index(redraw, "changed") || strings.Contains(redraw, "\x1b_Ga=T") {
		t.Fatalf("redraw = %q", redraw)
	}
}

// packages/tui/test/tui-alt-screen.test.ts "draws Kitty images after clearing the rows they cover" (#11835, Pi 4622edb79):
// Orca's xterm.js image addon stores images in cells, so clearing the covered rows after the placement erased all but the
// first image row.
func TestAltScreenDrawsKittyImagesAfterClearingTheRowsTheyCover(t *testing.T) {
	prev := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(prev) })
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	const imageID = 11835
	imageLine := EncodeKitty("AAAA", 2, 3, imageID, false)
	RegisterKittyImageMetadata(KittyImageMetadata{ImageID: imageID, Columns: 2, Rows: 3, WidthPx: 100, HeightPx: 100})
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 20, 5, TuiAltScreenOptions{})
	tui.SetLayoutRoot(&stubComponent{lines: []string{"before", imageLine, "", "", "after"}})
	tui.Start()
	tui.doRender()
	writes := out.String()
	transmit := strings.Index(writes, "\x1b_Ga=T")
	if transmit < 0 || transmit <= strings.LastIndex(writes, "\x1b[4;1H\x1b[2K") || transmit <= strings.Index(writes, "after") {
		t.Fatalf("the image must be transmitted after the covered rows are cleared and written: %q", writes)
	}
}
