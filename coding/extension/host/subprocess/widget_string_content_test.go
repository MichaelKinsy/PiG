package subprocess

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi 0.87.1 interactive-mode.ts:2321-2336 wraps a widget set as string[] in a
// Container of Text(line, 1, 0) for the first MAX_WIDGET_LINES (10) entries
// (interactive-mode.ts:2377) plus a muted "... (widget truncated)" Text, and
// the TUI renders it at the current width on every frame. Public issue #104:
// PiG painted the rows as pushed, so a row wider than the pane reached the
// differential renderer and panicked.
//
// The expected rows are the output of Pi's pi-tui Text.render for the same
// input (probed against the pinned package), not PiG's Text.
func TestSetWidgetStringContentUsesPiTextLayout(t *testing.T) {
	muted := tui.ActiveTheme().Fg("muted", "... (widget truncated)")
	cases := []struct {
		name    string
		content []string
		width   int
		want    []string
	}{
		{"short", []string{"ordinary"}, 20, []string{" ordinary           "}},
		{"wrap and styled", []string{"", "wide 界 and words wrap at a narrow width", "\x1b[31mred\x1b[39m"}, 20,
			[]string{" wide 界 and words  ", " wrap at a narrow   ", " width              ", " \x1b[31mred\x1b[39m                "}},
		{"row wider than the pane", []string{strings.Repeat("A", 60) + " tail"}, 40,
			[]string{" " + strings.Repeat("A", 38) + " ", " " + strings.Repeat("A", 22) + " tail            "}},
		{"truncated after ten entries", slices.Collect(func(yield func(string) bool) {
			for i := range 11 {
				if !yield("entry" + string(rune('0'+i%10))) {
					return
				}
			}
		}), 40, append(slices.Collect(func(yield func(string) bool) {
			for i := range 10 {
				if !yield(" entry" + string(rune('0'+i)) + strings.Repeat(" ", 33)) {
					return
				}
			}
		}), " "+muted+strings.Repeat(" ", 17))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bridge := NewUIBridge(func() {})
			bridge.SetUIContext(newFakeUIContext())
			args, err := json.Marshal(map[string]any{"key": "list", "content": tc.content})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := bridge.HandleCall("ext1", &CallPayload{Method: "ui.setWidget", Args: args}); err != nil {
				t.Fatal(err)
			}
			proxy := bridge.GetWidget("ext1", "list")
			if proxy == nil {
				t.Fatal("widget not published")
			}
			// The same widget at a second width proves the layout follows the
			// host width with no new push, as a component does in Pi.
			for _, width := range []int{tc.width, tc.width + 7} {
				for i, row := range proxy.Render(width) {
					if got := widthx.VisibleWidth(row); got > width {
						t.Fatalf("width %d row %d is %d cells: %q", width, i, got, row)
					}
				}
			}
			if got := proxy.Render(tc.width); !slices.Equal(got, tc.want) {
				t.Fatalf("Render(%d) = %q, want %q", tc.width, got, tc.want)
			}
		})
	}
}

// A frame that carries the width it was rendered at is a pre-rendered
// component frame (a Node factory widget) and keeps its rows and stale-frame
// rule: only a width-less string list takes Pi's Text layout.
func TestSetWidgetFrameWithWidthIsNotRewrapped(t *testing.T) {
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(newFakeUIContext())
	args, err := json.Marshal(map[string]any{"key": "frame", "content": []string{"a  b"}, "width": 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.HandleCall("ext1", &CallPayload{Method: "ui.setWidget", Args: args}); err != nil {
		t.Fatal(err)
	}
	proxy := bridge.GetWidget("ext1", "frame")
	if got := proxy.Render(20); !slices.Equal(got, []string{"a  b"}) {
		t.Fatalf("frame rows at its width = %q, want the pushed row unchanged", got)
	}
	if got := proxy.Render(30); got != nil {
		t.Fatalf("frame painted at another width: %q", got)
	}
}

// A string list widget renders through Text components that cache their last
// layout, so Render writes proxy state and must exclude concurrent renders.
func TestSetWidgetStringContentRendersConcurrently(t *testing.T) {
	proxy := NewPushProxy(nil, nil)
	proxy.UpdateContent([]string{"a row that wraps at the narrower of the two widths"})
	var wg sync.WaitGroup
	for _, width := range []int{20, 33} {
		wg.Go(func() {
			for range 200 {
				for _, row := range proxy.Render(width) {
					if got := widthx.VisibleWidth(row); got != width {
						t.Errorf("width %d row is %d cells: %q", width, got, row)
						return
					}
				}
			}
		})
	}
	wg.Wait()
}
