package tui

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/tui/termsim"
)

// Ports Pi 787005a40e (#12109, issue #11584) packages/tui/test/overlay-terminal-image.test.ts: an overlay left a hole where it
// intersected a terminal image, and a Kitty image covered the overlay's text on the rows below its image line. Each case keeps
// Pi's inputs and assertions and adds the bytes Pi 787005a40e writes for the same frames (its TuiMainScreen and TuiAltScreen
// on a recording terminal, 20x6), which the frame must equal.

// overlayImageScreen records the bytes of each frame and replays them on a 20x6 terminal, as the test's RecordingTerminal.
type overlayImageScreen struct {
	t      *testing.T
	out    bytes.Buffer
	grid   *termsim.Grid
	render func()
}

func newOverlayImageScreen(t *testing.T, images ImageProtocol) *overlayImageScreen {
	t.Helper()
	// The terminal Pi's frames were recorded for: no multiplexer and no WezTerm, whose image and mouse sequences differ.
	for _, name := range []string{"TMUX", "ZELLIJ", "STY", "WEZTERM_PANE", "TERM_PROGRAM"} {
		t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
	t.Setenv("TERM", "xterm-256color")
	SetCapabilities(TerminalCapabilities{Images: images, TrueColor: true, Hyperlinks: true})
	t.Cleanup(ResetCapabilitiesCache)
	resetKittyRegistry(t)
	t.Cleanup(func() { resetKittyRegistry(t) })
	return &overlayImageScreen{t: t, grid: termsim.New(6, 20)}
}

func newOverlayImageMainScreen(t *testing.T, images ImageProtocol, lines ...string) (*TuiMainScreen, *overlayImageScreen) {
	t.Helper()
	screen := newOverlayImageScreen(t, images)
	ui := newManualRenderTUI(&screen.out, 20, 6)
	ui.Add(&stubComponent{lines: lines})
	screen.render = ui.Render
	return ui, screen
}

// newOverlayImageAltScreen starts a fullscreen TUI over lines with the image of metadata registered, as an image the Image
// component rendered; Start draws the first frame.
func newOverlayImageAltScreen(t *testing.T, metadata KittyImageMetadata, lines ...string) (*TuiAltScreen, *overlayImageScreen) {
	t.Helper()
	screen := newOverlayImageScreen(t, ImageProtocolKitty)
	RegisterKittyImageMetadata(metadata)
	ui := newAltScreenForTest(&screen.out, 20, 6, TuiAltScreenOptions{})
	t.Cleanup(func() { ui.StopWithOptions(StopOptions{PreserveScreen: true}) })
	ui.SetLayoutRoot(&stubComponent{lines: lines})
	ui.Start()
	screen.grid.Write(screen.out.Bytes())
	screen.render = ui.Render
	return ui, screen
}

// frame renders once and returns the bytes the frame wrote, which must be Pi's.
func (s *overlayImageScreen) frame(pi string) string {
	s.t.Helper()
	mark := s.out.Len()
	s.render()
	writes := s.out.String()[mark:]
	s.grid.WriteString(writes)
	if writes != pi {
		s.t.Errorf("frame bytes differ from Pi's\n got %q\nwant %q", writes, pi)
	}
	return writes
}

// row is a row of the terminal's viewport with its trailing spaces trimmed.
func (s *overlayImageScreen) row(index int) string {
	return strings.Split(s.grid.String(), "\n")[index]
}

// from is row without its first cells, as viewport[row].slice(cells).trimEnd() of an ASCII row.
func (s *overlayImageScreen) from(index, cells int) string {
	row := s.row(index)
	if len(row) < cells {
		return ""
	}
	return row[cells:]
}

func overlayAt(row, col, width int) OverlayOptions {
	return OverlaySpec{Row: &OverlayValue{Value: float64(row)}, Col: &OverlayValue{Value: float64(col)}, Width: &OverlayValue{Value: float64(width)}}.Options()
}

var overlayImagePanel = &stubComponent{lines: []string{"PANEL"}}

// overlay-terminal-image.test.ts:34 "deletes a Kitty image under an overlay and redraws it after the overlay closes".
func TestOverlayOverAKittyImageDeletesTheImageAndRedrawsItAfterTheOverlayCloses(t *testing.T) {
	image := EncodeKitty("AAAA", 4, 4, 11584, false)
	ui, screen := newOverlayImageMainScreen(t, ImageProtocolKitty, "top", image, "", "", "", "bottom")
	screen.frame("\x1b[?2026htop\x1b[0m\x1b]8;;\x07\r\n\r\n\r\n\r\n\x1b[3A\x1b_Ga=T,f=100,q=2,C=1,c=4,r=4,i=11584;AAAA\x1b\\\x1b[3B\r\nbottom\x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")

	// Row 3 is the third image row, below the image line itself.
	overlay := ui.ShowOverlay(overlayImagePanel, overlayAt(3, 2, 8))
	writes := screen.frame("\x1b[?2026h\x1b_Ga=d,d=I,i=11584,q=2\x1b\\\x1b[4A\r\x1b[2K\x1b[0m\x1b]8;;\x07\r\n\x1b[2K\x1b[0m\x1b]8;;\x07\r\n\x1b[2K  \x1b[0m\x1b]8;;\x07PANEL   \x1b[0m\x1b]8;;\x07          \x1b[0m\x1b]8;;\x07\r\n\x1b[2K\x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")
	if !strings.Contains(writes, DeleteKittyImage(11584)) {
		t.Errorf("the covered image is not deleted: %q", writes)
	}
	if strings.Contains(writes, kittyTransmitPrefix) {
		t.Errorf("the covered image is drawn again: %q", writes)
	}
	if got := screen.row(3); got != "  PANEL" {
		t.Errorf("row 3 = %q, want %q", got, "  PANEL")
	}

	overlay.Hide()
	writes = screen.frame("\x1b[?2026h\x1b[3A\r\x1b[2K\r\n\x1b[2K\r\n\x1b[2K\r\n\x1b[2K\x1b[3A\x1b_Ga=T,f=100,q=2,C=1,c=4,r=4,i=11584;AAAA\x1b\\\x1b[3B\x1b[?2026l\x1b[?25l")
	if !strings.Contains(writes, image) {
		t.Errorf("the image is not drawn again once uncovered: %q", writes)
	}
}

// overlay-terminal-image.test.ts:62 "keeps images whose rows the overlay does not touch".
func TestOverlayKeepsAnImageWhoseRowsItDoesNotTouch(t *testing.T) {
	image := EncodeKitty("AAAA", 4, 2, 11585, false)
	ui, screen := newOverlayImageMainScreen(t, ImageProtocolKitty, image, "", "text", "", "", "")
	screen.frame("\x1b[?2026h\r\n\x1b[1A\x1b_Ga=T,f=100,q=2,C=1,c=4,r=2,i=11585;AAAA\x1b\\\x1b[1B\r\ntext\x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")

	ui.ShowOverlay(overlayImagePanel, overlayAt(4, 0, 8))
	writes := screen.frame("\x1b[?2026h\x1b[1A\r\x1b[2K\x1b[0m\x1b]8;;\x07PANEL   \x1b[0m\x1b]8;;\x07            \x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")
	if strings.Contains(writes, DeleteKittyImage(11585)) {
		t.Errorf("the image the overlay does not touch is deleted: %q", writes)
	}
	if got := screen.row(4); got != "PANEL" {
		t.Errorf("row 4 = %q, want %q", got, "PANEL")
	}
}

// overlay-terminal-image.test.ts:83 "keeps an image beside an overlay that shares its rows".
func TestOverlayKeepsAnImageBesideItThatSharesItsRows(t *testing.T) {
	image := EncodeKitty("AAAA", 4, 4, 11588, false)
	ui, screen := newOverlayImageMainScreen(t, ImageProtocolKitty, "top", image, "", "", "", "bottom")
	screen.frame("\x1b[?2026htop\x1b[0m\x1b]8;;\x07\r\n\r\n\r\n\r\n\x1b[3A\x1b_Ga=T,f=100,q=2,C=1,c=4,r=4,i=11588;AAAA\x1b\\\x1b[3B\r\nbottom\x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")

	// A sidebar at columns 10-17 next to the image at columns 0-3.
	ui.ShowOverlay(&stubComponent{lines: []string{"SIDE", "SIDE", "SIDE", "SIDE", "SIDE", "SIDE"}}, overlayAt(0, 10, 8))
	writes := screen.frame("\x1b[?2026h\x1b_Ga=d,d=I,i=11588,q=2\x1b\\\x1b[5A\r\x1b[2Ktop       \x1b[0m\x1b]8;;\x07SIDE    \x1b[0m\x1b]8;;\x07  \x1b[0m\x1b]8;;\x07\r\n\x1b[2K\x1b_Ga=T,f=100,q=2,C=1,c=4,r=4,i=11588;AAAA\x1b\\\r\n\x1b[2K          \x1b[0m\x1b]8;;\x07SIDE    \x1b[0m\x1b]8;;\x07  \x1b[0m\x1b]8;;\x07\r\n\x1b[2K          \x1b[0m\x1b]8;;\x07SIDE    \x1b[0m\x1b]8;;\x07  \x1b[0m\x1b]8;;\x07\r\n\x1b[2K          \x1b[0m\x1b]8;;\x07SIDE    \x1b[0m\x1b]8;;\x07  \x1b[0m\x1b]8;;\x07\r\n\x1b[2Kbottom    \x1b[0m\x1b]8;;\x07SIDE    \x1b[0m\x1b]8;;\x07  \x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")
	// The main screen redraws an image whenever a row it covers changes, so it may delete it first.
	if strings.LastIndex(writes, image) <= strings.LastIndex(writes, DeleteKittyImage(11588)) {
		t.Errorf("the image beside the sidebar is not drawn: %q", writes)
	}
	if got := screen.from(2, 10); got != "SIDE" {
		t.Errorf("row 2 from column 10 = %q, want %q: rows below the image line show the sidebar", got, "SIDE")
	}
	// The image line itself still passes through, so the sidebar skips that row.
	if got := strings.TrimSpace(screen.row(1)); got != "" {
		t.Errorf("row 1 = %q, want it empty", got)
	}
}

// overlay-terminal-image.test.ts:116 "keeps a fullscreen image beside an overlay that shares its rows".
func TestFullscreenOverlayKeepsAnImageBesideItThatSharesItsRows(t *testing.T) {
	const imageID = 11591
	image := EncodeKitty("AAAA", 4, 3, imageID, false)
	metadata := KittyImageMetadata{ImageID: imageID, Columns: 4, Rows: 3, WidthPx: 100, HeightPx: 100}
	ui, screen := newOverlayImageAltScreen(t, metadata, "top", image, "", "", "after", "")

	ui.ShowOverlay(&stubComponent{lines: []string{"SIDE", "SIDE", "SIDE", "SIDE"}}, overlayAt(1, 10, 8))
	writes := screen.frame("\x1b[?2026h\x1b_Ga=d,d=a,q=2\x1b\\\x1b[1;1H\x1b[2K\x1b[2;1H\x1b[2K\x1b[3;1H\x1b[2K\x1b[4;1H\x1b[2K\x1b[5;1H\x1b[2K\x1b[6;1H\x1b[2K\x1b[1;1Htop\x1b[0m\x1b]8;;\x07\x1b[3;1H          \x1b[0m\x1b]8;;\x07SIDE    \x1b[0m\x1b]8;;\x07  \x1b[0m\x1b]8;;\x07\x1b[4;1H          \x1b[0m\x1b]8;;\x07SIDE    \x1b[0m\x1b]8;;\x07  \x1b[0m\x1b]8;;\x07\x1b[5;1Hafter     \x1b[0m\x1b]8;;\x07SIDE    \x1b[0m\x1b]8;;\x07  \x1b[0m\x1b]8;;\x07\x1b[6;1H\x1b[0m\x1b]8;;\x07\x1b[2;1H\x1b_Ga=p,q=2,C=1,c=4,r=3,i=11591\x1b\\\x1b[?25l\x1b[?2026l")
	if !strings.Contains(writes, "\x1b_Ga=p,q=2") {
		t.Errorf("the image is not placed again after the redraw: %q", writes)
	}
	if !strings.Contains(writes, "i=11591") {
		t.Errorf("the placement does not name the image: %q", writes)
	}
	if got := screen.from(2, 10); got != "SIDE" {
		t.Errorf("row 2 from column 10 = %q, want %q", got, "SIDE")
	}
}

// overlay-terminal-image.test.ts:140 "hides an image of unknown width under any overlay sharing its rows".
func TestOverlayHidesAnImageOfUnknownWidthWhenItSharesItsRows(t *testing.T) {
	// No `c=` and no registered metadata: the width cannot be read.
	image := EncodeKitty("AAAA", 0, 2, 11589, false)
	ui, screen := newOverlayImageMainScreen(t, ImageProtocolKitty, "top", image, "", "", "", "")
	screen.frame("\x1b[?2026htop\x1b[0m\x1b]8;;\x07\r\n\r\n\x1b[1A\x1b_Ga=T,f=100,q=2,C=1,r=2,i=11589;AAAA\x1b\\\x1b[1B\r\n\x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")

	ui.ShowOverlay(overlayImagePanel, overlayAt(2, 15, 5))
	writes := screen.frame("\x1b[?2026h\x1b_Ga=d,d=I,i=11589,q=2\x1b\\\x1b[4A\r\x1b[2K\x1b[0m\x1b]8;;\x07\r\n\x1b[2K               \x1b[0m\x1b]8;;\x07PANEL\x1b[0m\x1b]8;;\x07\x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")
	if !strings.Contains(writes, DeleteKittyImage(11589)) {
		t.Errorf("the image of unknown width is not deleted: %q", writes)
	}
}

// overlay-terminal-image.test.ts:161 "reads the cells an image line covers".
func TestGetImageLineExtentReadsTheCellsAnImageLineCovers(t *testing.T) {
	resetKittyRegistry(t)
	kitty := "  " + EncodeKitty("AAAA", 6, 3, 11590, false)
	if got, ok := getImageLineExtent(kitty); !ok || got != (imageLineExtent{firstRow: 0, lastRow: 2, startCol: 2, endCol: 8}) {
		t.Errorf("Kitty line extent = %+v, %v", got, ok)
	}
	iterm2 := "\x1b[2A" + EncodeITerm2("AAAA", ITerm2Options{Width: 5, Height: "auto"})
	if got, ok := getImageLineExtent(iterm2); !ok || got != (imageLineExtent{firstRow: -2, lastRow: 0, startCol: 0, endCol: 5}) {
		t.Errorf("iTerm2 line extent = %+v, %v", got, ok)
	}
	autoWidth := EncodeITerm2("AAAA", ITerm2Options{Width: "50%", Height: "auto"})
	if got, ok := getImageLineExtent(autoWidth); !ok || got.endCol != imageExtentUnbounded {
		t.Errorf("percent width extent = %+v, %v, want an unbounded endCol", got, ok)
	}
	if got, ok := getImageLineExtent("plain text"); ok {
		t.Errorf("plain text has the extent %+v", got)
	}
}

// overlay-terminal-image.test.ts:171 "hides an image inside an overlay when a later overlay covers it".
func TestLaterOverlayHidesAnImageInsideAnEarlierOverlay(t *testing.T) {
	image := EncodeKitty("AAAA", 4, 3, 11587, false)
	ui, screen := newOverlayImageMainScreen(t, ImageProtocolKitty, "", "", "", "", "", "")
	ui.ShowOverlay(&stubComponent{lines: []string{image, "", ""}}, overlayAt(0, 0, 10))
	writes := screen.frame("\x1b[?2026h\x1b[0m\x1b]8;;\x07\x1b_Ga=T,f=100,q=2,C=1,c=4,r=3,i=11587;AAAA\x1b\\          \x1b[0m\x1b]8;;\x07          \r\n\x1b[0m\x1b]8;;\x07          \x1b[0m\x1b]8;;\x07          \x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07          \x1b[0m\x1b]8;;\x07          \x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")
	if !strings.Contains(writes, image) {
		t.Errorf("the overlay's own image is not drawn: %q", writes)
	}

	ui.ShowOverlay(overlayImagePanel, overlayAt(2, 0, 8))
	writes = screen.frame("\x1b[?2026h\x1b_Ga=d,d=I,i=11587,q=2\x1b\\\x1b[5A\r\x1b[2K\x1b[0m\x1b]8;;\x07          \x1b[0m\x1b]8;;\x07          \x1b[0m\x1b]8;;\x07\r\n\x1b[2K\x1b[0m\x1b]8;;\x07          \x1b[0m\x1b]8;;\x07          \x1b[0m\x1b]8;;\x07\r\n\x1b[2K\x1b[0m\x1b]8;;\x07PANEL   \x1b[0m\x1b]8;;\x07  \x1b[0m\x1b]8;;\x07          \x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")
	if !strings.Contains(writes, DeleteKittyImage(11587)) {
		t.Errorf("the image under the top overlay is not deleted: %q", writes)
	}
	if got := screen.row(2); got != "PANEL" {
		t.Errorf("row 2 = %q, want %q", got, "PANEL")
	}
}

// overlay-terminal-image.test.ts:194 "hides an iTerm2 image when an overlay covers rows above its image line".
func TestOverlayHidesAnITerm2ImageWhenItCoversRowsAboveItsImageLine(t *testing.T) {
	image := "\x1b[2A" + EncodeITerm2("AAAA", ITerm2Options{Width: 4, Height: "auto"})
	ui, screen := newOverlayImageMainScreen(t, ImageProtocolITerm2, "top", "", "", image, "", "bottom")
	screen.frame("\x1b[?2026htop\x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07\r\n\x1b[2A\x1b]1337;File=inline=1;size=3;width=4;height=auto:AAAA\x07\r\n\x1b[0m\x1b]8;;\x07\r\nbottom\x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")

	overlay := ui.ShowOverlay(overlayImagePanel, overlayAt(1, 0, 8))
	writes := screen.frame("\x1b[?2026h\x1b[4A\r\x1b[2K\x1b[0m\x1b]8;;\x07PANEL   \x1b[0m\x1b]8;;\x07            \x1b[0m\x1b]8;;\x07\r\n\x1b[2K\x1b[0m\x1b]8;;\x07\r\n\x1b[2K\x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")
	if strings.Contains(writes, "\x1b]1337;File=") {
		t.Errorf("the covered image is drawn again: %q", writes)
	}
	// A terminal that ignores the iTerm2 sequence still applies its cursor-up prefix, so check the writes, not the viewport.
	if !strings.Contains(writes, "PANEL") {
		t.Errorf("the overlay is not drawn: %q", writes)
	}

	overlay.Hide()
	writes = screen.frame("\x1b[?2026h\x1b[2A\r\x1b[2K\x1b[0m\x1b]8;;\x07\r\n\x1b[2K\x1b[0m\x1b]8;;\x07\r\n\x1b[2K\x1b[2A\x1b]1337;File=inline=1;size=3;width=4;height=auto:AAAA\x07\x1b[?2026l\x1b[?25l")
	if !strings.Contains(writes, image) {
		t.Errorf("the image is not drawn again once uncovered: %q", writes)
	}
}

// overlay-terminal-image.test.ts:221 "removes Kitty placements in fullscreen mode while an overlay covers them".
func TestFullscreenOverlayRemovesKittyPlacementsWhileItCoversThem(t *testing.T) {
	const imageID = 11586
	image := EncodeKitty("AAAA", 4, 3, imageID, false)
	metadata := KittyImageMetadata{ImageID: imageID, Columns: 4, Rows: 3, WidthPx: 100, HeightPx: 100}
	ui, screen := newOverlayImageAltScreen(t, metadata, "top", image, "", "", "after", "")

	overlay := ui.ShowOverlay(overlayImagePanel, overlayAt(2, 1, 8))
	writes := screen.frame("\x1b[?2026h\x1b_Ga=d,d=a,q=2\x1b\\\x1b[1;1H\x1b[2Ktop\x1b[0m\x1b]8;;\x07\x1b[2;1H\x1b[2K\x1b[0m\x1b]8;;\x07\x1b[3;1H\x1b[2K \x1b[0m\x1b]8;;\x07PANEL   \x1b[0m\x1b]8;;\x07           \x1b[0m\x1b]8;;\x07\x1b[4;1H\x1b[2K\x1b[0m\x1b]8;;\x07\x1b[5;1H\x1b[2Kafter\x1b[0m\x1b]8;;\x07\x1b[6;1H\x1b[2K\x1b[0m\x1b]8;;\x07\x1b[?25l\x1b[?2026l")
	if !strings.Contains(writes, DeleteAllKittyPlacements()) {
		t.Errorf("the visible placements are not deleted: %q", writes)
	}
	if strings.Contains(writes, "i=11586") {
		t.Errorf("the covered image is placed again: %q", writes)
	}
	if got := screen.row(2); got != " PANEL" {
		t.Errorf("row 2 = %q, want %q", got, " PANEL")
	}

	overlay.Hide()
	writes = screen.frame("\x1b[?2026h\x1b_Ga=d,d=a,q=2\x1b\\\x1b[1;1H\x1b[2K\x1b[2;1H\x1b[2K\x1b[3;1H\x1b[2K\x1b[4;1H\x1b[2K\x1b[5;1H\x1b[2K\x1b[6;1H\x1b[2K\x1b[1;1Htop\x1b[0m\x1b]8;;\x07\x1b[3;1H\x1b[0m\x1b]8;;\x07\x1b[4;1H\x1b[0m\x1b]8;;\x07\x1b[5;1Hafter\x1b[0m\x1b]8;;\x07\x1b[6;1H\x1b[0m\x1b]8;;\x07\x1b[2;1H\x1b_Ga=p,q=2,C=1,c=4,r=3,i=11586\x1b\\\x1b[?25l\x1b[?2026l")
	if !strings.Contains(writes, "\x1b_Ga=p,q=2") {
		t.Errorf("the uploaded image is not placed again: %q", writes)
	}
	if strings.Contains(writes, kittyTransmitPrefix) {
		t.Errorf("the image data is uploaded again: %q", writes)
	}
}

// tui.ts:479 hideImagesUnderOverlay starts getMaxKittyImageRows above the viewport, because a Kitty image whose line has scrolled
// off the top still reaches into the viewport. Pi's test file has no case for it; the frames are Pi 787005a40e's for this input.
// The line that changes is above the viewport, so the frame is the main screen's full redraw, before the fix as after it.
func TestOverlayHidesAKittyImageWhoseLineIsAboveTheViewport(t *testing.T) {
	const imageID = 11592
	image := EncodeKitty("AAAA", 4, 4, imageID, false)
	ui, screen := newOverlayImageMainScreen(t, ImageProtocolKitty, "l0", image, "", "", "", "l5", "l6", "l7")
	RegisterKittyImageMetadata(KittyImageMetadata{ImageID: imageID, Columns: 4, Rows: 4, WidthPx: 100, HeightPx: 100})
	screen.frame("\x1b[?2026hl0\x1b[0m\x1b]8;;\x07\r\n\r\n\r\n\r\n\x1b[3A\x1b_Ga=T,f=100,q=2,C=1,c=4,r=4,i=11592;AAAA\x1b\\\x1b[3B\r\nl5\x1b[0m\x1b]8;;\x07\r\nl6\x1b[0m\x1b]8;;\x07\r\nl7\x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")

	// The image line is one row above the viewport; its rows 1 to 3 are the viewport's rows 0 to 2.
	overlay := ui.ShowOverlay(overlayImagePanel, overlayAt(1, 0, 8))
	writes := screen.frame("\x1b[?2026h\x1b_Ga=d,d=I,i=11592,q=2\x1b\\\x1b[2J\x1b[H\x1b[3Jl0\x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07PANEL   \x1b[0m\x1b]8;;\x07            \x1b[0m\x1b]8;;\x07\r\n\x1b[0m\x1b]8;;\x07\r\nl5\x1b[0m\x1b]8;;\x07\r\nl6\x1b[0m\x1b]8;;\x07\r\nl7\x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")
	if !strings.Contains(writes, DeleteKittyImage(imageID)) {
		t.Errorf("the covered image is not deleted: %q", writes)
	}
	if strings.Contains(writes, kittyTransmitPrefix) {
		t.Errorf("the covered image is drawn again: %q", writes)
	}

	overlay.Hide()
	writes = screen.frame("\x1b[?2026h\x1b[2J\x1b[H\x1b[3Jl0\x1b[0m\x1b]8;;\x07\r\n\r\n\r\n\r\n\x1b[3A\x1b_Ga=T,f=100,q=2,C=1,c=4,r=4,i=11592;AAAA\x1b\\\x1b[3B\r\nl5\x1b[0m\x1b]8;;\x07\r\nl6\x1b[0m\x1b]8;;\x07\r\nl7\x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")
	if !strings.Contains(writes, image) {
		t.Errorf("the image is not drawn again once uncovered: %q", writes)
	}
}

// tui.ts:478 hideImagesUnderOverlay returns for an overlay of no height: an overlay that renders no line hides nothing, though its
// row lies inside an image. Pi's test file has no case for it; the frames are Pi 787005a40e's for this input.
func TestOverlayThatRendersNoLineKeepsTheImageItsRowLiesIn(t *testing.T) {
	image := EncodeKitty("AAAA", 4, 4, 11593, false)
	ui, screen := newOverlayImageMainScreen(t, ImageProtocolKitty, "top", image, "", "", "", "bottom")
	screen.frame("\x1b[?2026htop\x1b[0m\x1b]8;;\x07\r\n\r\n\r\n\r\n\x1b[3A\x1b_Ga=T,f=100,q=2,C=1,c=4,r=4,i=11593;AAAA\x1b\\\x1b[3B\r\nbottom\x1b[0m\x1b]8;;\x07\x1b[?2026l\x1b[?25l")

	ui.ShowOverlay(&stubComponent{}, overlayAt(3, 0, 8))
	writes := screen.frame("\x1b[?25l")
	if strings.Contains(writes, DeleteKittyImage(11593)) {
		t.Errorf("an overlay of no height deleted the image: %q", writes)
	}
}
