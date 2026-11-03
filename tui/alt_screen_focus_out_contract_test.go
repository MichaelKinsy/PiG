package tui

// pi: packages/tui/src/tui-alt-screen.ts handleViewportInput, the FOCUS_OUT branch (694-720)

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

const focusOutReport = "\x1b[O"

// A focus-out report ends every pointer gesture in progress: the selection auto-scroll stops, the scrollbar hover and drag end, the pressed link
// and the drag flag are forgotten, and a press that was still held loses its selection (the anchor, the focus, the word or line granularity and its range).
func TestAltScreenFocusOutEndsPointerGestures(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
	h.tui.Add(NewText(numberedLines(12)))
	h.start()
	h.render()
	// Press on the third row and drag to the first: the pointer sits on the top edge, so the selection auto-scroll arms.
	h.send("\x1b[<0;3;3M", "\x1b[<32;3;1M")
	h.tui.mu.Lock()
	armed := h.tui.selectionAutoScrollTimer != nil && h.tui.selectionAutoScrollDir != 0 && h.tui.selectionDragPointer != nil
	generation := h.tui.selectionAutoScrollGen
	dragged := h.tui.selectionDragged && h.tui.selectionPressActive && h.tui.selectionAnchor != nil && h.tui.selectionFocus != nil
	h.tui.selectionGranularity, h.tui.selectionInitialRange = selectWord, &selectionRange{}
	h.tui.scrollbarHover = h.tui.implicitScrollView
	h.tui.scrollbarDrag = &scrollbarDrag{scrollView: h.tui.implicitScrollView}
	h.tui.pressedURL, h.tui.pressedURLSet = "https://example.com", true
	h.tui.mu.Unlock()
	if !armed || !dragged {
		t.Fatalf("precondition: armed=%v dragged=%v", armed, dragged)
	}
	if !h.tui.HandleViewportInput(focusOutReport) {
		t.Fatal("the focus-out report is consumed")
	}
	h.tui.mu.Lock()
	defer h.tui.mu.Unlock()
	for name, ended := range map[string]bool{
		"auto-scroll timer":       h.tui.selectionAutoScrollTimer == nil,
		"auto-scroll direction":   h.tui.selectionAutoScrollDir == 0,
		"auto-scroll pointer":     h.tui.selectionDragPointer == nil,
		"auto-scroll generation":  h.tui.selectionAutoScrollGen != generation,
		"press":                   !h.tui.selectionPressActive,
		"drag flag":               !h.tui.selectionDragged,
		"scrollbar hover":         h.tui.scrollbarHover == nil,
		"scrollbar drag":          h.tui.scrollbarDrag == nil,
		"pressed link":            h.tui.pressedURL == "" && !h.tui.pressedURLSet,
		"selection anchor":        h.tui.selectionAnchor == nil,
		"selection focus":         h.tui.selectionFocus == nil,
		"selection granularity":   h.tui.selectionGranularity == selectCharacter,
		"selection initial range": h.tui.selectionInitialRange == nil,
		"last click":              h.tui.lastClick == nil,
		"component gesture":       h.tui.mouseCapture == nil && h.tui.mousePressTarget == nil && h.tui.mousePressPoint == nil && !h.tui.mousePressMoved,
		"last component click":    h.tui.lastComponentClick == nil,
	} {
		if !ended {
			t.Errorf("%s survives the focus-out report", name)
		}
	}
}

// Without a held press the focus-out report leaves the selection, its granularity and range alone: a completed selection survives focus changes.
func TestAltScreenFocusOutKeepsACompletedSelectionState(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
	h.tui.Add(NewText("alpha\nbeta\ngamma\ndelta"))
	h.start()
	h.send("\x1b[<0;1;1M", "\x1b[<32;4;2M", "\x1b[<0;4;2m")
	h.tui.mu.Lock()
	h.tui.selectionGranularity, h.tui.selectionInitialRange = selectWord, &selectionRange{}
	h.tui.mu.Unlock()
	h.tui.HandleViewportInput(focusOutReport)
	h.tui.mu.Lock()
	defer h.tui.mu.Unlock()
	if h.tui.selectionAnchor == nil || h.tui.selectionFocus == nil || h.tui.selectionGranularity != selectWord || h.tui.selectionInitialRange == nil {
		t.Fatalf("a completed selection changed: anchor=%v focus=%v granularity=%q range=%v", h.tui.selectionAnchor, h.tui.selectionFocus, h.tui.selectionGranularity, h.tui.selectionInitialRange)
	}
}

// A click on either side of a focus-out report is two single clicks, for the transcript word counter and for a mouse-aware component's click count.
func TestAltScreenFocusOutRestartsClickCounts(t *testing.T) {
	t.Run("transcript words", func(t *testing.T) {
		h := newAltHarness(t, 20, 2, TuiAltScreenOptions{})
		h.tui.Add(NewText("alpha beta\ngamma"))
		h.start()
		h.send("\x1b[<0;2;1M", "\x1b[<0;2;1m", "\x1b[<0;2;1M")
		h.tui.mu.Lock()
		doubled := h.tui.selectionGranularity == selectWord
		h.tui.mu.Unlock()
		if !doubled {
			t.Fatal("precondition: a second click on the word selects it")
		}
		h.send("\x1b[<0;2;1m", focusOutReport, "\x1b[<0;2;1M")
		h.tui.mu.Lock()
		granularity := h.tui.selectionGranularity
		h.tui.mu.Unlock()
		if granularity != selectCharacter {
			t.Fatalf("granularity after a click that follows the focus-out report = %q, want %q", granularity, selectCharacter)
		}
	})
	t.Run("component clicks", func(t *testing.T) {
		h := newAltHarness(t, 20, 1, TuiAltScreenOptions{})
		var counts []int
		h.tui.Add(&funcMouseComponent{lines: []string{"control"}, handle: func(event TuiMouseEvent) *TuiMouseEventResult {
			switch event.Type {
			case MousePress:
				return &TuiMouseEventResult{Handled: true}
			case MouseClick:
				counts = append(counts, event.ClickCount)
				return &TuiMouseEventResult{Handled: true}
			}
			return nil
		}})
		h.start()
		h.send("\x1b[<0;1;1M", "\x1b[<0;1;1m", focusOutReport, "\x1b[<0;1;1M", "\x1b[<0;1;1m", "\x1b[<0;1;1M", "\x1b[<0;1;1m")
		if !slices.Equal(counts, []int{1, 1, 2}) {
			t.Fatalf("click counts = %v, want [1 1 2]", counts)
		}
	})
}

// A component that captured the pointer at a press receives nothing of the drag and the release that follow a focus-out report.
func TestAltScreenFocusOutReleasesTheComponentCapture(t *testing.T) {
	h := newAltHarness(t, 20, 2, TuiAltScreenOptions{})
	var events []TuiMouseEventType
	h.tui.Add(&funcMouseComponent{lines: []string{"control"}, handle: func(event TuiMouseEvent) *TuiMouseEventResult {
		events = append(events, event.Type)
		if event.Type == MousePress {
			return &TuiMouseEventResult{Handled: true, Capture: true}
		}
		return &TuiMouseEventResult{Handled: true}
	}})
	h.start()
	h.send("\x1b[<0;1;1M", focusOutReport, "\x1b[<32;5;2M", "\x1b[<0;5;2m")
	if slices.Contains(events, MouseDrag) || slices.Contains(events, MouseRelease) {
		t.Fatalf("a component sees %v after the focus-out report, want only its press", events)
	}
}

// The search box's hovered navigation button loses its hover on a focus-out report, and the frame is drawn again.
func TestAltScreenFocusOutClearsTheSearchButtonHover(t *testing.T) {
	useAltScreenBindings(t, nil)
	h := newAltHarness(t, 120, 6, TuiAltScreenOptions{
		SearchNavigationButtonStyle: func(text string, hovered bool) string {
			if hovered {
				return "\x1b[45m" + text + "\x1b[49m"
			}
			return "\x1b[44m" + text + "\x1b[49m"
		},
	})
	h.tui.Add(NewText("needle one\nmiddle\nneedle two\nend"))
	h.start()
	h.send("\x1b[102;6u", "needle")
	arrowRow := slices.IndexFunc(h.viewport(), func(row string) bool { return strings.Contains(row, "↑") && strings.Contains(row, "↓") })
	arrowColumn := columnOf(h.viewport()[arrowRow], "Enter", true)
	h.send(fmt.Sprintf("\x1b[<35;%d;%dM", arrowColumn+1, arrowRow+1))
	if !h.wrote("\x1b[45m↓ Enter\x1b[49m") {
		t.Fatal("precondition: the hovered next button is restyled")
	}
	h.out.Reset()
	h.tui.HandleViewportInput(focusOutReport)
	h.render()
	if !h.wrote("\x1b[44m↓ Enter\x1b[49m") || h.wrote("\x1b[45m") {
		t.Fatalf("the next button keeps its hover after the focus-out report: %q", h.out.String())
	}
}

// A focus-out report that drops a selection with extent requests a frame, so the highlight disappears; one that drops a press with no extent requests none.
func TestAltScreenFocusOutRequestsAFrameForANonEmptySelection(t *testing.T) {
	h := newAltHarness(t, 20, 4, TuiAltScreenOptions{})
	h.tui.Add(NewText("alpha\nbeta\ngamma\ndelta"))
	h.start()
	h.send("\x1b[<0;1;1M", "\x1b[<32;4;2M")
	renderScheduled(h.tui)
	h.tui.HandleViewportInput(focusOutReport)
	if !renderScheduled(h.tui) {
		t.Fatal("dropping a selection with extent requests a frame")
	}
	h.send("\x1b[<0;1;3M")
	renderScheduled(h.tui)
	h.tui.HandleViewportInput(focusOutReport)
	if renderScheduled(h.tui) {
		t.Fatal("dropping a press with no extent requests no frame")
	}
}
