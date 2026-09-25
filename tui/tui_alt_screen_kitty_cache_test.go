package tui

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

// Ports of the upstream test/tui-alt-screen.test.ts offscreen Kitty upload
// cache cases (retention, least-recently-visible eviction, decoded-raster
// quota eviction). Each upstream waitForRender is an explicit Render, and the
// bytes written since a mark stand in for the recorded write events.

const kittyTransmitPrefix = "\x1b_Ga=T"

type kittyCacheHarness struct {
	t   *testing.T
	out *bytes.Buffer
	tui *TuiAltScreen
}

// newKittyCacheHarness starts a 20x1 alt-screen with Kitty images enabled over
// a primary ScrollView of lines, matching upstream RecordingTerminal(20, 1).
func newKittyCacheHarness(t *testing.T, lines []string) *kittyCacheHarness {
	t.Helper()
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	t.Cleanup(ResetCapabilitiesCache)
	out := &bytes.Buffer{}
	tui := newAltScreenForTest(out, 20, 1, TuiAltScreenOptions{})
	t.Cleanup(func() { tui.StopWithOptions(StopOptions{PreserveScreen: true}) })
	tui.SetLayoutRoot(NewScrollView(&stubComponent{lines: lines}, ScrollViewOptions{Primary: true}))
	tui.Start()
	return &kittyCacheHarness{t: t, out: out, tui: tui}
}

// writesSince returns everything written after mark.
func (h *kittyCacheHarness) writesSince(mark int) string { return h.out.String()[mark:] }

func (h *kittyCacheHarness) scrollBy(lines int) {
	h.tui.ScrollBy(lines)
	h.tui.Render()
}

func registerKittyCacheImages(firstImageID, count, widthPx, heightPx int) []string {
	lines := make([]string, count)
	for i := range lines {
		imageID := firstImageID + i
		RegisterKittyImageMetadata(KittyImageMetadata{ImageID: imageID, Columns: 2, Rows: 1, WidthPx: widthPx, HeightPx: heightPx})
		lines[i] = EncodeKitty("AAAA", 2, 1, imageID, false)
	}
	return lines
}

func transmitsImage(writes string, imageID int) bool {
	return strings.Contains(writes, kittyTransmitPrefix) && strings.Contains(writes, "i="+strconv.Itoa(imageID)+",")
}

// Upstream: "retains recently offscreen Kitty images for placement-only reuse".
func TestAltScreenRetainsRecentlyOffscreenKittyImages(t *testing.T) {
	const imageID = 321
	imageLine := EncodeKitty("AAAA", 2, 1, imageID, false)
	RegisterKittyImageMetadata(KittyImageMetadata{ImageID: imageID, Columns: 2, Rows: 1, WidthPx: 100, HeightPx: 50})
	h := newKittyCacheHarness(t, []string{imageLine, "after"})
	if !strings.Contains(h.out.String(), kittyTransmitPrefix) {
		t.Fatalf("initial frame should transmit the image; got %q", h.out.String())
	}

	mark := h.out.Len()
	h.scrollBy(1)
	h.scrollBy(-1)
	reentry := h.writesSince(mark)
	if !strings.Contains(reentry, "\x1b_Ga=p,q=2") {
		t.Errorf("re-entry should place the retained image; got %q", reentry)
	}
	if strings.Contains(reentry, kittyTransmitPrefix) {
		t.Errorf("re-entry must not retransmit the retained image; got %q", reentry)
	}
	if strings.Contains(reentry, DeleteKittyImage(imageID)) {
		t.Errorf("a single offscreen image is within budget and must not be deleted; got %q", reentry)
	}
}

// Upstream: "evicts the least recently visible Kitty image when the cache is full".
func TestAltScreenEvictsLeastRecentlyVisibleKittyImageWhenCacheFull(t *testing.T) {
	const firstImageID = 500
	imageLines := registerKittyCacheImages(firstImageID, 18, 100, 50)
	h := newKittyCacheHarness(t, imageLines)

	// Viewing image index k leaves k images offscreen; the count budget (16)
	// is only exceeded once the last image is visible.
	for index := 1; index < len(imageLines)-1; index++ {
		h.scrollBy(1)
	}
	if strings.Contains(h.out.String(), "\x1b_Ga=d,d=I,") {
		t.Fatalf("no image should be evicted while at most %d are offscreen; got %q", altMaxCachedOffscreenImages, h.out.String())
	}
	mark := h.out.Len()
	h.scrollBy(1)
	lastStep := h.writesSince(mark)
	if !strings.Contains(lastStep, DeleteKittyImage(firstImageID)) {
		t.Fatalf("17 offscreen images should evict the least recently visible %d; got %q", firstImageID, lastStep)
	}
	if strings.Count(lastStep, "\x1b_Ga=d,d=I,") != 1 {
		t.Errorf("exactly one image should be evicted; got %q", lastStep)
	}

	mark = h.out.Len()
	h.tui.ScrollToTop()
	h.tui.Render()
	reentry := h.writesSince(mark)
	if !transmitsImage(reentry, firstImageID) {
		t.Errorf("evicted image %d should be retransmitted on re-entry; got %q", firstImageID, reentry)
	}
	// Re-entry makes the evicted image visible again, so the next least
	// recently visible image is the one pushed out of the full cache.
	if !strings.Contains(reentry, DeleteKittyImage(firstImageID+1)) {
		t.Errorf("re-entry should evict the next least recently visible image %d; got %q", firstImageID+1, reentry)
	}
	if strings.Count(reentry, "\x1b_Ga=d,d=I,") != 1 {
		t.Errorf("re-entry should evict exactly one image; got %q", reentry)
	}

	// Seeing a cached image again refreshes its recency: revisit 502 (the
	// oldest cached image), then upload 501 into the full cache. The
	// eviction must skip the refreshed 502 and take 503.
	h.scrollBy(2)
	mark = h.out.Len()
	h.scrollBy(-1)
	refreshed := h.writesSince(mark)
	if !transmitsImage(refreshed, firstImageID+1) {
		t.Errorf("evicted image %d should be retransmitted; got %q", firstImageID+1, refreshed)
	}
	if strings.Contains(refreshed, DeleteKittyImage(firstImageID+2)) {
		t.Errorf("recently revisited image %d must be retained; got %q", firstImageID+2, refreshed)
	}
	if !strings.Contains(refreshed, DeleteKittyImage(firstImageID+3)) {
		t.Errorf("least recently visible image %d should be evicted; got %q", firstImageID+3, refreshed)
	}
}

// Upstream: "evicts offscreen Kitty images when decoded raster memory exceeds
// the cache quota".
func TestAltScreenEvictsOffscreenKittyImagesOverDecodedMemoryQuota(t *testing.T) {
	const firstImageID = 600
	imageLines := registerKittyCacheImages(firstImageID, 4, 3840, 2160)
	h := newKittyCacheHarness(t, imageLines)

	// Each 3840x2160 raster decodes to 33,177,600 bytes: two offscreen stay
	// under the 64 MiB quota, three exceed it long before the count budget.
	h.scrollBy(1)
	h.scrollBy(1)
	if strings.Contains(h.out.String(), "\x1b_Ga=d,d=I,") {
		t.Fatalf("two offscreen 4K rasters fit the decoded quota; got %q", h.out.String())
	}
	mark := h.out.Len()
	h.scrollBy(1)
	lastStep := h.writesSince(mark)
	if !strings.Contains(lastStep, DeleteKittyImage(firstImageID)) {
		t.Fatalf("decoded quota overflow should evict image %d; got %q", firstImageID, lastStep)
	}
	if strings.Count(lastStep, "\x1b_Ga=d,d=I,") != 1 {
		t.Errorf("evicting the oldest raster restores the quota, so only one delete is expected; got %q", lastStep)
	}
}
