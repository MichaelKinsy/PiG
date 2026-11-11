package tui

import (
	"bytes"
	"strings"
	"testing"
)

// Ports Pi be2744d956dd (#12047, issue #12010) test/tui-alt-screen.test.ts
// "keeps uploaded Kitty images across a full redraw": Kitty frees image data
// that has no placements on ED 2, so a full redraw must delete the placements
// and skip the screen clear instead of clearing and then reusing placements.
func TestAltScreenKeepsUploadedKittyImagesAcrossFullRedraw(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	t.Cleanup(ResetCapabilitiesCache)
	const imageID = 12010
	imageLine := EncodeKitty("AAAA", 2, 3, imageID, false)
	RegisterKittyImageMetadata(KittyImageMetadata{ImageID: imageID, Columns: 2, Rows: 3, WidthPx: 100, HeightPx: 100})
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 20, 6, TuiAltScreenOptions{})
	t.Cleanup(func() { tui.StopWithOptions(StopOptions{PreserveScreen: true}) })
	tui.SetLayoutRoot(&stubComponent{lines: []string{"before", imageLine, "", "", "after"}})
	tui.Start()
	mark := out.Len()

	tui.SetFixedSize(20, 8)
	tui.Render()
	writes := out.String()[mark:]
	if !strings.Contains(writes, "\x1b_Ga=p,q=2") {
		t.Errorf("full redraw did not reuse the uploaded image with a placement: %q", writes)
	}
	if strings.Contains(writes, kittyTransmitPrefix) {
		t.Errorf("full redraw re-transmitted the image: %q", writes)
	}
	if strings.Contains(writes, "\x1b[2J") {
		t.Errorf("full redraw cleared the screen with ED 2, freeing the Kitty image data: %q", writes)
	}
}
