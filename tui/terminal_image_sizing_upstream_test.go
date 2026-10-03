package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// .upstream/v0.99.2/packages/tui/test/terminal-image.test.ts:446.
func TestUpstreamTerminalImageDetectsTruecolorFromDirectColorTerm(t *testing.T) {
	isolateCapabilityEnv(t)
	t.Setenv("TERM", "xterm-direct")
	if caps := DetectCapabilities(func() bool { return false }); !caps.TrueColor {
		t.Fatalf("caps=%#v, want truecolor for TERM=xterm-direct", caps)
	}
}

// .upstream/v0.99.2/packages/tui/test/terminal-image.test.ts:632 (#8938: reduce Kitty placement distortion without shrinking iTerm2 reservations).
func TestUpstreamImageCellSizing(t *testing.T) {
	prepare := func(t *testing.T, protocol ImageProtocol) {
		preserveCapabilityState(t)
		t.Cleanup(func() {
			ResetCapabilitiesCache()
			SetCellDimensions(CellDimensions{WidthPx: 9, HeightPx: 18})
		})
		SetCapabilities(TerminalCapabilities{Images: protocol, TrueColor: true, Hyperlinks: true})
	}
	newImage := func(dimensions ImageDimensions, options ImageOptions) *Image {
		return NewImage("AAAA", "image/png", options, &dimensions)
	}

	t.Run("Kitty", func(t *testing.T) {
		// terminal-image.test.ts:643.
		t.Run("reserves at least one Kitty row for thin images", func(t *testing.T) {
			prepare(t, ImageProtocolKitty)
			SetCellDimensions(CellDimensions{WidthPx: 9, HeightPx: 18})
			result := RenderImage("AAAA", ImageDimensions{WidthPx: 1200, HeightPx: 12}, ImageRenderOptions{MaxWidthCells: 60})
			if result == nil || result.Rows != 1 || !strings.Contains(result.Sequence, ",c=60,r=1;") {
				t.Fatalf("result=%#v", result)
			}
		})

		// terminal-image.test.ts:651.
		t.Run("keeps Kitty placement, reserved lines, and cropping metadata consistent across width changes", func(t *testing.T) {
			prepare(t, ImageProtocolKitty)
			SetCellDimensions(CellDimensions{WidthPx: 9, HeightPx: 18})
			image := newImage(ImageDimensions{WidthPx: 615, HeightPx: 86}, ImageOptions{MaxWidthCells: 60, ImageID: 8938})
			lines := image.Render(62)
			if len(lines) != 4 || !slices.Equal(lines[1:], []string{"", "", ""}) {
				t.Fatalf("lines=%q", lines)
			}
			if !strings.Contains(lines[0], ",c=60,r=4,i=8938;") {
				t.Fatalf("first line=%q", lines[0])
			}
			if got, want := GetKittyImageMetadata(lines[0]), (&KittyImageMetadata{ImageID: 8938, Columns: 60, Rows: 4, WidthPx: 615, HeightPx: 86}); got == nil || *got != *want {
				t.Fatalf("metadata=%+v, want %+v", got, want)
			}
			cropped := CropKittyImageLine(lines[0], 1, 2)
			if placement, ok := GetKittyImagePlacement(cropped); !ok || placement.Sequence != "\x1b_Ga=p,q=2,C=1,c=60,i=8938,y=21,h=44,r=2\x1b\\" {
				t.Fatalf("placement=%q ok=%v", placement.Sequence, ok)
			}

			narrower := image.Render(32)
			if len(narrower) != 2 || !strings.Contains(narrower[0], ",c=30,r=2,i=8938;") {
				t.Fatalf("narrower=%q", narrower)
			}
			if metadata := GetKittyImageMetadata(narrower[0]); metadata == nil || metadata.Rows != 2 {
				t.Fatalf("narrower metadata=%+v", metadata)
			}
		})

		// terminal-image.test.ts:683.
		t.Run("keeps the ceiling placement when rounding down would increase distortion", func(t *testing.T) {
			prepare(t, ImageProtocolKitty)
			SetCellDimensions(CellDimensions{WidthPx: 15, HeightPx: 28})
			result := RenderImage("AAAA", ImageDimensions{WidthPx: 615, HeightPx: 86}, ImageRenderOptions{MaxWidthCells: 60})
			if result == nil || result.Rows != 5 || !strings.Contains(result.Sequence, ",c=60,r=5;") {
				t.Fatalf("result=%#v", result)
			}
		})

		// terminal-image.test.ts:691.
		t.Run("keeps height-limited Kitty columns, reservations, and crop metadata consistent", func(t *testing.T) {
			prepare(t, ImageProtocolKitty)
			SetCellDimensions(CellDimensions{WidthPx: 14, HeightPx: 28})
			image := newImage(ImageDimensions{WidthPx: 400, HeightPx: 900}, ImageOptions{MaxWidthCells: 30, ImageID: 8938})
			lines := image.Render(32)
			if len(lines) != 15 || !strings.Contains(lines[0], ",c=13,r=15,i=8938;") {
				t.Fatalf("lines=%d first=%q", len(lines), lines[0])
			}
			if got, want := GetKittyImageMetadata(lines[0]), (&KittyImageMetadata{ImageID: 8938, Columns: 13, Rows: 15, WidthPx: 400, HeightPx: 900}); got == nil || *got != *want {
				t.Fatalf("metadata=%+v, want %+v", got, want)
			}
			if placement, ok := GetKittyImagePlacement(CropKittyImageLine(lines[0], 1, 2)); !ok || placement.Sequence != "\x1b_Ga=p,q=2,C=1,c=13,i=8938,y=60,h=120,r=2\x1b\\" {
				t.Fatalf("placement=%q ok=%v", placement.Sequence, ok)
			}
			narrower := image.Render(22)
			if len(narrower) != 10 || !strings.Contains(narrower[0], ",c=9,r=10,i=8938;") {
				t.Fatalf("narrower lines=%d first=%q", len(narrower), narrower[0])
			}
		})

		// terminal-image.test.ts:719.
		t.Run("chooses thin Kitty widths by proportions while keeping at least one column", func(t *testing.T) {
			prepare(t, ImageProtocolKitty)
			SetCellDimensions(CellDimensions{WidthPx: 1, HeightPx: 1})
			for _, tc := range [][2]int{{1, 1}, {140, 1}, {149, 2}} {
				widthPx, columns := tc[0], tc[1]
				result := RenderImage("AAAA", ImageDimensions{WidthPx: widthPx, HeightPx: 1000}, ImageRenderOptions{MaxWidthCells: 30, MaxHeightCells: 10})
				if result == nil || result.Columns != columns || result.Rows != 10 || !strings.Contains(result.Sequence, fmt.Sprintf(",c=%d,r=10;", columns)) {
					t.Fatalf("widthPx=%d result=%#v, want %d columns", widthPx, result, columns)
				}
			}
		})
	})

	t.Run("iTerm2", func(t *testing.T) {
		// terminal-image.test.ts:740.
		t.Run("keeps iTerm2's ceiling width when height-limited", func(t *testing.T) {
			prepare(t, ImageProtocolITerm2)
			SetCellDimensions(CellDimensions{WidthPx: 14, HeightPx: 28})
			result := RenderImage("AAAA", ImageDimensions{WidthPx: 400, HeightPx: 900}, ImageRenderOptions{MaxWidthCells: 30, MaxHeightCells: 15})
			if result == nil || result.Columns != 14 || result.Rows != 15 || result.Sequence != "\x1b]1337;File=inline=1;size=3;width=14;height=auto:AAAA\x07" {
				t.Fatalf("result=%#v", result)
			}
		})

		// terminal-image.test.ts:749.
		t.Run("keeps iTerm2's ceiling-based reserved lines and cursor offset", func(t *testing.T) {
			prepare(t, ImageProtocolITerm2)
			SetCellDimensions(CellDimensions{WidthPx: 9, HeightPx: 18})
			image := newImage(ImageDimensions{WidthPx: 615, HeightPx: 86}, ImageOptions{MaxWidthCells: 60})
			want := []string{"", "", "", "", "\x1b[4A\x1b]1337;File=inline=1;size=3;width=60;height=auto:AAAA\x07"}
			if got := image.Render(62); !slices.Equal(got, want) {
				t.Fatalf("lines=%q, want %q", got, want)
			}
		})
	})
}
