package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/tui/termsim"
)

// Rendering is synchronous in this fixture; returning from Render is the upstream waitForRender barrier.
func upstreamOverlayScreen(width, height int) (*TUI, func() []string) {
	output := new(bytes.Buffer)
	terminal := termsim.New(height, width)
	ui := NewWithOutput(output, width, height)
	render := func() []string {
		ui.ForceFullRender()
		ui.Render()
		terminal.Write(output.Bytes())
		output.Reset()
		return strings.Split(terminal.String(), "\n")
	}
	return ui, render
}

func TestUpstreamOverlayOptionsOverflow(t *testing.T) {
	complexLine := "\x1b[48;2;40;50;40m \x1b[38;2;128;128;128mSome styled content\x1b[39m\x1b[49m" + "\x1b]8;;http://example.com\x07link\x1b]8;;\x07" + strings.Repeat(" more content ", 10)
	cases := []struct {
		name    string
		lines   []string
		opts    OverlayOptions
		base    func(int) []string
		visible string
	}{
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:40
		{"should truncate overlay lines that exceed declared width", []string{strings.Repeat("X", 100)}, OverlayOptions{width: overlayCells(20)}, nil, ""},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:61
		{"should handle overlay with complex ANSI sequences without crashing", []string{complexLine, complexLine, complexLine}, OverlayOptions{width: overlayCells(60)}, nil, ""},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:82
		{"should handle overlay composited on styled base content", []string{"OVERLAY"}, OverlayOptions{width: overlayCells(20), anchor: overlayCenter}, func(width int) []string {
			s := "\x1b[1m\x1b[38;2;255;0;0m" + strings.Repeat("X", width) + "\x1b[0m"
			return []string{s, s, s}
		}, "OVERLAY"},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:109
		{"should handle wide characters at overlay boundary", []string{"中文日本語한글テスト漢字"}, OverlayOptions{width: overlayCells(15)}, nil, ""},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:127
		{"should handle overlay positioned at terminal edge", []string{strings.Repeat("X", 50)}, OverlayOptions{col: overlayCells(60), width: overlayCells(20)}, nil, ""},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:145
		{"should handle overlay on base content with OSC sequences", []string{"OVERLAY-TEXT"}, OverlayOptions{anchor: overlayCenter, width: overlayCells(20)}, func(width int) []string {
			s := "See \x1b]8;;file:///path/to/file.ts\x07file.ts\x1b]8;;\x07 for details " + strings.Repeat("X", width-30)
			return []string{s, s, s}
		}, ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ui, render := upstreamOverlayScreen(80, 24)
			base := tt.base
			if base == nil {
				base = func(int) []string { return nil }
			}
			ui.Add(renderFuncComponent(base))
			ui.OpenOverlay(&recordingComponent{lines: tt.lines}, tt.opts)
			viewport := render()
			if len(viewport) == 0 {
				t.Fatal("empty viewport")
			}
			if tt.visible != "" && !strings.Contains(strings.Join(viewport, "\n"), tt.visible) {
				t.Fatalf("overlay absent: %q", viewport)
			}
		})
	}
}

func TestUpstreamOverlayOptionsWidth(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts OverlayOptions
		want int
	}{
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:174
		{"should render overlay at percentage of terminal width", OverlayOptions{width: overlayPercent(50)}, 50},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:188
		{"should respect minWidth when widthPercent results in smaller width", OverlayOptions{width: overlayPercent(10), minWidth: 30}, 30},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ui, render := upstreamOverlayScreen(100, 24)
			requestedWidth := 0
			overlay := renderFuncComponent(func(width int) []string { requestedWidth = width; return []string{"test"} })
			ui.OpenOverlay(overlay, tt.opts)
			render()
			if requestedWidth != tt.want {
				t.Fatalf("requested width=%d, want %d", requestedWidth, tt.want)
			}
		})
	}
}

func TestUpstreamOverlayOptionsPosition(t *testing.T) {
	cases := []struct {
		name, text          string
		opts                OverlayOptions
		row, minCol, maxCol int
		absentRows          []int
		end                 bool
	}{
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:204
		{"should position overlay at top-left", "TOP-LEFT", OverlayOptions{anchor: overlayTopLeft, width: overlayCells(10)}, 0, 0, 0, nil, false},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:219
		{"should position overlay at bottom-right", "BTM-RIGHT", OverlayOptions{anchor: overlayBottomRight, width: overlayCells(10)}, 23, 0, 79, nil, true},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:237
		{"should position overlay at top-center", "CENTERED", OverlayOptions{anchor: overlayTopCenter, width: overlayCells(10)}, 0, 30, 40, nil, false},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:259
		{"should clamp negative margins to zero", "NEG-MARGIN", OverlayOptions{anchor: overlayTopLeft, width: overlayCells(12), margin: overlayMargin{Top: -5, Left: -10}}, 0, 0, 0, nil, false},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:280
		{"should respect margin as number", "MARGIN", OverlayOptions{anchor: overlayTopLeft, width: overlayCells(10), marginAll: new(5)}, 5, 5, 5, []int{0, 4}, false},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:301
		{"should respect margin object", "MARGIN", OverlayOptions{anchor: overlayTopLeft, width: overlayCells(10), margin: overlayMargin{Top: 2, Left: 3}}, 2, 3, 3, nil, false},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:324
		{"should apply offsetX and offsetY from anchor position", "OFFSET", OverlayOptions{anchor: overlayTopLeft, width: overlayCells(10), offsetX: 10, offsetY: 5}, 5, 10, 10, nil, false},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:368
		{"rowPercent 0 should position at top", "TOP", OverlayOptions{width: overlayCells(10), row: overlayPercent(0)}, 0, 0, 79, nil, false},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:383
		{"rowPercent 100 should position at bottom", "BOTTOM", OverlayOptions{width: overlayCells(10), row: overlayPercent(100)}, 23, 0, 79, nil, false},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:441
		{"row and col should override anchor", "ABSOLUTE", OverlayOptions{anchor: overlayBottomRight, row: overlayCells(3), col: overlayCells(5), width: overlayCells(10)}, 3, 5, 5, nil, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ui, render := upstreamOverlayScreen(80, 24)
			ui.OpenOverlay(&recordingComponent{lines: []string{tt.text}}, tt.opts)
			viewport := render()
			col := strings.Index(viewport[tt.row], tt.text)
			if col < tt.minCol || col > tt.maxCol {
				t.Fatalf("row %d=%q, want %q at col %d..%d", tt.row, viewport[tt.row], tt.text, tt.minCol, tt.maxCol)
			}
			if tt.end && !strings.HasSuffix(strings.TrimRight(viewport[tt.row], " "), tt.text) {
				t.Fatalf("text not at row end: %q", viewport[tt.row])
			}
			for _, row := range tt.absentRows {
				if strings.Contains(viewport[row], tt.text) {
					t.Fatalf("text on excluded row %d: %q", row, viewport[row])
				}
			}
		})
	}
	// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:343
	t.Run("should position with rowPercent and colPercent", func(t *testing.T) {
		ui, render := upstreamOverlayScreen(80, 24)
		ui.OpenOverlay(&recordingComponent{lines: []string{"PCT"}}, OverlayOptions{width: overlayCells(10), row: overlayPercent(50), col: overlayPercent(50)})
		found := -1
		for row, line := range render() {
			if strings.Contains(line, "PCT") {
				found = row
				break
			}
		}
		if found < 10 || found > 13 {
			t.Fatalf("PCT row=%d, want 10..13", found)
		}
	})
}

func TestUpstreamOverlayOptionsMaxHeight(t *testing.T) {
	for _, tt := range []struct {
		name             string
		height           int
		lines            []string
		opts             OverlayOptions
		include, exclude []string
	}{
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:400
		{"should truncate overlay to maxHeight", 24, []string{"Line 1", "Line 2", "Line 3", "Line 4", "Line 5"}, OverlayOptions{maxHeight: overlayCells(3)}, []string{"Line 1", "Line 2", "Line 3"}, []string{"Line 4", "Line 5"}},
		// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:420
		{"should truncate overlay to maxHeightPercent", 10, []string{"L1", "L2", "L3", "L4", "L5", "L6", "L7", "L8", "L9", "L10"}, OverlayOptions{maxHeight: overlayPercent(50)}, []string{"L1", "L5"}, []string{"L6"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ui, render := upstreamOverlayScreen(80, tt.height)
			ui.OpenOverlay(&recordingComponent{lines: tt.lines}, tt.opts)
			content := strings.Join(render(), "\n")
			for _, s := range tt.include {
				if !strings.Contains(content, s) {
					t.Errorf("missing %q: %q", s, content)
				}
			}
			for _, s := range tt.exclude {
				if strings.Contains(content, s) {
					t.Errorf("unexpected %q: %q", s, content)
				}
			}
		})
	}
}

func TestUpstreamOverlayOptionsStacked(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:461
	t.Run("should render multiple overlays with later ones on top", func(t *testing.T) {
		ui, render := upstreamOverlayScreen(80, 24)
		ui.OpenOverlay(&recordingComponent{lines: []string{"FIRST-OVERLAY"}}, OverlayOptions{anchor: overlayTopLeft, width: overlayCells(20)})
		ui.OpenOverlay(&recordingComponent{lines: []string{"SECOND"}}, OverlayOptions{anchor: overlayTopLeft, width: overlayCells(10)})
		if viewport := render(); !strings.Contains(viewport[0], "SECOND") {
			t.Fatal(viewport)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:486
	t.Run("should handle overlays at different positions without interference", func(t *testing.T) {
		ui, render := upstreamOverlayScreen(80, 24)
		ui.OpenOverlay(&recordingComponent{lines: []string{"TOP-LEFT"}}, OverlayOptions{anchor: overlayTopLeft, width: overlayCells(15)})
		ui.OpenOverlay(&recordingComponent{lines: []string{"BTM-RIGHT"}}, OverlayOptions{anchor: overlayBottomRight, width: overlayCells(15)})
		viewport := render()
		if !strings.Contains(viewport[0], "TOP-LEFT") || !strings.Contains(viewport[23], "BTM-RIGHT") {
			t.Fatal(viewport)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/overlay-options.test.ts:510
	t.Run("should properly hide overlays in stack order", func(t *testing.T) {
		ui, render := upstreamOverlayScreen(80, 24)
		ui.OpenOverlay(&recordingComponent{lines: []string{"FIRST"}}, OverlayOptions{anchor: overlayTopLeft, width: overlayCells(10)})
		ui.OpenOverlay(&recordingComponent{lines: []string{"SECOND"}}, OverlayOptions{anchor: overlayTopLeft, width: overlayCells(10)})
		if viewport := render(); !strings.Contains(viewport[0], "SECOND") {
			t.Fatal(viewport)
		}
		ui.hideOverlay()
		if viewport := render(); !strings.Contains(viewport[0], "FIRST") {
			t.Fatal(viewport)
		}
	})
}

// cursorVisibility replays the cursor show and hide sequences a renderer wrote; a terminal starts with its cursor visible (upstream `CursorTrackingTerminal.cursorVisible`, overlay-options.test.ts:25).
func cursorVisibility(output string) bool {
	visible := true
	for i := 0; i+len("\x1b[?25l") <= len(output); i++ {
		switch output[i : i+len("\x1b[?25l")] {
		case "\x1b[?25l":
			visible = false
		case "\x1b[?25h":
			visible = true
		}
	}
	return visible
}

// .upstream/v0.99.2/packages/tui/test/overlay-options.test.ts:557 (https://github.com/earendil-works/pi/issues/10026).
func TestUpstreamOverlayOptionsHidingAfterStop(t *testing.T) {
	// overlay-options.test.ts:558.
	t.Run("hideOverlay() leaves the cursor visible", func(t *testing.T) {
		output := new(bytes.Buffer)
		ui := NewWithOutput(output, 80, 24)
		ui.Start()
		ui.OpenOverlay(&recordingComponent{lines: []string{"OVERLAY"}}, OverlayOptions{nonCapturing: true})

		ui.Stop()
		ui.hideOverlay()

		if !cursorVisibility(output.String()) {
			t.Fatalf("cursor hidden after hideOverlay() following stop(): %q", output.String())
		}
	})
	// overlay-options.test.ts:572.
	t.Run("overlay handle hide() leaves the cursor visible", func(t *testing.T) {
		output := new(bytes.Buffer)
		ui := NewWithOutput(output, 80, 24)
		ui.Start()
		handle := ui.OpenOverlay(&recordingComponent{lines: []string{"OVERLAY"}}, OverlayOptions{nonCapturing: true})

		ui.Stop()
		handle.Hide()

		if !cursorVisibility(output.String()) {
			t.Fatalf("cursor hidden after handle.Hide() following stop(): %q", output.String())
		}
	})
}
