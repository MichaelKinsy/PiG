package tui

import (
	"fmt"
	"strings"
	"testing"
)

func upstreamKittyImageRows(t *testing.T, cells int) []string {
	t.Helper()
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	SetCellDimensions(CellDimensions{WidthPx: 10, HeightPx: 10})
	t.Cleanup(func() { ResetCapabilitiesCache(); SetCellDimensions(CellDimensions{WidthPx: 9, HeightPx: 18}) })
	image := NewImage("AAAA", "image/png", ImageOptions{MaxWidthCells: cells}, &ImageDimensions{WidthPx: cells * 10, HeightPx: cells * 10})
	image.Theme = ImageTheme{FallbackColor: func(value string) string { return value }}
	return image.Render(40)
}

func TestUpstreamTUIRenderKittyCleanup(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:260
	t.Run("clears reserved Kitty image rows before drawing appended image placements", func(t *testing.T) {
		rows := upstreamKittyImageRows(t, 2)
		h := newUpstreamRenderHarness(t, 40, 10)
		h.render([]string{"before"})
		lines := append([]string{"before"}, rows...)
		lines = append(lines, "after")
		writes := h.render(lines)
		upstreamContains(t, writes, "\x1b[2K\r\n\x1b[2K\x1b[1A"+rows[0]+"\x1b[1B")
		upstreamExcludes(t, writes, rows[0]+"\r\n\x1b[2K")
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:304
	t.Run("falls back to full redraw when Kitty image pre-clear would scroll", func(t *testing.T) {
		rows := upstreamKittyImageRows(t, 3)
		h := newUpstreamRenderHarness(t, 40, 2)
		h.render([]string{"before"})
		lines := append([]string{"before"}, rows...)
		lines = append(lines, "after")
		upstreamContains(t, h.render(lines), "\x1b[2J")
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:340
	t.Run("reserves Kitty image rows before drawing during full redraw fallbacks", func(t *testing.T) {
		rows := upstreamKittyImageRows(t, 3)
		h := newUpstreamRenderHarness(t, 40, 5)
		before := []string{"l0", "l1", "l2", "l3", "l4"}
		h.render(before)
		lines := append(append([]string{}, before...), rows...)
		lines = append(lines, "after")
		writes := h.render(lines)
		upstreamContains(t, writes, "\x1b[2J")
		upstreamContains(t, writes, "\r\n\r\n\x1b[2A"+rows[0]+"\x1b[2B")
		upstreamExcludes(t, writes, rows[0]+"\r\n\x1b[0m")
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:386
	t.Run("does not use cursor-up placement for Kitty images taller than the viewport", func(t *testing.T) {
		rows := upstreamKittyImageRows(t, 6)
		h := newUpstreamRenderHarness(t, 40, 5)
		h.render([]string{"before"})
		if len(rows) <= 5 {
			t.Fatalf("image has %d rows, want more than 5", len(rows))
		}
		lines := append([]string{"before"}, rows...)
		lines = append(lines, "after")
		h.ui.ForceFullRender()
		writes := h.render(lines)
		upstreamContains(t, writes, rows[0])
		upstreamExcludes(t, writes, fmt.Sprintf("\x1b[%dA%s", len(rows)-1, rows[0]))
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:429
	t.Run("deletes changed image ids before drawing moved placements", func(t *testing.T) {
		h := newUpstreamRenderHarness(t, 40, 10)
		old := EncodeKitty("AAAA", 2, 2, 42, false)
		h.render([]string{"top", old})
		next := EncodeKitty("BBBB", 2, 1, 42, false)
		writes := h.render([]string{next, ""})
		assertKittyDeleteBefore(t, writes, 42, next)
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:456
	t.Run("redraws image lines when an earlier reserved image row changes", func(t *testing.T) {
		h := newUpstreamRenderHarness(t, 40, 10)
		image := EncodeKitty("AAAA", 2, 2, 88, false)
		h.render([]string{"", image})
		writes := h.render([]string{"covered", image})
		assertKittyDeleteBefore(t, writes, 88, image)
		upstreamExcludes(t, writes, "\x1b[2J")
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:483
	t.Run("deletes previously rendered image ids during full redraws", func(t *testing.T) {
		h := newUpstreamRenderHarness(t, 40, 10)
		h.render([]string{EncodeKitty("AAAA", 2, 2, 77, false)})
		h.ui.ForceFullRender()
		writes := h.render([]string{"plain text"})
		assertKittyDeleteBefore(t, writes, 77, "\x1b[2J")
	})
}

func TestRawKittyCleanupWithoutAdvertisedSupport(t *testing.T) {
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	SetCapabilities(TerminalCapabilities{})
	h := newUpstreamRenderHarness(t, 40, 10)
	old := "\x1b_Ga=T,f=100,i=42,r=2;AAAA\x1b\\"
	next := "\x1b_Ga=T,f=100,i=42,r=1;BBBB\x1b\\"
	h.render([]string{"top", old})
	assertKittyDeleteBefore(t, h.render([]string{next, ""}), 42, next)
}

// Pi's Set preserves encounter order and removes duplicate IDs before full-redraw deletion.
func TestKittyDeletionPreservesEncounterOrder(t *testing.T) {
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty})
	h := newUpstreamRenderHarness(t, 40, 30)
	var lines []string
	var want strings.Builder
	for id := 20; id > 0; id-- {
		line := EncodeKitty("AAAA", 1, 1, id, false)
		lines = append(lines, line, line)
		want.WriteString(DeleteKittyImage(id))
	}
	h.render(lines)
	h.ui.ForceFullRender()
	writes := h.render([]string{"plain"})
	upstreamContains(t, writes, want.String()+"\x1b[2J")
}

func assertKittyDeleteBefore(t *testing.T, writes string, id int, draw string) {
	t.Helper()
	deleted, drawn := strings.Index(writes, DeleteKittyImage(id)), strings.Index(writes, draw)
	if deleted < 0 || drawn < 0 || deleted >= drawn {
		t.Fatalf("delete at %d, draw at %d: %q", deleted, drawn, writes)
	}
}
