package tui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// altRowAddress matches a per-row differential write intro: \x1b[<row>;1H\x1b[2K.
var altRowAddress = regexp.MustCompile("\x1b\\[(\\d+);1H\x1b\\[2K")

// altScreenFrame drives one render and returns the escape-stripped visible rows,
// so tests can assert frame geometry without matching raw control bytes.
func altScreenVisibleRows(t *testing.T, out *bytes.Buffer, width, height int) []string {
	t.Helper()
	raw := out.String()
	rows := make([]string, height)
	locs := altRowAddress.FindAllStringSubmatchIndex(raw, -1)
	for i, loc := range locs {
		// Row number from the capture group.
		rowNum := 0
		for _, c := range raw[loc[2]:loc[3]] {
			rowNum = rowNum*10 + int(c-'0')
		}
		if rowNum < 1 || rowNum > height {
			continue
		}
		// Content runs from the end of this intro to the start of the next
		// row-address (or end of the buffer for the last row).
		end := len(raw)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		content := raw[loc[1]:end]
		// Trim a trailing cursor-position / synchronized-output tail on the last row.
		if k := strings.Index(content, "\x1b[?25"); k >= 0 {
			content = content[:k]
		}
		rows[rowNum-1] = widthx.StripTerminalSequences(content)
	}
	return rows
}

// newAltScreenForTest builds a fixed-size alt-screen with a no-op render
// dispatcher so timer-scheduled renders (e.g. from a flash's auto-remove timer)
// are dropped instead of painting on a background goroutine. Tests drive frames
// explicitly via Start()/Render(); production wires a real dispatcher (layer 8).
func newAltScreenForTest(out *bytes.Buffer, width, height int, options TuiAltScreenOptions) *TuiAltScreen {
	tui := NewTuiAltScreenWithOutput(out, width, height, options)
	tui.SetRenderDispatcher(func(func()) {})
	return tui
}

func TestAltScreenRendersFullHeightFrame(t *testing.T) {
	var out bytes.Buffer
	width, height := 40, 10
	tui := newAltScreenForTest(&out, width, height, TuiAltScreenOptions{})
	tui.Add(NewText("hello"))
	tui.Add(NewText("world"))
	tui.Start()

	rows := altScreenVisibleRows(t, &out, width, height)
	// The frame paints all `height` rows (empty rows are addressed + cleared).
	painted := 0
	for _, r := range rows {
		if r != "" {
			painted++
		}
	}
	if painted == 0 {
		t.Fatalf("expected a painted frame, got all-empty rows: %q", rows)
	}
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "hello") || !strings.Contains(joined, "world") {
		t.Errorf("frame missing content; rows=%q", rows)
	}
	tui.StopWithOptions(StopOptions{PreserveScreen: true})
}

func TestAltScreenEntersAndExitsAltBuffer(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 20, 5, TuiAltScreenOptions{})
	tui.Add(NewText("x"))
	tui.Start()
	if !strings.Contains(out.String(), altEnterAltScreen) {
		t.Error("Start should enter the alternate screen (\\x1b[?1049h)")
	}
	out.Reset()
	tui.StopWithOptions(StopOptions{PreserveScreen: true})
	if !strings.Contains(out.String(), altExitAltScreen) {
		t.Error("Stop should exit the alternate screen (\\x1b[?1049l)")
	}
}

func TestAltScreenDifferentialSkipsUnchangedRows(t *testing.T) {
	var out bytes.Buffer
	width, height := 20, 6
	tui := newAltScreenForTest(&out, width, height, TuiAltScreenOptions{})
	first := NewText("line-a")
	tui.Add(first)
	tui.Add(NewText("line-b"))
	tui.Start() // full redraw
	out.Reset()
	// Change only the first child; the differential render must not repaint
	// the unchanged row for line-b.
	first.Content = "line-A"
	first.Invalidate()
	tui.Render()
	frame := out.String()
	// A second-frame differential write addresses row 1 (line-a changed) but not
	// the row carrying the unchanged "line-b".
	if !strings.Contains(frame, "line-A") {
		t.Errorf("changed row not repainted; frame=%q", frame)
	}
	if strings.Contains(frame, "line-b") {
		t.Errorf("unchanged row should not be repainted; frame=%q", frame)
	}
}

func TestAltScreenScrollAndFollow(t *testing.T) {
	var out bytes.Buffer
	width, height := 20, 4
	tui := newAltScreenForTest(&out, width, height, TuiAltScreenOptions{})
	for range 20 {
		tui.Add(NewText("row"))
	}
	tui.Start()
	if !tui.IsFollowingOutput() {
		t.Error("a fresh alt-screen should follow output (pinned to end)")
	}
	tui.ScrollToTop()
	if tui.ViewportTop() != 0 {
		t.Errorf("ScrollToTop should set viewportTop to 0, got %d", tui.ViewportTop())
	}
	if tui.IsFollowingOutput() {
		t.Error("after ScrollToTop the view should not follow output")
	}
	tui.ScrollToBottom()
	if !tui.IsFollowingOutput() {
		t.Error("after ScrollToBottom the view should follow output again")
	}
}

func TestAltScreenFlashComposited(t *testing.T) {
	var out bytes.Buffer
	width, height := 30, 6
	tui := newAltScreenForTest(&out, width, height, TuiAltScreenOptions{})
	tui.Add(NewText("body"))
	tui.Start()
	out.Reset()
	tui.Flash("Copied!", 1000)
	tui.Render()
	rows := altScreenVisibleRows(t, &out, width, height)
	if !strings.Contains(strings.Join(rows, "\n"), "Copied!") {
		t.Errorf("flash message not composited into frame; rows=%q", rows)
	}
}

func TestAltScreenModeAndViewportInterface(t *testing.T) {
	var tui ViewportTUI = NewTuiAltScreenWithOutput(&bytes.Buffer{}, 10, 3, TuiAltScreenOptions{})
	if tui.Mode() != "fullscreen" {
		t.Errorf("Mode() = %q, want fullscreen", tui.Mode())
	}
}

func TestAltScreenPrepareKittyScreenReplacesUploadedImage(t *testing.T) {
	tui := newAltScreenForTest(&bytes.Buffer{}, 40, 10, TuiAltScreenOptions{})
	RegisterKittyImageMetadata(KittyImageMetadata{ImageID: 77, Columns: 4, Rows: 2, WidthPx: 40, HeightPx: 20})
	line := "\x1b_Gi=77,a=T,w=40,h=20;DATA\x1b\\"

	// First pass: image not yet uploaded -> transmitted verbatim, cached.
	lines, evicted := tui.prepareKittyScreen([]string{line})
	if lines[0] != line {
		t.Errorf("first pass should transmit verbatim, got %q", lines[0])
	}
	if evicted != "" {
		t.Errorf("first pass should evict nothing, got %q", evicted)
	}
	if _, ok := tui.uploadedKittyImages[77]; !ok {
		t.Fatal("image 77 should be cached after first pass")
	}

	// Second pass, same generation: re-placed instead of re-transmitted.
	placement, _ := GetKittyImagePlacement(line)
	lines2, _ := tui.prepareKittyScreen([]string{line})
	if lines2[0] != placement.ReplacementLine {
		t.Errorf("second pass should re-place; got %q want %q", lines2[0], placement.ReplacementLine)
	}
}

// BUG A regression: exiting fullscreen (non-preserve) must dump the FULL
// transcript into scrollback, not just the visible viewport height.
func TestAltScreenExitDumpsFullTranscriptNotViewport(t *testing.T) {
	var out bytes.Buffer
	width, height := 20, 4 // viewport only 4 rows tall
	tui := newAltScreenForTest(&out, width, height, TuiAltScreenOptions{})
	for i := range 30 {
		tui.Add(NewText(fmt.Sprintf("line-%02d", i)))
	}
	tui.Start()
	out.Reset()
	tui.Stop() // non-preserve: reflow document into scrollback
	dumped := out.String()
	// The whole transcript (line-00 .. line-29) must appear, not just the last 4.
	for _, want := range []string{"line-00", "line-15", "line-29"} {
		if !strings.Contains(dumped, want) {
			t.Errorf("exit dump missing %q (transcript truncated to viewport?)", want)
		}
	}
}

// BUG B regression: entering fullscreen on iTerm2 suppresses inline images
// (they don't render in the alt buffer); exiting restores the capability.
func TestAltScreenSuppressesITerm2ImagesWhileActive(t *testing.T) {
	prev := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(prev) })
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true})

	tui := newAltScreenForTest(&bytes.Buffer{}, 20, 5, TuiAltScreenOptions{})
	tui.Add(NewText("x"))
	tui.Start()
	if got := GetCapabilities().Images; got != "" {
		t.Errorf("iTerm2 images should be suppressed while alt-screen active, got %q", got)
	}
	tui.StopWithOptions(StopOptions{PreserveScreen: true})
	if got := GetCapabilities().Images; got != ImageProtocolITerm2 {
		t.Errorf("iTerm2 images should be restored after Stop, got %q", got)
	}
	// TrueColor must round-trip through the save/restore.
	if !GetCapabilities().TrueColor {
		t.Error("restored capabilities should preserve TrueColor")
	}
}

// BUG C regression: the alt-screen doRender must stamp lastRenderAt so the
// shared 16ms render throttle applies (a subsequent RequestRender is delayed).
func TestAltScreenRenderStampsThrottleTimestamp(t *testing.T) {
	tui := newAltScreenForTest(&bytes.Buffer{}, 20, 5, TuiAltScreenOptions{})
	// Fixed clock so the throttle math is deterministic.
	base := timeNowFixed()
	tui.now = func() time.Time { return base }
	var capturedDelay time.Duration
	captured := false
	tui.afterFunc = func(d time.Duration, fn func()) stoppableTimer {
		if !captured {
			capturedDelay = d
			captured = true
		}
		return noopTimer{}
	}
	tui.Add(NewText("x"))
	tui.Start() // Start -> Render -> doRender stamps lastRenderAt = base
	if tui.lastRenderAt.IsZero() {
		t.Fatal("doRender must stamp lastRenderAt for the render throttle")
	}
	// A RequestRender at the same instant must schedule with the full min interval.
	captured = false
	tui.requestRender(false)
	if !captured {
		t.Fatal("RequestRender should schedule a throttled frame")
	}
	if capturedDelay < minRenderInterval-time.Millisecond {
		t.Errorf("throttle defeated: delay %v, want ~%v", capturedDelay, minRenderInterval)
	}
}

type noopTimer struct{}

func (noopTimer) Stop() bool { return true }

func timeNowFixed() time.Time { return time.Unix(1_700_000_000, 0) }

func TestAltScreenWheelScrolls(t *testing.T) {
	tui := newAltScreenForTest(&bytes.Buffer{}, 20, 4, TuiAltScreenOptions{})
	for range 20 {
		tui.Add(NewText("row"))
	}
	tui.Start() // follows end -> scrolled to bottom
	top0 := tui.ViewportTop()
	if top0 == 0 {
		t.Fatalf("expected non-zero scroll at end for tall content, got %d", top0)
	}
	// Wheel up scrolls the transcript up and is consumed (never reaches editor).
	if !tui.HandleViewportInput("\x1b[<64;5;5M") {
		t.Fatal("wheel-up should be consumed by the viewport")
	}
	if tui.ViewportTop() >= top0 {
		t.Errorf("wheel-up should decrease ViewportTop: %d -> %d", top0, tui.ViewportTop())
	}
	// Wheel down scrolls back toward the end.
	up := tui.ViewportTop()
	tui.HandleViewportInput("\x1b[<65;5;5M")
	if tui.ViewportTop() <= up {
		t.Errorf("wheel-down should increase ViewportTop: %d -> %d", up, tui.ViewportTop())
	}
}

func TestAltScreenConsumesMouseNotKeys(t *testing.T) {
	tui := newAltScreenForTest(&bytes.Buffer{}, 20, 5, TuiAltScreenOptions{})
	tui.Add(NewText("x"))
	tui.Start()
	// Non-wheel mouse reports (click) and focus events are consumed so raw SGR
	// bytes never get inserted into the editor.
	for _, seq := range []string{"\x1b[<0;3;3M", "\x1b[<0;3;3m", "\x1b[I", "\x1b[O"} {
		if !tui.HandleViewportInput(seq) {
			t.Errorf("viewport should consume %q", seq)
		}
	}
	// Ordinary keystrokes are NOT consumed; the driver dispatches them.
	for _, seq := range []string{"a", "\r", "\x1b[A"} {
		if tui.HandleViewportInput(seq) {
			t.Errorf("viewport must not consume keystroke %q", seq)
		}
	}
}

// --- Layer 7c-A: selection render side ---

func TestAltScreenGetSelectionBounds(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 40, 10, TuiAltScreenOptions{})
	sv := NewScrollView(NewText("x"), ScrollViewOptions{})
	t.Cleanup(sv.Dispose)

	// No anchor/focus -> no bounds.
	if _, _, ok := tui.getSelectionBounds(); ok {
		t.Fatalf("empty selection should have no bounds")
	}
	// Coincident anchor/focus -> no bounds.
	tui.selectionAnchor = &selectionPoint{row: 2, col: 3}
	tui.selectionFocus = &selectionPoint{row: 2, col: 3}
	if _, _, ok := tui.getSelectionBounds(); ok {
		t.Fatalf("zero-width selection should have no bounds")
	}
	// Different scroll views -> no bounds.
	tui.selectionAnchor = &selectionPoint{row: 0, col: 0, scrollView: sv}
	tui.selectionFocus = &selectionPoint{row: 1, col: 0}
	if _, _, ok := tui.getSelectionBounds(); ok {
		t.Fatalf("cross-view selection should have no bounds")
	}
	// Anchor after focus -> ordered start<end.
	tui.selectionAnchor = &selectionPoint{row: 3, col: 5}
	tui.selectionFocus = &selectionPoint{row: 1, col: 2}
	start, end, ok := tui.getSelectionBounds()
	if !ok || start.row != 1 || start.col != 2 || end.row != 3 || end.col != 5 {
		t.Fatalf("ordering failed: start=%+v end=%+v ok=%v", start, end, ok)
	}
	// Same row, anchor col before focus col -> anchor is start.
	tui.selectionAnchor = &selectionPoint{row: 2, col: 1}
	tui.selectionFocus = &selectionPoint{row: 2, col: 6}
	start, end, ok = tui.getSelectionBounds()
	if !ok || start.col != 1 || end.col != 6 {
		t.Fatalf("same-row ordering failed: start=%+v end=%+v", start, end)
	}
}

func TestAltScreenApplySelectionHighlightReassertsAfterReset(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 40, 10, TuiAltScreenOptions{})
	// Plain text is wrapped in reverse-video and closed with SGR 27.
	got := tui.applySelectionHighlight("ab")
	if got != "\x1b[7mab\x1b[27m" {
		t.Fatalf("plain highlight = %q", got)
	}
	// An embedded SGR reset must re-assert reverse-video so the tail stays lit.
	got = tui.applySelectionHighlight("a\x1b[0mb")
	if got != "\x1b[7ma\x1b[0m\x1b[7mb\x1b[27m" {
		t.Fatalf("reset re-assert = %q", got)
	}
}

func TestAltScreenApplySelectionScreenCoordinates(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 40, 10, TuiAltScreenOptions{})
	screen := []string{"hello world", "second line", "third row"}
	// Select columns 2..7 on row 0 through column 4 on row 1 (absolute coords).
	tui.selectionAnchor = &selectionPoint{row: 0, col: 2}
	tui.selectionFocus = &selectionPoint{row: 1, col: 4}
	got := tui.applySelection(append([]string(nil), screen...), nil)

	// Row 0: highlight starts at col 2 ("llo world"), row 1: up to col 4 (grapheme end 5).
	if !strings.Contains(got[0], "\x1b[7m") {
		t.Fatalf("row 0 should be highlighted: %q", got[0])
	}
	// Content before the selection stays unhighlighted at the start of row 0.
	if !strings.HasPrefix(got[0], "he") {
		t.Fatalf("row 0 prefix should be plain, got %q", got[0])
	}
	// Row 2 is outside the selection -> unchanged.
	if got[2] != screen[2] {
		t.Fatalf("row 2 should be untouched, got %q", got[2])
	}
	// The visible text is preserved once escapes are stripped.
	if widthx.StripTerminalSequences(got[0]) != "hello world" {
		t.Fatalf("row 0 visible text altered: %q", widthx.StripTerminalSequences(got[0]))
	}
}

func TestAltScreenApplySelectionSkipsImageLines(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 40, 10, TuiAltScreenOptions{})
	// An image line with visible width ("IMGTEXT" = 7 cells) whose selected span
	// is non-empty, so only the IsImageLine guard prevents the highlight.
	image := "IMGTEXT\x1b_Ga=p,i=1\x1b\\"
	if widthx.VisibleWidth(image) == 0 || !widthx.IsImageLine(image) {
		t.Fatalf("fixture must be a non-zero-width image line")
	}
	screen := []string{"text row", image}
	tui.selectionAnchor = &selectionPoint{row: 0, col: 0}
	tui.selectionFocus = &selectionPoint{row: 1, col: 5}
	got := tui.applySelection(append([]string(nil), screen...), nil)
	if got[1] != image {
		t.Fatalf("image line must not be highlighted, got %q", got[1])
	}
	// Guard: row 0 (a normal line in range) IS highlighted, proving the selection
	// reached row 1 and the image line was skipped, not merely out of range.
	if !strings.Contains(got[0], "\x1b[7m") {
		t.Fatalf("row 0 should be highlighted (selection spans into row 1): %q", got[0])
	}
}

func TestAltScreenApplySelectionNoBoundsReturnsUnchanged(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 40, 10, TuiAltScreenOptions{})
	screen := []string{"a", "b"}
	got := tui.applySelection(append([]string(nil), screen...), nil)
	if got[0] != "a" || got[1] != "b" {
		t.Fatalf("no selection must return screen unchanged, got %q", got)
	}
}

// --- Layer 7c-B1: mouse event parsing + selection point mapping ---

func TestParseSgrMouseEvent(t *testing.T) {
	// Press button 0 at col 5 row 3 (1-based in the wire, zero-based decoded).
	ev, ok := parseSgrMouseEvent("\x1b[<0;5;3M")
	if !ok || ev.button != 0 || ev.x != 4 || ev.y != 2 || ev.release {
		t.Fatalf("press decode = %+v ok=%v", ev, ok)
	}
	// Release ('m' final byte).
	ev, ok = parseSgrMouseEvent("\x1b[<0;10;7m")
	if !ok || ev.x != 9 || ev.y != 6 || !ev.release {
		t.Fatalf("release decode = %+v ok=%v", ev, ok)
	}
	// Drag button (32|0).
	ev, ok = parseSgrMouseEvent("\x1b[<32;2;2M")
	if !ok || ev.button != 32 {
		t.Fatalf("drag button decode = %+v ok=%v", ev, ok)
	}
	// Non-mouse input.
	if _, ok := parseSgrMouseEvent("abc"); ok {
		t.Fatalf("non-mouse input should not parse")
	}
	if _, ok := parseSgrMouseEvent("\x1b[A"); ok {
		t.Fatalf("arrow key should not parse as mouse")
	}
}

func TestAltScreenGetSelectionPointScreenFallback(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 20, 8, TuiAltScreenOptions{})
	// No scroll view -> absolute screen coords, clamped to bounds.
	p := tui.getSelectionPoint(sgrMouseEvent{x: 5, y: 3}, nil)
	if p.row != 3 || p.col != 5 || p.scrollView != nil {
		t.Fatalf("screen point = %+v", p)
	}
	// Beyond bounds -> clamped to width-1 / height-1.
	p = tui.getSelectionPoint(sgrMouseEvent{x: 99, y: 99}, nil)
	if p.row != 7 || p.col != 19 {
		t.Fatalf("clamped point = %+v, want row 7 col 19", p)
	}
	// Negative -> clamped to 0.
	p = tui.getSelectionPoint(sgrMouseEvent{x: -3, y: -1}, nil)
	if p.row != 0 || p.col != 0 {
		t.Fatalf("negative clamp = %+v", p)
	}
}

func TestAltScreenGetScrollSelectionPointMapsContentRow(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 20, 6, TuiAltScreenOptions{})
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%02d", i)
	}
	sv := NewScrollView(&stubComponent{lines: lines}, ScrollViewOptions{Follow: "end"})
	t.Cleanup(sv.Dispose)
	frame := RenderLayoutFrame(sv, 20, 6, noRender)
	tui.currentLayout = &frame
	tui.width, tui.height = 20, 6

	// Follow=end pins scrollTop to the bottom; a pointer at the top visible row
	// maps to scrollTop + (row - rect.Y). With 20 lines of content in a height-6
	// viewport the view is scrolled well past the top, so the top visible row
	// must map to a content row reflecting that offset (not row 0).
	sv.UpdateLayout(len(lines), 6, noRender)
	wantScrollTop := sv.ScrollTop()
	if wantScrollTop < 10 {
		t.Fatalf("fixture must be scrolled past the top, scrollTop=%d", wantScrollTop)
	}
	p := tui.getScrollSelectionPoint(sv, 2, 0)
	if p == nil {
		t.Fatalf("expected a scroll selection point")
	}
	if p.scrollView != sv {
		t.Fatalf("point should carry the scroll view")
	}
	if p.col != 2 {
		t.Fatalf("col = %d, want 2", p.col)
	}
	// The top visible row maps to scrollTop + (0 - rect.Y); dropping scrollTop
	// would map it to ~0, so require it to reflect the scroll offset.
	if p.row < wantScrollTop-1 {
		t.Fatalf("top row content = %d, want ~scrollTop %d (scrollTop offset not applied)", p.row, wantScrollTop)
	}
	if p.row > len(lines)-1 {
		t.Fatalf("content row %d out of [0,%d]", p.row, len(lines)-1)
	}
	// A pointer below the viewport clamps to the last visible row's content, not
	// past content length.
	p2 := tui.getScrollSelectionPoint(sv, 2, 99)
	if p2 == nil || p2.row > len(lines)-1 {
		t.Fatalf("below-viewport point = %+v", p2)
	}
	if p2.row < p.row {
		t.Fatalf("lower pointer should map to a >= content row (%d vs %d)", p2.row, p.row)
	}
}

// --- Layer 7c-B2: selection input state machine + auto-scroll + scrollbar ---

func TestAltScreenSelectionPressDragReleaseCopies(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 40, 8, TuiAltScreenOptions{})
	for i := range 6 {
		tui.Add(NewText(fmt.Sprintf("row-%d-content", i)))
	}
	tui.Start()
	out.Reset()

	// Press at (col 2, row 1), drag to (col 8, row 2), release.
	if !tui.HandleViewportInput("\x1b[<0;3;2M") {
		t.Fatal("press should be consumed")
	}
	if tui.selectionAnchor == nil || !tui.selectionPressActive {
		t.Fatalf("press should set anchor + pressActive: anchor=%v active=%v", tui.selectionAnchor, tui.selectionPressActive)
	}
	tui.HandleViewportInput("\x1b[<32;9;3M") // drag (button|32)
	if !tui.selectionDragged || tui.selectionFocus == nil {
		t.Fatalf("drag should set dragged + focus: dragged=%v focus=%v", tui.selectionDragged, tui.selectionFocus)
	}
	tui.HandleViewportInput("\x1b[<0;9;3m") // release ('m')
	if tui.selectionPressActive {
		t.Fatalf("release should clear pressActive")
	}
	// A non-empty selection copies to the clipboard via OSC 52.
	if !strings.Contains(out.String(), "\x1b]52;c;") {
		t.Fatalf("release with a selection should emit an OSC 52 clipboard write; out=%q", out.String())
	}
}

func TestAltScreenReleaseWithoutPressIsNoop(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 40, 8, TuiAltScreenOptions{})
	tui.Add(NewText("hello"))
	tui.Start()
	out.Reset()
	// Release with no prior press: consumed, but no clipboard write.
	tui.HandleViewportInput("\x1b[<0;5;2m")
	if strings.Contains(out.String(), "\x1b]52;c;") {
		t.Fatalf("release without press must not copy; out=%q", out.String())
	}
}

func TestAltScreenFocusOutClearsActiveSelection(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 40, 8, TuiAltScreenOptions{})
	tui.Add(NewText("hello world"))
	tui.Start()
	// Start a press so there is an active selection.
	tui.HandleViewportInput("\x1b[<0;3;2M")
	if tui.selectionAnchor == nil {
		t.Fatalf("precondition: press should anchor")
	}
	// Focus-out with an active press clears the selection.
	tui.HandleViewportInput("\x1b[O")
	if tui.selectionAnchor != nil || tui.selectionFocus != nil || tui.selectionPressActive {
		t.Fatalf("focus-out should clear active selection: anchor=%v focus=%v active=%v",
			tui.selectionAnchor, tui.selectionFocus, tui.selectionPressActive)
	}
}

func TestAltScreenSelectionAutoScrollArmsAndStops(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 20, 4, TuiAltScreenOptions{})
	for i := range 30 {
		tui.Add(NewText(fmt.Sprintf("line-%02d", i)))
	}
	tui.Start()
	// Press inside the viewport to anchor in the scroll view.
	tui.HandleViewportInput("\x1b[<0;3;2M")
	if tui.selectionAnchor == nil || tui.selectionAnchor.scrollView == nil {
		t.Fatalf("press must anchor in the scroll view: %v", tui.selectionAnchor)
	}
	// Drag below the viewport bottom edge -> auto-scroll timer arms (dir=+1).
	tui.HandleViewportInput("\x1b[<32;5;99M")
	tui.mu.Lock()
	armed := tui.selectionAutoScrollTimer != nil
	dir := tui.selectionAutoScrollDir
	tui.mu.Unlock()
	if !armed || dir != 1 {
		t.Fatalf("drag past bottom edge should arm auto-scroll dir=+1: armed=%v dir=%d", armed, dir)
	}
	// StopWithOptions must stop the auto-scroll timer (no goroutine leak).
	tui.StopWithOptions(StopOptions{PreserveScreen: true})
	tui.mu.Lock()
	stillArmed := tui.selectionAutoScrollTimer != nil
	tui.mu.Unlock()
	if stillArmed {
		t.Fatalf("teardown must stop the auto-scroll timer")
	}
}

// --- Layer 7c-C: keyboard viewport scroll + OSC 133 prompt navigation ---

func TestAltScreenKeyboardPageAndEdgeScroll(t *testing.T) {
	tui := newAltScreenForTest(&bytes.Buffer{}, 20, 6, TuiAltScreenOptions{})
	for range 40 {
		tui.Add(NewText("row"))
	}
	tui.Start() // follows end -> scrolled to bottom
	bottom := tui.ViewportTop()
	if bottom == 0 {
		t.Fatalf("tall content should start scrolled to the bottom")
	}
	// Page up scrolls up by ~a page and is consumed.
	if !tui.HandleViewportInput("\x1b[5~") {
		t.Fatal("pageUp should be consumed")
	}
	afterPageUp := tui.ViewportTop()
	if afterPageUp >= bottom {
		t.Errorf("pageUp should decrease ViewportTop: %d -> %d", bottom, afterPageUp)
	}
	// Home jumps to the very top.
	tui.HandleViewportInput("\x1b[H")
	if tui.ViewportTop() != 0 {
		t.Errorf("home should scroll to top, got %d", tui.ViewportTop())
	}
	// Page down scrolls back down from the top.
	tui.HandleViewportInput("\x1b[6~")
	if tui.ViewportTop() == 0 {
		t.Errorf("pageDown from top should scroll down")
	}
	// End jumps back to the bottom.
	tui.HandleViewportInput("\x1b[F")
	if tui.ViewportTop() != bottom {
		t.Errorf("end should scroll to bottom %d, got %d", bottom, tui.ViewportTop())
	}
}

func TestAltScreenScrollToPromptJumpsToOsc133Marker(t *testing.T) {
	tui := newAltScreenForTest(&bytes.Buffer{}, 20, 6, TuiAltScreenOptions{})
	// Content with OSC 133;A prompt-start marks at rows 4 and 12.
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%02d", i)
	}
	lines[4] = "\x1b]133;A\x07prompt-4"
	lines[12] = "\x1b]133;A\x07prompt-12"
	tui.Add(&stubComponent{lines: lines})
	tui.Start()
	tui.ScrollToTop()
	if tui.ViewportTop() != 0 {
		t.Fatalf("precondition: scrolled to top, got %d", tui.ViewportTop())
	}
	// Next prompt jumps to the first marker below the top (row 4). ctrl+down is
	// bound on every platform; the Windows column drops ctrl+shift+down, as
	// upstream coding-agent KEYBINDINGS.
	tui.HandleViewportInput("\x1b[1;5B")
	if tui.ViewportTop() != 4 {
		t.Fatalf("nextPrompt should jump to marker row 4, got %d", tui.ViewportTop())
	}
	// Next prompt again jumps to the following marker (row 12).
	tui.HandleViewportInput("\x1b[1;5B")
	if tui.ViewportTop() != 12 {
		t.Fatalf("nextPrompt should advance to marker row 12, got %d", tui.ViewportTop())
	}
	// Previous prompt (ctrl+up) jumps back to row 4.
	tui.HandleViewportInput("\x1b[1;5A")
	if tui.ViewportTop() != 4 {
		t.Fatalf("previousPrompt should jump back to marker row 4, got %d", tui.ViewportTop())
	}
}

// renderScheduled reports whether a render is pending (set by RequestRender's
// coalescing path) and resets the flag, so a test can check per-event whether a
// mouse event scheduled a repaint.
func renderScheduled(t *TuiAltScreen) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	was := t.renderRequested
	t.renderRequested = false
	return was
}

// TestAltScreenInertMotionDoesNotRender pins the upstream contract that inert
// pointer motion (mode 1003h reports all motion) does not repaint, while a
// selection press and drag do. An unconditional RequestRender after every mouse
// event repainted the whole transcript ~60fps on any mouse move.
func TestAltScreenInertMotionDoesNotRender(t *testing.T) {
	tui := newAltScreenForTest(&bytes.Buffer{}, 40, 10, TuiAltScreenOptions{})
	for range 20 {
		tui.Add(NewText("row"))
	}
	tui.Start()
	renderScheduled(tui) // clear any render from Start

	// Inert motion: SGR button 35 = motion(32) + no-button(3), no press active.
	tui.HandleViewportInput("\x1b[<35;5;5M")
	if renderScheduled(tui) {
		t.Error("inert mouse motion must not schedule a render")
	}

	// Left press begins a selection: must render.
	tui.HandleViewportInput("\x1b[<0;5;5M")
	if !renderScheduled(tui) {
		t.Error("selection press must schedule a render")
	}

	// Left drag (button 32 = motion + left, press active): must render.
	tui.HandleViewportInput("\x1b[<32;6;6M")
	if !renderScheduled(tui) {
		t.Error("selection drag must schedule a render")
	}

	// A second inert motion after the drag released state: still no render.
	tui.HandleViewportInput("\x1b[<0;6;6m") // release
	renderScheduled(tui)                    // release renders (copy); clear it
	tui.HandleViewportInput("\x1b[<35;8;8M")
	if renderScheduled(tui) {
		t.Error("inert motion after release must not schedule a render")
	}
}

// TestAltScreenAutoScrollCancelledMidScrollDoesNotResurrect drives the exact
// lifecycle race the generation token guards: an auto-scroll tick reads state,
// unlocks to ScrollBy, and while it is scrolling a release/focus-out/stop
// cancels the selection. The stale callback must not re-arm the timer or
// resurrect the cleared selectionFocus. The cancellation is injected
// deterministically from inside ScrollBy via the ScrollView's render callback.
func TestAltScreenAutoScrollCancelledMidScrollDoesNotResurrect(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel func(tui *TuiAltScreen)
	}{
		{"stop", func(tui *TuiAltScreen) { tui.stopSelectionAutoScrollLocked() }},
		{"release-clears-anchor", func(tui *TuiAltScreen) {
			tui.stopSelectionAutoScrollLocked()
			tui.selectionAnchor = nil
			tui.selectionFocus = nil
		}},
		{"stopped-flag", func(tui *TuiAltScreen) {
			tui.stopped = true
			tui.stopSelectionAutoScrollLocked()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tui := newAltScreenForTest(&bytes.Buffer{}, 40, 10, TuiAltScreenOptions{})
			sv := NewScrollView(NewText("x"), ScrollViewOptions{})
			// Content taller than the viewport, scrolled to the top so ScrollBy(+1)
			// actually moves and fires the render callback.
			injected := false
			sv.UpdateLayout(100, 6, func() {
				// Simulate a cancel arriving on the owner loop mid-ScrollBy. Runs
				// while autoScrollSelection has released t.mu.
				if injected {
					return
				}
				injected = true
				tui.mu.Lock()
				tc.cancel(tui)
				tui.mu.Unlock()
			})
			sv.ScrollTo(0)

			tui.mu.Lock()
			anchor := selectionPoint{row: 0, col: 0, scrollView: sv}
			tui.selectionAnchor = &anchor
			tui.selectionDragPointer = &pointerXY{x: 1, y: 9}
			tui.selectionAutoScrollDir = 1
			gen := tui.selectionAutoScrollGen
			tui.mu.Unlock()

			tui.autoScrollSelection(gen)

			if !injected {
				t.Fatal("precondition: ScrollBy did not fire the render callback (no scroll happened)")
			}
			tui.mu.Lock()
			defer tui.mu.Unlock()
			if tui.selectionAutoScrollTimer != nil {
				t.Error("stale callback re-armed the auto-scroll timer after cancellation")
			}
		})
	}
}

// TestAltScreenStartStopStartRestoresCleanState pins the renderer lifecycle
// contract: upstream start() sets stopped=false and beforeTerminalStart clears
// selection/scrollbar/press state. A start->stop->start cycle must resume with
// a clean slate and render again (doRender gates on stopped).
func TestAltScreenStartStopStartRestoresCleanState(t *testing.T) {
	tui := newAltScreenForTest(&bytes.Buffer{}, 40, 10, TuiAltScreenOptions{})
	for range 20 {
		tui.Add(NewText("row"))
	}
	tui.Start()

	// Create active input state via a selection press + drag.
	tui.HandleViewportInput("\x1b[<0;5;5M")
	tui.HandleViewportInput("\x1b[<32;6;6M")
	tui.mu.Lock()
	tui.scrollbarHover = tui.implicitScrollView
	tui.scrollbarDrag = &scrollbarDrag{scrollView: tui.implicitScrollView}
	hadAnchor := tui.selectionAnchor != nil
	tui.mu.Unlock()
	if !hadAnchor {
		t.Fatal("precondition: press+drag should have set a selection anchor")
	}

	tui.StopWithOptions(StopOptions{})

	tui.mu.Lock()
	if !tui.stopped {
		t.Error("stop should set stopped=true")
	}
	if tui.selectionPressActive || tui.scrollbarHover != nil || tui.scrollbarDrag != nil {
		t.Error("stop must clear press/hover/drag state")
	}
	tui.mu.Unlock()

	tui.Start()

	tui.mu.Lock()
	defer tui.mu.Unlock()
	if tui.stopped {
		t.Error("restart must set stopped=false so rendering resumes")
	}
	if tui.selectionAnchor != nil || tui.selectionFocus != nil {
		t.Error("restart must clear selection anchor/focus")
	}
	if tui.selectionPressActive || tui.selectionDragged {
		t.Error("restart must clear press/dragged state")
	}
	if tui.scrollbarHover != nil || tui.scrollbarDrag != nil {
		t.Error("restart must clear scrollbar hover/drag")
	}
	if tui.pressedURLSet {
		t.Error("restart must clear pressed URL state")
	}
}

// TestAltScreenCopySelectionOsc52Payload verifies the decoded OSC 52 clipboard
// payload equals the selected text (not just that some OSC 52 prefix was
// emitted). Selection spans two absolute-screen lines.
func TestAltScreenCopySelectionOsc52Payload(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 40, 6, TuiAltScreenOptions{})
	tui.mu.Lock()
	tui.previousScreen = []string{"hello world", "second line", "third"}
	tui.selectionAnchor = &selectionPoint{row: 0, col: 0}
	tui.selectionFocus = &selectionPoint{row: 1, col: 11} // end of "second line"
	tui.mu.Unlock()
	copied := tui.CopyActiveSelectionToClipboard()
	if !copied {
		t.Fatal("expected copy to report success")
	}
	m := regexp.MustCompile(`\x1b\]52;c;([A-Za-z0-9+/=]+)\x07`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no OSC 52 sequence in output: %q", out.String())
	}
	decoded, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("bad base64: %v", err)
	}
	if got, want := string(decoded), "hello world\nsecond line"; got != want {
		t.Errorf("clipboard payload = %q, want %q", got, want)
	}
}

func TestAltScreenCopyOnSelectAndManualCopy(t *testing.T) {
	for _, test := range []struct {
		name       string
		configured *bool
		wantAuto   bool
	}{
		{name: "default-on", wantAuto: true},
		{name: "disabled", configured: new(false), wantAuto: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			renderer := newAltScreenForTest(&out, 40, 6, TuiAltScreenOptions{CopyOnSelect: test.configured})
			renderer.previousScreen = []string{"hello world"}
			renderer.HandleViewportInput("\x1b[<0;1;1M")
			renderer.HandleViewportInput("\x1b[<32;5;1M")
			renderer.HandleViewportInput("\x1b[<0;5;1m")

			autoCopied := strings.Contains(out.String(), "\x1b]52;c;")
			if autoCopied != test.wantAuto {
				t.Fatalf("automatic copy = %t, want %t; output=%q", autoCopied, test.wantAuto, out.String())
			}
			if renderer.GetCopyOnSelect() != test.wantAuto {
				t.Fatalf("GetCopyOnSelect() = %t, want %t", renderer.GetCopyOnSelect(), test.wantAuto)
			}
			if !renderer.HasActiveSelection() {
				t.Fatal("completed selection should remain active")
			}
			if !test.wantAuto {
				if !renderer.CopyActiveSelectionToClipboard() || !strings.Contains(out.String(), "\x1b]52;c;") {
					t.Fatalf("manual active-selection copy failed: %q", out.String())
				}
			}
		})
	}
}

func TestAltScreenClipboardFailureDoesNotFlashSuccessOrHoldRendererLock(t *testing.T) {
	var out bytes.Buffer
	var renderer *TuiAltScreen
	renderer = newAltScreenForTest(&out, 40, 6, TuiAltScreenOptions{
		CopySelection: func(string) error {
			_ = renderer.GetCopyOnSelect()
			return errors.New("clipboard denied")
		},
	})
	renderer.Start()
	out.Reset()
	renderer.mu.Lock()
	renderer.previousScreen = []string{"hello world"}
	renderer.selectionAnchor = &selectionPoint{row: 0, col: 0}
	renderer.selectionFocus = &selectionPoint{row: 0, col: 4}
	renderer.mu.Unlock()

	done := make(chan bool, 1)
	go func() { done <- renderer.CopyActiveSelectionToClipboard() }()
	select {
	case copied := <-done:
		if copied {
			t.Fatal("failed clipboard callback reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("clipboard callback deadlocked while re-entering renderer")
	}

	renderer.Render()
	frame := out.String()
	if strings.Contains(frame, "Copied!") {
		t.Fatalf("clipboard failure flashed success: %q", frame)
	}
	if !strings.Contains(frame, "clipboard denied") {
		t.Fatalf("clipboard failure was not surfaced: %q", frame)
	}
}

func TestAltScreenSelectionCopyAndShutdownDoNotRace(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var output bytes.Buffer
	renderer := newAltScreenForTest(&output, 40, 6, TuiAltScreenOptions{
		CopySelection: func(string) error {
			close(entered)
			<-release
			return nil
		},
	})
	renderer.Start()
	renderer.mu.Lock()
	renderer.previousScreen = []string{"hello world"}
	renderer.selectionAnchor = &selectionPoint{row: 0, col: 0}
	renderer.selectionFocus = &selectionPoint{row: 0, col: 4}
	renderer.mu.Unlock()

	copyDone := make(chan bool, 1)
	go func() { copyDone <- renderer.CopyActiveSelectionToClipboard() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("clipboard callback did not start")
	}
	stopDone := make(chan struct{})
	go func() {
		renderer.StopWithOptions(StopOptions{PreserveScreen: true})
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked on clipboard callback")
	}
	close(release)
	select {
	case copied := <-copyDone:
		if !copied {
			t.Fatal("successful clipboard callback reported failure")
		}
	case <-time.After(time.Second):
		t.Fatal("clipboard callback did not finish after shutdown")
	}
}

// TestAltScreenAutoScrollTickAdvancesViewport verifies a non-cancelled tick
// actually scrolls the viewport, updates selectionFocus, and re-arms the timer.
func TestAltScreenAutoScrollTickAdvancesViewport(t *testing.T) {
	tui := newAltScreenForTest(&bytes.Buffer{}, 40, 10, TuiAltScreenOptions{})
	sv := NewScrollView(NewText("x"), ScrollViewOptions{})
	sv.UpdateLayout(100, 6, func() {})
	sv.ScrollTo(0)
	before := sv.ScrollTop()

	tui.mu.Lock()
	tui.selectionAnchor = &selectionPoint{row: 0, col: 0, scrollView: sv}
	tui.selectionFocus = &selectionPoint{row: 0, col: 0, scrollView: sv}
	tui.selectionDragPointer = &pointerXY{x: 1, y: 9}
	tui.selectionAutoScrollDir = 1
	gen := tui.selectionAutoScrollGen
	tui.mu.Unlock()

	tui.autoScrollSelection(gen)

	if sv.ScrollTop() <= before {
		t.Errorf("auto-scroll tick should advance ScrollTop past %d, got %d", before, sv.ScrollTop())
	}
	tui.mu.Lock()
	defer tui.mu.Unlock()
	if tui.selectionAutoScrollTimer == nil {
		t.Error("a non-cancelled tick should re-arm the auto-scroll timer")
	}
	// The tick re-armed a real owned timer; stop it so the test does not leak it.
	tui.stopSelectionAutoScrollLocked()
}

// TestAltScreenScrollbarGrabAndDragMapsScrollTop exercises the scrollbar branch:
// grabbing the thumb starts a drag (clearing any selection), and dragging maps
// the pointer row to a scrollTop via the thumb geometry.
func TestAltScreenScrollbarGrabAndDragMapsScrollTop(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 40, 8, TuiAltScreenOptions{})
	sv := NewScrollView(&stubComponent{lines: makeNumberedLines(60)}, ScrollViewOptions{
		Primary: true, Scrollbar: "always",
	})
	tui.SetLayoutRoot(sv)
	tui.Start()

	tui.mu.Lock()
	layout := tui.currentLayout
	tui.mu.Unlock()
	if layout == nil {
		t.Fatal("precondition: no layout after Start")
	}
	box := GetScrollViewBox(*layout, sv)
	if box == nil {
		t.Fatal("precondition: primary scroll view has no layout box")
	}
	geom := GetScrollbarGeometry(box)
	if geom == nil || geom.TrackHeight <= geom.ThumbHeight {
		t.Skipf("no draggable scrollbar geometry (track=%v)", geom)
	}

	// Grab the thumb (press at the scrollbar column, thumb row).
	grabY := geom.ThumbTop
	tui.mu.Lock()
	tui.selectionAnchor = &selectionPoint{row: 5, col: 0, scrollView: sv} // pre-existing selection
	var eff mouseEffects
	handled := tui.handleScrollbarMouseEvent(sgrMouseEvent{button: 0, x: geom.Column, y: grabY}, &eff)
	grabbedDrag := tui.scrollbarDrag != nil
	clearedSelection := tui.selectionAnchor == nil
	tui.mu.Unlock()
	if !handled || !grabbedDrag {
		t.Fatalf("grab at scrollbar column should start a drag (handled=%v drag=%v)", handled, grabbedDrag)
	}
	if !clearedSelection {
		t.Error("starting a scrollbar drag must clear the active selection")
	}

	// Drag the thumb to the bottom of the track: scrollTop should map near max.
	tui.mu.Lock()
	var dragEff mouseEffects
	tui.handleScrollbarMouseEvent(sgrMouseEvent{button: 32, x: geom.Column, y: geom.TrackTop + geom.TrackHeight}, &dragEff)
	tui.mu.Unlock()
	if !dragEff.doScrollTo {
		t.Fatal("dragging the thumb should request a scrollTo")
	}
	if dragEff.scrollTop < geom.MaxScrollTop {
		t.Errorf("dragging to track bottom should map near MaxScrollTop %d, got %d", geom.MaxScrollTop, dragEff.scrollTop)
	}
}

func makeNumberedLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%02d", i)
	}
	return lines
}

// TestAltScreenClickActivatesOsc8Link proves the full OSC 8 activation path: a
// rendered hyperlink cell, a primary-button press whose press-side lookup
// (GetOsc8LinkAtColumn over the rendered frame) discovers the URL, and a release
// at the same cell with no drag that invokes the opener with that URL. Driving a
// real press: rather than preloading pressedURL: is what proves the press-side
// lookup, the plumbing the production fullscreen wiring depends on.
func TestAltScreenClickActivatesOsc8Link(t *testing.T) {
	const url = "https://example.com/docs"
	var opened []string
	tui := newAltScreenForTest(&bytes.Buffer{}, 40, 8, TuiAltScreenOptions{
		OpenURL: func(u string) { opened = append(opened, u) },
	})
	tui.Add(NewText(Hyperlink("docs", url)))
	tui.Start()
	tui.Render()

	// Locate the rendered hyperlink cell from the actual frame.
	linkX, linkY := -1, -1
	tui.mu.Lock()
	for y, line := range tui.previousScreen {
		if _, ok := widthx.GetOsc8LinkAtColumn(line, 0); ok {
			linkX, linkY = 0, y
			break
		}
	}
	tui.mu.Unlock()
	if linkY < 0 {
		t.Fatal("hyperlink was not rendered into the frame; cannot drive press-side lookup")
	}

	// Real press at the hyperlink cell (SGR mouse is 1-based): press-side lookup
	// must discover the URL from the frame, not from a preloaded field.
	tui.HandleViewportInput(fmt.Sprintf("\x1b[<0;%d;%dM", linkX+1, linkY+1))
	tui.mu.Lock()
	pressedURL, pressedSet := tui.pressedURL, tui.pressedURLSet
	tui.mu.Unlock()
	if !pressedSet || pressedURL != url {
		t.Fatalf("press-side OSC 8 lookup = (%q, %v), want (%q, true)", pressedURL, pressedSet, url)
	}

	// Release at the same cell with no drag invokes the opener.
	tui.HandleViewportInput(fmt.Sprintf("\x1b[<0;%d;%dm", linkX+1, linkY+1))
	if len(opened) != 1 || opened[0] != url {
		t.Fatalf("OpenURL invocations = %v, want [%s]", opened, url)
	}
}

// TestAltScreenAutoScrollTickIsMarshaledOntoOwnerLoop proves the auto-scroll tick
// runs on the driver's owner loop through the non-dropping tick seam, not inline
// on the timer goroutine and not through the lossy render dispatcher. Upstream
// runs the whole tick synchronously in its single event loop; pig must marshal it
// so the render ScrollBy schedules cannot observe a new-scroll + stale-focus
// intermediate frame, and it must not drop the tick (a dropped tick leaves the
// fired one-shot timer's stale pointer in place and wedges auto-scroll).
func TestAltScreenAutoScrollTickIsMarshaledOntoOwnerLoop(t *testing.T) {
	tui := NewTuiAltScreenWithOutput(io.Discard, 40, 10, TuiAltScreenOptions{})
	// The render dispatcher drops (simulates a saturated render queue). The
	// state-machine tick must NOT be routed here.
	renderDropped := 0
	tui.SetRenderDispatcher(func(fn func()) { renderDropped++ })
	// The non-dropping tick seam records what it receives.
	var ticked []func()
	tui.SetTickDispatcher(func(fn func()) { ticked = append(ticked, fn) })

	var tickCb func()
	tui.afterFunc = func(d time.Duration, fn func()) stoppableTimer {
		// Capture only the auto-scroll rearm, not the render-throttle timer that
		// RequestRender also schedules through afterFunc.
		if d == altSelectionAutoScrollInterval {
			tickCb = fn
		}
		return noopTimer{}
	}

	sv := NewScrollView(NewText("x"), ScrollViewOptions{})
	sv.UpdateLayout(100, 6, func() {})
	sv.ScrollTo(0)

	tui.mu.Lock()
	tui.selectionAnchor = &selectionPoint{scrollView: sv}
	tui.selectionFocus = &selectionPoint{scrollView: sv}
	tui.selectionDragPointer = &pointerXY{x: 1, y: 9}
	tui.selectionAutoScrollDir = 1
	gen := tui.selectionAutoScrollGen
	tui.mu.Unlock()

	// Run one tick inline to reach the rearm, which captures the marshaled
	// wrapper into tickCb.
	tui.autoScrollSelection(gen)
	if tickCb == nil {
		t.Fatal("tick did not re-arm a timer")
	}
	scrollAfterFirst := sv.ScrollTop()

	// Fire the armed callback. It must reach the non-dropping tick seam exactly
	// once, not run inline (viewport unchanged) and not be lost to the dropping
	// render dispatcher.
	ticked = nil
	tickCb()
	if len(ticked) != 1 {
		t.Fatalf("armed tick reached the tick seam %d times, want 1 (non-dropping owner-loop seam)", len(ticked))
	}
	if sv.ScrollTop() != scrollAfterFirst {
		t.Errorf("viewport moved on tick fire (ScrollTop %d -> %d): tick ran inline instead of marshaled",
			scrollAfterFirst, sv.ScrollTop())
	}

	// Running the captured closure executes the real tick (it re-arms the timer,
	// a reliable side effect of autoScrollSelection reaching its end).
	tickCb = nil
	ticked[0]()
	if tickCb == nil {
		t.Error("running the dispatched closure did not re-arm the tick: not the real auto-scroll tick")
	}
	_ = renderDropped
	tui.mu.Lock()
	tui.stopSelectionAutoScrollLocked()
	tui.mu.Unlock()
}

// TestAltScreenAutoScrollInitialArmMarshaledThroughTickSeam covers the INITIAL
// arm in updateSelectionAutoScroll (distinct from the rearm inside
// autoScrollSelection): its first tick must also reach the non-dropping tick seam.
func TestAltScreenAutoScrollInitialArmMarshaledThroughTickSeam(t *testing.T) {
	var out bytes.Buffer
	tui := newAltScreenForTest(&out, 40, 8, TuiAltScreenOptions{})
	sv := NewScrollView(&stubComponent{lines: makeNumberedLines(60)}, ScrollViewOptions{
		Primary: true, Scrollbar: "always",
	})
	tui.SetLayoutRoot(sv)
	tui.Start()

	var ticked []func()
	tui.SetTickDispatcher(func(fn func()) { ticked = append(ticked, fn) })
	var tickCb func()
	tui.afterFunc = func(d time.Duration, fn func()) stoppableTimer {
		if d == altSelectionAutoScrollInterval {
			tickCb = fn
		}
		return noopTimer{}
	}

	tui.mu.Lock()
	tui.selectionAnchor = &selectionPoint{scrollView: sv}
	tui.selectionFocus = &selectionPoint{scrollView: sv}
	// Pointer well below the visible bottom → dir=1 → initial arm.
	tui.updateSelectionAutoScroll(sgrMouseEvent{x: 1, y: 100})
	armed := tui.selectionAutoScrollTimer != nil
	tui.mu.Unlock()
	if !armed || tickCb == nil {
		t.Fatal("updateSelectionAutoScroll did not arm the initial tick")
	}

	tickCb()
	if len(ticked) != 1 {
		t.Errorf("initial-arm tick reached the tick seam %d times, want 1 (marshaled, not inline)", len(ticked))
	}

	tui.mu.Lock()
	tui.stopSelectionAutoScrollLocked()
	tui.mu.Unlock()
}
