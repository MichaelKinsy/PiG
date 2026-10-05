package tui

import (
	"bytes"
	"strings"
	"testing"
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

// TestAltScreenWezTermClearsRowsBeforeKittyImages ports upstream
// clearRowsBeforeKittyImages: in WezTerm a frame that places Kitty images
// erases every repainted row first and then draws rows without EL, so a later
// EL cannot erase image cells drawn earlier in the same frame.
func TestAltScreenWezTermClearsRowsBeforeKittyImages(t *testing.T) {
	t.Setenv("WEZTERM_PANE", "7")
	t.Setenv("TERM_PROGRAM", "")
	frame := renderKittyFrame(t)
	assertWezTermKittyFrame(t, frame)

	t.Setenv("WEZTERM_PANE", "")
	t.Setenv("TERM_PROGRAM", "WezTerm")
	assertWezTermKittyFrame(t, renderKittyFrame(t))
}

// TestAltScreenOtherTerminalsInterleaveEraseWithKittyImages pins the
// unchanged path: outside WezTerm each row is erased as it is drawn.
func TestAltScreenOtherTerminalsInterleaveEraseWithKittyImages(t *testing.T) {
	t.Setenv("WEZTERM_PANE", "")
	t.Setenv("TERM_PROGRAM", "ghostty")
	frame := renderKittyFrame(t)
	for _, row := range []string{"\x1b[1;1H\x1b[2Ktext", "\x1b[2;1H\x1b[2K\x1b_G", "\x1b[3;1H\x1b[2Ktail"} {
		if !strings.Contains(frame, row) {
			t.Fatalf("non-WezTerm Kitty frame should erase row %q as it draws it:\n got %q", row, frame)
		}
	}
	if strings.Contains(frame, wezTermRowClears) {
		t.Fatalf("non-WezTerm Kitty frame must not clear rows ahead of drawing:\n got %q", frame)
	}
}

const wezTermRowClears = "\x1b[1;1H\x1b[2K\x1b[2;1H\x1b[2K\x1b[3;1H\x1b[2K"

// assertWezTermKittyFrame checks that every row is cleared before the first
// row is drawn and that no EL follows once drawing starts.
func assertWezTermKittyFrame(t *testing.T, frame string) {
	t.Helper()
	_, draws, ok := strings.Cut(frame, wezTermRowClears)
	if !ok || !strings.HasPrefix(draws, "\x1b[1;1Htext") ||
		!strings.Contains(draws, "\x1b[2;1H\x1b_G") || !strings.Contains(draws, "\x1b[3;1Htail") {
		t.Fatalf("WezTerm Kitty frame should clear all rows, then draw them:\n got %q", frame)
	}
	if strings.Contains(draws, "\x1b[2K") {
		t.Fatalf("WezTerm Kitty frame must not erase rows while drawing:\n got %q", frame)
	}
	// 1.0.1 drawKittyImagesLast: image rows are drawn after every text row.
	if strings.Index(draws, "\x1b[2;1H\x1b_G") < strings.Index(draws, "\x1b[3;1Htail") {
		t.Fatalf("WezTerm Kitty frame should draw images after text rows:\n got %q", frame)
	}
}

// .upstream/v1.0.1/packages/tui/test/tui-alt-screen.test.ts:1065 (#10319): a write to a row an unchanged image covers
// erases its cells in WezTerm, so the images are placed again after the row is written, without re-uploading them.
func TestAltScreenRedrawsWezTermKittyImagesAfterWritesToCoveredRows(t *testing.T) {
	t.Setenv("WEZTERM_PANE", "1")
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
