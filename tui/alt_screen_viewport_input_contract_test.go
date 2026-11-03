package tui

// pi: packages/tui/src/tui-alt-screen.ts handleViewportInput (694-808)

import (
	"fmt"
	"strings"
	"testing"
)

// viewportContractKeys binds each tui.altScreen action to its own Ctrl+letter, so a key press (and its Kitty release report) names exactly one action.
var viewportContractKeys = map[TUIKeybinding]int{
	KBAltScreenPageUp: 'b', KBAltScreenPageDown: 'f', KBAltScreenHalfPageUp: 'u', KBAltScreenHalfPageDown: 'd',
	KBAltScreenLineUp: 'y', KBAltScreenLineDown: 'e', KBAltScreenPreviousPrompt: 'p', KBAltScreenNextPrompt: 'n',
	KBAltScreenTop: 't', KBAltScreenBottom: 'o', KBAltScreenSearch: 's', KBAltScreenSearchNext: 'g',
	KBAltScreenSearchPrevious: 'k', KBAltScreenSearchClose: 'q',
}

func viewportPress(code int) string   { return fmt.Sprintf("\x1b[%d;5u", code) }
func viewportRelease(code int) string { return fmt.Sprintf("\x1b[%d;5:3u", code) }

// viewportContractBindings returns the user bindings for the contract keys, with extra actions bound to the same key as an existing one.
func viewportContractBindings(shared map[TUIKeybinding]int) map[string][]string {
	out := map[string][]string{}
	for action, code := range viewportContractKeys {
		out[action] = []string{"ctrl+" + string(rune(code))}
	}
	for action, code := range shared {
		out[action] = []string{"ctrl+" + string(rune(code))}
	}
	return out
}

// viewportPromptDocument is sixty rows with an OSC 133 prompt-start mark every ten rows.
func viewportPromptDocument() string {
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = fmt.Sprintf("row %d", i)
		if i%10 == 0 {
			lines[i] = "\x1b]133;A\x07" + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func newViewportContractHarness(t *testing.T, height int, bindings map[string][]string) *altHarness {
	t.Helper()
	useAltScreenBindings(t, bindings)
	h := newAltHarness(t, 30, height, TuiAltScreenOptions{})
	h.tui.Add(NewText(viewportPromptDocument()))
	h.start()
	h.render()
	return h
}

func setViewportTop(h *altHarness, row int) {
	h.tui.getPrimaryScrollViewLocked().ScrollTo(row)
	h.render()
}

// The actions are matched in this order, as in the upstream handler.
var viewportActionOrder = []TUIKeybinding{
	KBAltScreenPageUp, KBAltScreenPageDown, KBAltScreenHalfPageUp, KBAltScreenHalfPageDown, KBAltScreenLineUp,
	KBAltScreenLineDown, KBAltScreenPreviousPrompt, KBAltScreenNextPrompt, KBAltScreenTop, KBAltScreenBottom,
}

// Each scroll action moves the primary scroll view by the amount handleViewportInput computes from the viewport height: a page is the height less four
// overlapping rows, a half page is the floor of half the height, each at least one row; the prompt jumps land on the OSC 133 marks; top and bottom
// go to the ends. A key press is consumed, so is its release, and the release moves nothing.
func TestAltScreenViewportKeysScrollByTheirAmounts(t *testing.T) {
	const start, maxRows = 25, 60
	for _, height := range []int{10, 6, 3, 1} {
		page, half := max(1, height-4), max(1, height/2)
		bottom := maxRows - height
		for _, tc := range []struct {
			action TUIKeybinding
			want   int
		}{
			{KBAltScreenPageUp, start - page}, {KBAltScreenPageDown, start + page},
			{KBAltScreenHalfPageUp, start - half}, {KBAltScreenHalfPageDown, start + half},
			{KBAltScreenLineUp, start - 1}, {KBAltScreenLineDown, start + 1},
			{KBAltScreenPreviousPrompt, 20}, {KBAltScreenNextPrompt, 30},
			{KBAltScreenTop, 0}, {KBAltScreenBottom, bottom},
		} {
			t.Run(fmt.Sprintf("%s/height=%d", tc.action, height), func(t *testing.T) {
				h := newViewportContractHarness(t, height, viewportContractBindings(nil))
				setViewportTop(h, start)
				code := viewportContractKeys[tc.action]
				if !h.tui.HandleViewportInput(viewportRelease(code)) || h.tui.ViewportTop() != start {
					t.Fatalf("release: consumed=%v top=%d, want consumed and top %d", false, h.tui.ViewportTop(), start)
				}
				if !h.tui.HandleViewportInput(viewportPress(code)) {
					t.Fatal("a viewport key press is consumed")
				}
				if got := h.tui.ViewportTop(); got != tc.want {
					t.Fatalf("top = %d, want %d", got, tc.want)
				}
				if !h.tui.HandleViewportInput(viewportRelease(code)) || h.tui.ViewportTop() != tc.want {
					t.Fatalf("release after the press: top=%d, want %d", h.tui.ViewportTop(), tc.want)
				}
			})
		}
	}
}

// The scroll actions stop at the ends of the transcript and at the first and last prompt marks, and a jump to the bottom follows the output again.
func TestAltScreenViewportKeysStopAtTheEnds(t *testing.T) {
	const height, rows = 10, 60
	for _, tc := range []struct {
		name   string
		from   int
		action TUIKeybinding
		want   int
	}{
		{"page up at the top", 0, KBAltScreenPageUp, 0}, {"half page up at the top", 2, KBAltScreenHalfPageUp, 0},
		{"line up at the top", 0, KBAltScreenLineUp, 0}, {"page down at the bottom", rows - height, KBAltScreenPageDown, rows - height},
		{"half page down at the bottom", rows - height - 2, KBAltScreenHalfPageDown, rows - height},
		{"line down at the bottom", rows - height, KBAltScreenLineDown, rows - height},
		{"previous prompt at the first mark", 0, KBAltScreenPreviousPrompt, 0}, {"previous prompt between marks", 15, KBAltScreenPreviousPrompt, 10},
		{"next prompt at the last mark", 50, KBAltScreenNextPrompt, 50}, {"next prompt from the first mark", 0, KBAltScreenNextPrompt, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newViewportContractHarness(t, height, viewportContractBindings(nil))
			setViewportTop(h, tc.from)
			h.tui.HandleViewportInput(viewportPress(viewportContractKeys[tc.action]))
			if got := h.tui.ViewportTop(); got != tc.want {
				t.Fatalf("top = %d, want %d", got, tc.want)
			}
		})
	}
	h := newViewportContractHarness(t, height, viewportContractBindings(nil))
	setViewportTop(h, 20)
	if h.tui.IsFollowingOutput() {
		t.Fatal("scrolling away from the end stops following")
	}
	h.tui.HandleViewportInput(viewportPress(viewportContractKeys[KBAltScreenBottom]))
	if !h.tui.IsFollowingOutput() || h.tui.ViewportTop() != rows-height {
		t.Fatalf("bottom: following=%v top=%d, want following at %d", h.tui.IsFollowingOutput(), h.tui.ViewportTop(), rows-height)
	}
}

// A key bound to two scroll actions runs the one matched first: every action beats each action after it.
func TestAltScreenViewportKeyBoundToTwoActionsRunsTheEarlierOne(t *testing.T) {
	const start, height = 27, 10
	observe := func(t *testing.T, action TUIKeybinding, shared map[TUIKeybinding]int) int {
		h := newViewportContractHarness(t, height, viewportContractBindings(shared))
		setViewportTop(h, start)
		h.tui.HandleViewportInput(viewportPress(viewportContractKeys[action]))
		return h.tui.ViewportTop()
	}
	for i, earlier := range viewportActionOrder {
		for _, later := range viewportActionOrder[i+1:] {
			t.Run(earlier+"+"+later, func(t *testing.T) {
				wantEarlier := observe(t, earlier, nil)
				if wantEarlier == observe(t, later, nil) {
					t.Fatalf("%s and %s scroll to the same row, so the order is not observable", earlier, later)
				}
				got := observe(t, earlier, map[TUIKeybinding]int{later: viewportContractKeys[earlier]})
				if got != wantEarlier {
					t.Fatalf("top = %d, want %d (the %s result)", got, wantEarlier, earlier)
				}
			})
		}
	}
}

// The search key is matched before the search navigation keys and the scroll keys, whatever else it is bound to, and it is matched while a
// component that captures viewport input has focus.
func TestAltScreenViewportSearchKeyComesFirst(t *testing.T) {
	h := newViewportContractHarness(t, 10, viewportContractBindings(map[TUIKeybinding]int{KBAltScreenPageUp: viewportContractKeys[KBAltScreenSearch], KBAltScreenSearchClose: viewportContractKeys[KBAltScreenSearch]}))
	setViewportTop(h, 25)
	search := viewportPress(viewportContractKeys[KBAltScreenSearch])
	if !h.tui.HandleViewportInput(search) || !h.tui.IsSearchFocused() || h.tui.ViewportTop() != 25 {
		t.Fatalf("search key: focused=%v top=%d, want the search box open and no scroll", h.tui.IsSearchFocused(), h.tui.ViewportTop())
	}
	if !h.tui.HandleViewportInput(viewportRelease(viewportContractKeys[KBAltScreenSearch])) || !h.tui.IsSearchFocused() {
		t.Fatal("the release of the search key is consumed and does not toggle the search box")
	}
	if !h.tui.HandleViewportInput(search) || h.tui.IsSearchFocused() {
		t.Fatal("the search key closes an open search box before the search close action could")
	}
}

// The search navigation keys act only while the search box has focus; the scroll key sharing their key scrolls otherwise. A release of any search key is consumed and does nothing.
func TestAltScreenViewportSearchKeysNeedTheFocusedSearchBox(t *testing.T) {
	const start = 25
	shared := map[TUIKeybinding]int{KBAltScreenLineDown: viewportContractKeys[KBAltScreenSearchNext]}
	h := newViewportContractHarness(t, 10, viewportContractBindings(shared))
	setViewportTop(h, start)
	next := viewportPress(viewportContractKeys[KBAltScreenSearchNext])
	if !h.tui.HandleViewportInput(next) || h.tui.ViewportTop() != start+1 {
		t.Fatalf("without a search box the shared key scrolls a line: top=%d, want %d", h.tui.ViewportTop(), start+1)
	}
	closeKey := viewportPress(viewportContractKeys[KBAltScreenSearchClose])
	if h.tui.HandleViewportInput(closeKey) {
		t.Fatal("search close with no search box is not consumed")
	}
	h.tui.HandleViewportInput(viewportPress(viewportContractKeys[KBAltScreenSearch]))
	if !h.tui.IsSearchFocused() {
		t.Fatal("the search key opens a focused search box")
	}
	before := h.tui.ViewportTop()
	if !h.tui.HandleViewportInput(next) || h.tui.ViewportTop() != before {
		t.Fatalf("with the search box focused the shared key navigates the search and does not scroll: top=%d, want %d", h.tui.ViewportTop(), before)
	}
	for _, action := range []TUIKeybinding{KBAltScreenSearchNext, KBAltScreenSearchPrevious, KBAltScreenSearchClose} {
		if !h.tui.HandleViewportInput(viewportRelease(viewportContractKeys[action])) || !h.tui.IsSearchFocused() {
			t.Fatalf("the release of %s is consumed and leaves the search box open", action)
		}
	}
	h.tui.activeSearchSnapshot().overlay.unfocus()
	if h.tui.IsSearchFocused() {
		t.Fatal("the search box lost focus")
	}
	before = h.tui.ViewportTop()
	if !h.tui.HandleViewportInput(next) || h.tui.ViewportTop() != before+1 {
		t.Fatalf("with the search box unfocused the shared key scrolls again: top=%d, want %d", h.tui.ViewportTop(), before+1)
	}
	h.tui.activeSearchSnapshot().overlay.focus()
	if !h.tui.HandleViewportInput(closeKey) || h.tui.IsSearchFocused() || h.tui.activeSearchSnapshot() != nil {
		t.Fatal("search close closes the focused search box")
	}
}

// Navigation keys belong to a focused component that captures viewport input and to a focused overlay other than the search box: the handler does not
// consume them there. The search key is still consumed, and once the search box has focus the navigation keys scroll again.
func TestAltScreenViewportNavigationDefersToCapturingFocusAndOverlays(t *testing.T) {
	press := func(action TUIKeybinding) string { return viewportPress(viewportContractKeys[action]) }
	t.Run("capturing component", func(t *testing.T) {
		h := newViewportContractHarness(t, 10, viewportContractBindings(nil))
		capturing := &capturingRecorder{}
		h.tui.SetFocus(capturing)
		setViewportTop(h, 25)
		for _, action := range viewportActionOrder {
			if h.tui.HandleViewportInput(press(action)) || h.tui.ViewportTop() != 25 {
				t.Fatalf("%s with a capturing component focused: consumed or scrolled to %d", action, h.tui.ViewportTop())
			}
		}
		if !h.tui.HandleViewportInput(press(KBAltScreenSearch)) || !h.tui.IsSearchFocused() {
			t.Fatal("the search key opens the search box over a capturing focus")
		}
		if !h.tui.HandleViewportInput(press(KBAltScreenLineDown)) || h.tui.ViewportTop() != 26 {
			t.Fatalf("with the search box focused the navigation keys scroll: top=%d, want 26", h.tui.ViewportTop())
		}
	})
	t.Run("focused overlay", func(t *testing.T) {
		h := newViewportContractHarness(t, 10, viewportContractBindings(nil))
		h.tui.ShowOverlay(&inputRecorder{lines: []string{"overlay"}}, OverlayOptions{})
		h.render()
		setViewportTop(h, 25)
		for _, action := range viewportActionOrder {
			if h.tui.HandleViewportInput(press(action)) || h.tui.ViewportTop() != 25 {
				t.Fatalf("%s with an overlay focused: consumed or scrolled to %d", action, h.tui.ViewportTop())
			}
		}
		if !h.tui.HandleViewportInput(press(KBAltScreenSearch)) || !h.tui.IsSearchFocused() {
			t.Fatal("the search key opens the search box over a focused overlay")
		}
	})
	t.Run("non-capturing focus", func(t *testing.T) {
		h := newViewportContractHarness(t, 10, viewportContractBindings(nil))
		h.tui.SetFocus(&inputRecorder{lines: []string{"editor"}})
		setViewportTop(h, 25)
		if !h.tui.HandleViewportInput(press(KBAltScreenLineDown)) || h.tui.ViewportTop() != 26 {
			t.Fatalf("a focused component that does not capture viewport input leaves the keys to the viewport: top=%d", h.tui.ViewportTop())
		}
	})
}

// capturingRecorder is a focused component that captures viewport input.
type capturingRecorder struct{ inputRecorder }

func (*capturingRecorder) CapturesViewportInput() bool { return true }

// A key that no viewport action is bound to, a release of one, and a search key with no search box are not consumed; focus reports and every mouse report are.
func TestAltScreenViewportConsumesFocusAndMouseReportsOnly(t *testing.T) {
	h := newViewportContractHarness(t, 10, viewportContractBindings(nil))
	setViewportTop(h, 25)
	for _, data := range []string{"x", "\x1b[120;1:3u", "\r", "\x1b[A", viewportPress('z'), viewportRelease('z')} {
		if h.tui.HandleViewportInput(data) {
			t.Errorf("%q is consumed, want it left to the focused component", data)
		}
	}
	for _, data := range []string{altFocusIn, "\x1b[<0;3;3M", "\x1b[<0;3;3m", "\x1b[<35;3;3M", "\x1b[M #!"} {
		if !h.tui.HandleViewportInput(data) {
			t.Errorf("%q is not consumed", data)
		}
	}
	if h.tui.ViewportTop() != 25 {
		t.Fatalf("unconsumed keys and non-wheel reports scrolled the transcript to %d", h.tui.ViewportTop())
	}
	if !h.tui.HandleViewportInput(altFocusOut) {
		t.Fatal("a focus-out report is consumed")
	}
}

// A wheel report is consumed and scrolls the transcript when nothing under the pointer takes it, a focused overlay that ignores it included. The
// scroll amount is the wheel's lines, multiplied when the report carries the Alt modifier bit.
func TestAltScreenViewportWheelScrollsTheTranscript(t *testing.T) {
	h := newViewportContractHarness(t, 10, viewportContractBindings(nil))
	setViewportTop(h, 25)
	if !h.tui.HandleViewportInput("\x1b[<65;3;3M") || h.tui.ViewportTop() <= 25 {
		t.Fatalf("wheel down: top=%d, want it to scroll down from 25", h.tui.ViewportTop())
	}
	after := h.tui.ViewportTop()
	if !h.tui.HandleViewportInput("\x1b[<64;3;3M") || h.tui.ViewportTop() >= after {
		t.Fatalf("wheel up: top=%d, want it to scroll up from %d", h.tui.ViewportTop(), after)
	}
	h.tui.ShowOverlay(&inputRecorder{lines: []string{"overlay"}}, OverlayOptions{anchor: overlayTopLeft, width: overlayCells(5)})
	h.render()
	before := h.tui.ViewportTop()
	if !h.tui.HandleViewportInput("\x1b[<65;20;8M") || h.tui.ViewportTop() <= before {
		t.Fatalf("wheel with a focused overlay: top=%d, want it to scroll down from %d", h.tui.ViewportTop(), before)
	}
}

// A key bound to two of the search navigation actions runs the one matched first: next, then previous, then close.
func TestAltScreenViewportSearchNavigationKeysAreMatchedInOrder(t *testing.T) {
	for _, tc := range []struct {
		earlier, later TUIKeybinding
		want           string
		open           bool
	}{
		{KBAltScreenSearchNext, KBAltScreenSearchPrevious, "2/3", true},
		{KBAltScreenSearchNext, KBAltScreenSearchClose, "2/3", true},
		{KBAltScreenSearchPrevious, KBAltScreenSearchClose, "3/3", true},
	} {
		t.Run(tc.earlier+"+"+tc.later, func(t *testing.T) {
			useAltScreenBindings(t, viewportContractBindings(map[TUIKeybinding]int{tc.later: viewportContractKeys[tc.earlier]}))
			h := newAltHarness(t, 60, 6, TuiAltScreenOptions{})
			h.tui.Add(NewText("needle a\nmiddle\nneedle b\nneedle c"))
			h.start()
			h.send(viewportPress(viewportContractKeys[KBAltScreenSearch]))
			h.tui.HandleFocusedSearchInput("needle")
			h.render()
			if !h.viewportHas("1/3") {
				t.Fatalf("the first match is selected: %q", h.viewport())
			}
			if !h.tui.HandleViewportInput(viewportPress(viewportContractKeys[tc.earlier])) {
				t.Fatal("the shared key is consumed")
			}
			h.render()
			if !h.tui.IsSearchFocused() || !h.viewportHas(tc.want) {
				t.Fatalf("focused=%v rows=%q, want the %s result %s", h.tui.IsSearchFocused(), h.viewport(), tc.earlier, tc.want)
			}
		})
	}
}

// A wheel report is offered to the overlays first and then, when no overlay covers the pointer, to the layout. An overlay that covers the pointer
// and ignores the wheel keeps it from the layout, so the transcript scrolls; away from the overlay a mouse-aware component takes the wheel.
func TestAltScreenViewportWheelOverlaysShadowTheLayout(t *testing.T) {
	h := newAltHarness(t, 20, 6, TuiAltScreenOptions{})
	wheels := 0
	h.tui.Add(NewMouseRegion(NewText(numberedLines(20)), func(event TuiMouseEvent) *TuiMouseEventResult {
		if event.Type != MouseWheel {
			return nil
		}
		wheels++
		return &TuiMouseEventResult{Handled: true}
	}))
	h.start()
	h.tui.ShowOverlay(&inputRecorder{lines: []string{"overlay"}}, OverlayOptions{anchor: overlayTopLeft, width: overlayCells(7)})
	h.render()
	top := h.tui.ViewportTop()
	if !h.tui.HandleViewportInput("\x1b[<64;2;1M") || wheels != 0 || h.tui.ViewportTop() >= top {
		t.Fatalf("wheel over the overlay: layout wheels=%d top %d -> %d, want the transcript scrolled and the layout not offered the wheel", wheels, top, h.tui.ViewportTop())
	}
	top = h.tui.ViewportTop()
	if !h.tui.HandleViewportInput("\x1b[<64;15;4M") || wheels != 1 || h.tui.ViewportTop() != top {
		t.Fatalf("wheel away from the overlay: layout wheels=%d top %d -> %d, want the component to take it", wheels, top, h.tui.ViewportTop())
	}
}
