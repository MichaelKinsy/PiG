package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Upstream (pinned 0.87.1) checks row width in exactly one place: the
// differential loop of TuiMainScreen.doRender (tui-main-screen.ts:517-545).
// fullRender (tui-main-screen.ts:279-301) serves the initial, forced,
// width-change, height-change, clear-on-shrink, above-viewport, and
// deleted-lines fallback renders and emits rows unchanged. Overlay
// composition clips composited rows to the terminal width
// (tui.ts:1344 and 415), and TuiAltScreen clips every row
// (tui-alt-screen.ts:392 and its frame composition), so neither terminates.

const overflowRow = "a b c d e f g h i j k l m n o p q r s t u v" // 43 cells

// renderRecover runs one render and returns the recovered panic value.
func renderRecover(r interface{ Render() }) (value any) {
	defer func() { value = recover() }()
	r.Render()
	return nil
}

func newOverflowTUI(t *testing.T, out *bytes.Buffer, width, height int, lines *[]string) (*TUI, string) {
	t.Helper()
	dir := t.TempDir()
	ui := NewWithOutput(out, width, height)
	ui.SetLogDirectory(dir)
	ui.now = func() time.Time { return time.Date(2026, 9, 25, 14, 30, 0, 123_000_000, time.UTC) }
	ui.Add(renderFuncComponent(func(int) []string { return *lines }))
	return ui, dir
}

func requireNoCrashLog(t *testing.T, dir string) {
	t.Helper()
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("log directory entries = %v (err %v), want none", entries, err)
	}
}

// Every full-render path emits the over-wide row unchanged, without a crash
// log, and the renderer keeps running.
func TestOverflowFullRenderPathsEmitRowUnchanged(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(ui *TUI, lines *[]string) // after a fitting first frame
		prefix string
	}{
		{name: "initial", prefix: "\x1b[?2026h"},
		{name: "forced", setup: func(ui *TUI, _ *[]string) { ui.ForceFullRender() }, prefix: "\x1b[?2026h\x1b[2J\x1b[H\x1b[3J"},
		{name: "width-change", setup: func(ui *TUI, _ *[]string) { ui.width = 20 }, prefix: "\x1b[?2026h\x1b[2J\x1b[H\x1b[3J"},
		{name: "height-change", setup: func(ui *TUI, _ *[]string) { ui.height = 12 }, prefix: "\x1b[?2026h\x1b[2J\x1b[H\x1b[3J"},
		{name: "clear-on-shrink", setup: func(ui *TUI, lines *[]string) {
			ui.SetClearOnShrink(true)
			*lines = []string{"fits", "ok", "extra"}
			ui.Render()
		}, prefix: "\x1b[?2026h\x1b[2J\x1b[H\x1b[3J"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			lines := []string{"fits", "ok"}
			ui, dir := newOverflowTUI(t, &out, 20, 10, &lines)
			if tc.name == "width-change" {
				ui.width = 60
			}
			if tc.setup != nil {
				ui.Render()
				tc.setup(ui, &lines)
			}
			out.Reset()
			lines = []string{"fits", overflowRow}
			if value := renderRecover(ui); value != nil {
				t.Fatalf("full render terminated: %v", value)
			}
			want := tc.prefix + "\x1b[2Kfits" + widthx.SegmentReset + "\r\n\x1b[2K" + overflowRow + widthx.SegmentReset + "\x1b[?2026l\x1b[?25l"
			if got := out.String(); got != want {
				t.Fatalf("bytes = %q, want %q", got, want)
			}
			if ui.stopped {
				t.Fatal("full render stopped the renderer")
			}
			requireNoCrashLog(t, dir)
		})
	}
}

// A render whose first changed row is above the previous viewport takes
// upstream's full-render fallback and emits the row unchanged.
func TestOverflowAboveViewportFallbackEmitsRowUnchanged(t *testing.T) {
	var out bytes.Buffer
	lines := []string{"r0", "r1", "r2", "r3", "r4"}
	ui, dir := newOverflowTUI(t, &out, 20, 3, &lines)
	ui.Render()
	out.Reset()
	lines = []string{overflowRow, "r1", "r2", "r3", "r4"}
	if value := renderRecover(ui); value != nil {
		t.Fatalf("above-viewport render terminated: %v", value)
	}
	if !strings.Contains(out.String(), "\x1b[2J\x1b[H\x1b[3J") || !strings.Contains(out.String(), overflowRow) {
		t.Fatalf("above-viewport fallback did not full-render the row unchanged: %q", out.String())
	}
	requireNoCrashLog(t, dir)
}

// The differential loop writes Pi's crash log, stops the TUI (parking the
// cursor below the previous frame and showing it), writes none of the
// buffered frame, and panics with Pi's Error message.
func TestOverflowDifferentialTerminatesWithCrashLog(t *testing.T) {
	var out bytes.Buffer
	lines := []string{"fits", "ok"}
	ui, dir := newOverflowTUI(t, &out, 20, 10, &lines)
	ui.Render()
	out.Reset()
	lines = []string{"fits", overflowRow}

	value := renderRecover(ui)
	overflow, ok := value.(*RenderOverflowError)
	if !ok {
		t.Fatalf("recovered %v, want *RenderOverflowError", value)
	}
	logPath := filepath.Join(dir, "pig-tui-crash.log")
	wantMessage := "Rendered line 1 exceeds terminal width (43 > 20).\n\n" +
		"This is likely caused by a custom TUI component not truncating its output.\n" +
		"Use visibleWidth() to measure and truncateToWidth() to truncate lines.\n\n" +
		"Debug log written to: " + logPath
	if overflow.Error() != wantMessage {
		t.Fatalf("message = %q, want %q", overflow.Error(), wantMessage)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	wantLog := "Crash at 2026-09-25T14:30:00.123Z\n" +
		"Terminal width: 20\n" +
		"Line 1 visible width: 43\n" +
		"\n" +
		"=== All rendered lines ===\n" +
		"[0] (w=4) fits" + widthx.SegmentReset + "\n" +
		"[1] (w=43) " + overflowRow + widthx.SegmentReset + "\n"
	if string(data) != wantLog {
		t.Fatalf("crash log = %q, want %q", data, wantLog)
	}
	// TuiBase.stop -> TuiMainScreen.beforeTerminalStop with the previous frame
	// (hardware cursor on row 1, two rows): space, down 1, CRLF, show cursor.
	if got, want := out.String(), " \x1b[1B\r\n\x1b[?25h"; got != want {
		t.Fatalf("terminal bytes = %q, want %q", got, want)
	}
	if !ui.stopped {
		t.Fatal("renderer not stopped")
	}
	out.Reset()
	lines = []string{"fits", "next"}
	if value := renderRecover(ui); value != nil || out.Len() != 0 {
		t.Fatalf("stopped renderer rendered again: value=%v bytes=%q", value, out.String())
	}
}

// Without a log directory the crash log falls back to the OS temp directory,
// as upstream's `this.logDirectory ?? os.tmpdir()` does.
func TestOverflowCrashLogFallsBackToTempDir(t *testing.T) {
	tmp := t.TempDir()
	// os.TempDir reads TMPDIR on Unix and TMP, then TEMP, on Windows.
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	var out bytes.Buffer
	lines := []string{"fits"}
	ui := NewWithOutput(&out, 20, 10)
	ui.Add(renderFuncComponent(func(int) []string { return lines }))
	ui.Render()
	lines = []string{overflowRow}
	overflow, ok := renderRecover(ui).(*RenderOverflowError)
	if !ok || overflow.LogPath != filepath.Join(tmp, "pig-tui-crash.log") {
		t.Fatalf("overflow = %#v, want log under %s", overflow, tmp)
	}
	if _, err := os.Stat(overflow.LogPath); err != nil {
		t.Fatal(err)
	}
}

// The crash check runs only on rows the differential loop writes; an appended
// over-wide row still reaches it, and an over-wide image row is exempt.
func TestOverflowDifferentialRowSelection(t *testing.T) {
	image := "\x1b_Ga=T,f=100,i=7;" + strings.Repeat("A", 60) + "\x1b\\"
	cases := []struct {
		name      string
		next      []string
		terminate bool
	}{
		{"appended-row", []string{"fits", "ok", overflowRow}, true},
		{"image-row", []string{"fits", image}, false},
		{"fitting-change", []string{"fits", "changed"}, false},
		{"deletion-only", []string{"fits"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			lines := []string{"fits", "ok"}
			ui, dir := newOverflowTUI(t, &out, 20, 10, &lines)
			ui.Render()
			lines = tc.next
			value := renderRecover(ui)
			if _, got := value.(*RenderOverflowError); got != tc.terminate {
				t.Fatalf("terminated = %v (value %v), want %v", got, value, tc.terminate)
			}
			if !tc.terminate {
				requireNoCrashLog(t, dir)
			}
		})
	}
}

// Overlay composition clips composited rows to the terminal width, so an
// over-wide row under an overlay reaches the differential loop fitting. An
// over-wide row outside the overlay still terminates.
func TestOverflowDifferentialWithOverlay(t *testing.T) {
	for _, tc := range []struct {
		name      string
		row       int
		terminate bool
	}{
		{"under-overlay", 0, false},
		{"outside-overlay", 3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			lines := []string{"r0", "r1", "r2", "r3"}
			ui, dir := newOverflowTUI(t, &out, 20, 10, &lines)
			ui.OpenOverlay(&recordingComponent{lines: []string{"modal"}}, OverlayOptions{width: overlayCells(8), anchor: overlayTopLeft})
			ui.Render()
			next := append([]string(nil), lines...)
			next[tc.row] = overflowRow
			lines = next
			value := renderRecover(ui)
			if _, got := value.(*RenderOverflowError); got != tc.terminate {
				t.Fatalf("terminated = %v (value %v), want %v", got, value, tc.terminate)
			}
			if !tc.terminate {
				requireNoCrashLog(t, dir)
			}
		})
	}
}

// TuiAltScreen never terminates on an over-wide row; upstream clips it.
func TestOverflowAltScreenDoesNotTerminate(t *testing.T) {
	var out bytes.Buffer
	lines := []string{"fits", "ok"}
	ui := newAltScreenForTest(&out, 20, 10, TuiAltScreenOptions{})
	ui.Add(renderFuncComponent(func(int) []string { return lines }))
	ui.Start()
	for i, next := range [][]string{{"fits", overflowRow}, {overflowRow, overflowRow}} {
		lines = next
		if value := renderRecover(ui); value != nil {
			t.Fatalf("frame %d terminated: %v", i, value)
		}
	}
}

func TestRenderOverflowErrorFields(t *testing.T) {
	err := &RenderOverflowError{Line: 7, LineWidth: 90, TerminalWidth: 80, LogPath: "/x/pig-tui-crash.log"}
	if !strings.HasPrefix(err.Error(), fmt.Sprintf("Rendered line %d exceeds terminal width (%d > %d).", 7, 90, 80)) {
		t.Fatal(err.Error())
	}
}

// D53's full clear for a formerly over-wide row must not replace a
// differential write that Pi ends with an overflow: Pi's loop reaches the
// over-wide row 1 and throws (reproduced against pinned pi-tui).
func TestOverflowD53DoesNotMaskDifferentialOverflow(t *testing.T) {
	var out bytes.Buffer
	lines := []string{overflowRow, "ok"}
	ui, dir := newOverflowTUI(t, &out, 20, 10, &lines)
	if value := renderRecover(ui); value != nil {
		t.Fatalf("initial render terminated: %v", value)
	}
	lines = []string{"fits", overflowRow}
	overflow, ok := renderRecover(ui).(*RenderOverflowError)
	if !ok || overflow.Line != 1 || overflow.LineWidth != 43 {
		t.Fatalf("recovered %#v, want overflow at line 1 (43 > 20)", overflow)
	}
	if _, err := os.Stat(filepath.Join(dir, TUICrashLogName)); err != nil {
		t.Fatal(err)
	}
}

// Upstream takes the initial full-render path whenever the previous frame is
// empty and no dimension changed (tui-main-screen.ts:331), not only on the
// first render: an over-wide row after an empty frame is emitted unchanged.
// A non-empty previous frame still terminates.
func TestReviewOverflowAfterEmptyFrameDoesNotTerminate(t *testing.T) {
	row := strings.Repeat("x", 21)
	for _, tc := range []struct {
		name      string
		frames    [][]string
		terminate bool
	}{
		{"initially-empty", [][]string{{}}, false},
		{"deleted-to-empty", [][]string{{"fits"}, {}}, false},
		{"non-empty-control", [][]string{{"fits"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			var lines []string
			ui, dir := newOverflowTUI(t, &out, 20, 10, &lines)
			for _, frame := range tc.frames {
				lines = frame
				if value := renderRecover(ui); value != nil {
					t.Fatalf("frame %q terminated: %v", frame, value)
				}
			}
			out.Reset()
			lines = []string{row}
			value := renderRecover(ui)
			if _, got := value.(*RenderOverflowError); got != tc.terminate {
				t.Fatalf("terminated = %v (value %v), want %v", got, value, tc.terminate)
			}
			if tc.terminate {
				return
			}
			if !strings.Contains(out.String(), row) || strings.Contains(out.String(), "\x1b[2J") {
				t.Fatalf("over-wide row not emitted unchanged by the non-clearing full render: %q", out.String())
			}
			requireNoCrashLog(t, dir)
		})
	}
}
