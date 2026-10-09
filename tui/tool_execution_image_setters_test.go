package tui

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Upstream tool-execution.ts setShowImages(show) stores the flag and redraws; setImageWidthCells(width) stores
// Math.max(1, Math.floor(width)) and redraws.
// Pi: packages/coding-agent/src/modes/interactive/components/tool-execution.ts:199 (ToolExecutionComponent.setShowImages); packages/coding-agent/src/modes/interactive/components/tool-execution.ts:204 (ToolExecutionComponent.setImageWidthCells).
func TestToolExecutionImageSettersRedrawLikeUpstream(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty})
	defer ResetCapabilitiesCache()
	c := newToolCardForTest("read", `path:"test.png"`)
	c.ImageBlocks = []ImageBlock{{Data: "iVBORw0KGgoAAAANSUhEUg==", MIMEType: "image/png"}}
	c.ShowImages = false
	c.SetResult("image file read", false, 0)
	// A Kitty image is one escape sequence (kitty graphics "\x1b_G"); the fallback text names the MIME type.
	drawn := func(l string) bool { return strings.Contains(l, "\x1b_G") || strings.Contains(l, "image/png") }
	hidden := c.Render(80)
	if slices.ContainsFunc(hidden, drawn) {
		t.Fatalf("image shown while ShowImages is false: %q", hidden)
	}
	c.SetShowImages(true)
	if shown := c.Render(80); !slices.ContainsFunc(shown, drawn) {
		t.Fatalf("SetShowImages(true) did not redraw the image: %q", shown)
	}
	c.SetShowImages(false)
	if again := c.Render(80); slices.ContainsFunc(again, drawn) {
		t.Fatalf("SetShowImages(false) did not hide the image: %q", again)
	}
	for _, tc := range []struct{ in, want int }{{40, 40}, {1, 1}, {0, 1}, {-7, 1}} {
		c.SetImageWidthCells(tc.in)
		if c.ImageWidthCells != tc.want {
			t.Errorf("SetImageWidthCells(%d) stored %d, want %d", tc.in, c.ImageWidthCells, tc.want)
		}
	}
}

// setImageWidthCells redraws at the new width: on an image-capable terminal the image is laid out from the stored
// width, so a cached render from the old width must not survive the setter.
func TestToolExecutionSetImageWidthCellsRedrawsAtTheNewWidth(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty})
	defer ResetCapabilitiesCache()
	c := newToolCardForTest("read", `path:"test.png"`)
	c.ImageBlocks = []ImageBlock{{Data: "iVBORw0KGgoAAAANSUhEUg==", MIMEType: "image/png"}}
	c.SetShowImages(true)
	c.SetResult("image file read", false, 0)
	c.SetImageWidthCells(40)
	wide := c.Render(80)
	c.SetImageWidthCells(10)
	narrow := c.Render(80)
	if slices.Equal(wide, narrow) {
		t.Fatalf("SetImageWidthCells(10) returned the 40-cell render: %q", narrow)
	}
	fresh := newToolCardForTest("read", `path:"test.png"`)
	fresh.ImageBlocks = c.ImageBlocks
	fresh.SetShowImages(true)
	fresh.SetResult("image file read", false, 0)
	fresh.SetImageWidthCells(10)
	// Each Image allocates its own Kitty image id (image.ts), so compare the layout without it.
	dropID := func(rows []string) []string {
		out := make([]string, len(rows))
		for i, row := range rows {
			out[i] = regexp.MustCompile(`,i=\d+;`).ReplaceAllString(row, ";")
		}
		return out
	}
	if want := fresh.Render(80); !slices.Equal(dropID(narrow), dropID(want)) {
		t.Fatalf("redraw after SetImageWidthCells(10) = %q, want a fresh 10-cell render %q", narrow, want)
	}
}
