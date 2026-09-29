package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/tui/termsim"
)

type upstreamRenderHarness struct {
	ui      *TUI
	content *recordingComponent
	output  bytes.Buffer
	vt      *scrollVT
}

func newUpstreamRenderHarness(t *testing.T, width, height int) *upstreamRenderHarness {
	t.Helper()
	h := &upstreamRenderHarness{content: &recordingComponent{}, vt: newScrollVT(width, height)}
	h.ui = NewWithOutput(&h.output, width, height)
	h.ui.Add(h.content)
	t.Cleanup(h.ui.CancelPendingRender)
	return h
}
func (h *upstreamRenderHarness) render(lines []string) string {
	h.content.lines = lines
	h.output.Reset()
	h.ui.Render()
	writes := h.output.String()
	h.vt.apply(writes)
	return writes
}
func (h *upstreamRenderHarness) viewport() []string {
	out := make([]string, h.vt.h)
	for row := range out {
		out[row] = h.vt.visibleLine(row)
	}
	return out
}
func upstreamNumberedLines(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s %d", prefix, i)
	}
	return out
}
func upstreamContains(t *testing.T, text, part string) {
	t.Helper()
	if !strings.Contains(text, part) {
		t.Fatalf("missing %q in %q", part, text)
	}
}
func upstreamExcludes(t *testing.T, text, part string) {
	t.Helper()
	if strings.Contains(text, part) {
		t.Fatalf("unexpected %q in %q", part, text)
	}
}

// The upstream BoundedWriteTerminal's hideCursor is a no-op. Exclude that separate terminal operation, not any bytes inside a render write.
type upstreamBoundedWriteTerminal struct{ writes []string }

func (w *upstreamBoundedWriteTerminal) Write(p []byte) (int, error) {
	if !bytes.Equal(p, []byte("\x1b[?25l")) {
		w.writes = append(w.writes, string(p))
	}
	return len(p), nil
}

func TestUpstreamTUIRenderDiagnostics(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:143
	t.Run("writes redraw logs to the provided directory", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("PI_TUI_DEBUG_REDRAW", "1")
		t.Setenv("PIG_HOME", t.TempDir())
		h := newUpstreamRenderHarness(t, 40, 10)
		h.ui.SetLogDirectory(dir)
		h.render([]string{"test"})
		data, err := os.ReadFile(filepath.Join(dir, "pi-tui-debug.log"))
		if err != nil {
			t.Fatal(err)
		}
		upstreamContains(t, string(data), "fullRender: first render")
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:224
	t.Run("writes the crash dump to the OS temp directory instead of a home-directory default", func(t *testing.T) {
		dir := t.TempDir()
		for _, key := range []string{"TMPDIR", "TEMP", "TMP"} {
			t.Setenv(key, dir)
		}
		h := newUpstreamRenderHarness(t, 40, 10)
		h.render([]string{"ok"})
		logPath := filepath.Join(dir, "pi-tui-crash.log")
		func() {
			defer func() {
				failure := recover()
				err, ok := failure.(error)
				if !ok {
					t.Fatalf("overflow panic = %v, want error", failure)
				}
				upstreamContains(t, err.Error(), logPath)
			}()
			h.render([]string{"ok", strings.Repeat("x", 60)})
		}()
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		upstreamContains(t, string(data), "Terminal width: 40")
	})
}

func TestUpstreamTUIBoundedRender(t *testing.T) {
	const limit = 1024 * 1024 // .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:40
	line := "\x1b_Ga=T,f=100;" + strings.Repeat("A", 1200000) + "\x1b\\"
	assertBounded := func(t *testing.T, w *upstreamBoundedWriteTerminal) {
		t.Helper()
		if len(w.writes) <= 2 {
			t.Fatalf("got %d writes, want more than 2", len(w.writes))
		}
		for _, write := range w.writes {
			if len(write) > limit {
				t.Fatalf("write length=%d exceeds %d", len(write), limit)
			}
		}
	}
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:165
	t.Run("splits a large full render without changing its output", func(t *testing.T) {
		w := &upstreamBoundedWriteTerminal{}
		ui := NewWithOutput(w, 80, 24)
		ui.Add(&recordingComponent{lines: []string{line, line}})
		ui.Render()
		assertBounded(t, w)
		if got, want := strings.Join(w.writes, ""), "\x1b[?2026h"+line+"\r\n"+line+"\x1b[?2026l"; got != want {
			t.Fatalf("full output mismatch: got %d bytes, want %d", len(got), len(want))
		}
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:187
	t.Run("splits large differential updates without a full redraw", func(t *testing.T) {
		w := &upstreamBoundedWriteTerminal{}
		ui := NewWithOutput(w, 80, 24)
		c := &recordingComponent{lines: []string{"before"}}
		ui.Add(c)
		ui.Render()
		w.writes = nil
		c.lines = []string{"before", line, line}
		ui.Render()
		assertBounded(t, w)
		output := strings.Join(w.writes, "")
		if !strings.HasPrefix(output, "\x1b[?2026h") || !strings.HasSuffix(output, "\x1b[?2026l") {
			t.Fatal("synchronized render boundaries changed")
		}
		upstreamExcludes(t, output, "\x1b[2J")
	})
}

func TestUpstreamTUIResize(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:510
	t.Run("triggers full re-render when terminal height changes", func(t *testing.T) {
		t.Setenv("TERMUX_VERSION", "")
		h := newUpstreamRenderHarness(t, 40, 10)
		lines := upstreamNumberedLines("Line", 3)
		h.render(lines)
		h.ui.height = 15
		h.vt.resize(40, 15)
		writes := h.render(lines)
		upstreamContains(t, writes, "\x1b[2J")
		upstreamContains(t, h.viewport()[0], "Line 0")
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:537
	t.Run("skips full re-render on height changes in Termux", func(t *testing.T) {
		t.Setenv("TERMUX_VERSION", "1")
		h := newUpstreamRenderHarness(t, 40, 10)
		lines := upstreamNumberedLines("Line", 20)
		h.render(lines)
		for _, height := range []int{15, 8, 14, 11} {
			h.ui.height = height
			h.vt.resize(40, height)
			writes := h.render(lines)
			upstreamExcludes(t, writes, "\x1b[2J")
			upstreamExcludes(t, writes, "\x1b[3J")
		}
		upstreamContains(t, strings.Join(h.viewport(), "\n"), "Line 19")
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:566
	t.Run("triggers full re-render when terminal width changes", func(t *testing.T) {
		h := newUpstreamRenderHarness(t, 40, 10)
		lines := upstreamNumberedLines("Line", 3)
		h.render(lines)
		h.ui.width = 60
		h.vt.resize(60, 10)
		upstreamContains(t, h.render(lines), "\x1b[2J")
	})
}

func TestUpstreamTUIContentShrinkage(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:590
	t.Run("clears empty rows when content shrinks significantly", func(t *testing.T) {
		h := newUpstreamRenderHarness(t, 40, 10)
		h.ui.SetClearOnShrink(true)
		h.render(upstreamNumberedLines("Line", 6))
		upstreamContains(t, h.render(upstreamNumberedLines("Line", 2)), "\x1b[2J")
		v := h.viewport()
		upstreamContains(t, v[0], "Line 0")
		upstreamContains(t, v[1], "Line 1")
		ncEqual(t, strings.TrimSpace(v[2]), "")
		ncEqual(t, strings.TrimSpace(v[3]), "")
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:622
	t.Run("handles shrink to single line", func(t *testing.T) {
		h := newUpstreamRenderHarness(t, 40, 10)
		h.ui.SetClearOnShrink(true)
		h.render(upstreamNumberedLines("Line", 4))
		h.render([]string{"Only line"})
		v := h.viewport()
		upstreamContains(t, v[0], "Only line")
		ncEqual(t, strings.TrimSpace(v[1]), "")
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:645
	t.Run("handles shrink to empty", func(t *testing.T) {
		h := newUpstreamRenderHarness(t, 40, 10)
		h.ui.SetClearOnShrink(true)
		h.render(upstreamNumberedLines("Line", 3))
		h.render(nil)
		v := h.viewport()
		ncEqual(t, strings.TrimSpace(v[0]), "")
		ncEqual(t, strings.TrimSpace(v[1]), "")
	})
}

func TestUpstreamTUIDifferentialRendering(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:671
	t.Run("tracks cursor correctly when content shrinks with unchanged remaining lines", func(t *testing.T) {
		h := newUpstreamRenderHarness(t, 40, 10)
		h.render(upstreamNumberedLines("Line", 5))
		h.render(upstreamNumberedLines("Line", 3))
		h.render([]string{"Line 0", "CHANGED", "Line 2"})
		upstreamContains(t, h.viewport()[1], "CHANGED")
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:700
	t.Run("renders correctly when only a middle line changes (spinner case)", func(t *testing.T) {
		h := newUpstreamRenderHarness(t, 40, 10)
		h.render([]string{"Header", "Working...", "Footer"})
		for _, frame := range []string{"|", "/", "-", "\\"} {
			h.render([]string{"Header", "Working " + frame, "Footer"})
			v := h.viewport()
			upstreamContains(t, v[0], "Header")
			upstreamContains(t, v[1], "Working "+frame)
			upstreamContains(t, v[2], "Footer")
		}
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:727
	t.Run("resets styles after each rendered line", func(t *testing.T) {
		h := newUpstreamRenderHarness(t, 20, 6)
		writes := h.render([]string{"\x1b[3mItalic", "Plain"})
		grid := termsim.New(6, 20)
		grid.WriteString(writes)
		ncEqual(t, grid.CellAt(1, 0).Style.Italic, false)
	})
	for _, tt := range []struct {
		name          string
		before, after []string
	}{
		// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:741
		{"renders correctly when first line changes but rest stays same", upstreamNumberedLines("Line", 4), []string{"CHANGED", "Line 1", "Line 2", "Line 3"}},
		// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:765
		{"renders correctly when last line changes but rest stays same", upstreamNumberedLines("Line", 4), []string{"Line 0", "Line 1", "Line 2", "CHANGED"}},
		// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:789
		{"renders correctly when multiple non-adjacent lines change", upstreamNumberedLines("Line", 5), []string{"Line 0", "CHANGED 1", "Line 2", "CHANGED 3", "Line 4"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newUpstreamRenderHarness(t, 40, 10)
			h.render(tt.before)
			h.render(tt.after)
			v := h.viewport()
			for i, line := range tt.after {
				upstreamContains(t, v[i], line)
			}
		})
	}
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:814
	t.Run("handles transition from content to empty and back to content", func(t *testing.T) {
		h := newUpstreamRenderHarness(t, 40, 10)
		h.render(upstreamNumberedLines("Line", 3))
		upstreamContains(t, h.viewport()[0], "Line 0")
		h.render(nil)
		h.render([]string{"New Line 0", "New Line 1"})
		v := h.viewport()
		upstreamContains(t, v[0], "New Line 0")
		upstreamContains(t, v[1], "New Line 1")
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:845
	t.Run("full re-renders when deleted lines move the viewport upward", func(t *testing.T) {
		h := newUpstreamRenderHarness(t, 20, 5)
		h.render(upstreamNumberedLines("Line", 12))
		upstreamContains(t, h.render(upstreamNumberedLines("Line", 7)), "\x1b[2J")
		if got, want := h.viewport(), []string{"Line 2", "Line 3", "Line 4", "Line 5", "Line 6"}; !slices.Equal(got, want) {
			t.Fatalf("viewport=%q, want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:867
	t.Run("appends after a shrink without another full redraw once the viewport is reset", func(t *testing.T) {
		h := newUpstreamRenderHarness(t, 20, 5)
		h.render(upstreamNumberedLines("Line", 8))
		upstreamContains(t, h.render(upstreamNumberedLines("Line", 2)), "\x1b[2J")
		upstreamExcludes(t, h.render(upstreamNumberedLines("Line", 3)), "\x1b[2J")
		if got, want := h.viewport(), []string{"Line 0", "Line 1", "Line 2", "", ""}; !slices.Equal(got, want) {
			t.Fatalf("viewport=%q, want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:896
	t.Run("clears stale content when maxLinesRendered was inflated by a transient component", func(t *testing.T) {
		h := newUpstreamRenderHarness(t, 40, 10)
		editor := &recordingComponent{lines: upstreamNumberedLines("Editor", 3)}
		h.ui.Add(editor)
		long := upstreamNumberedLines("Chat", 15)
		h.render(long)
		editor.lines = upstreamNumberedLines("Selector", 8)
		h.render(long)
		editor.lines = upstreamNumberedLines("Editor", 3)
		h.render(long)
		writes := h.render(upstreamNumberedLines("Chat", 12))
		upstreamContains(t, writes, "\x1b[2J")
		v := h.viewport()
		for _, line := range v {
			for _, stale := range []string{"Chat 12", "Chat 13", "Chat 14"} {
				upstreamExcludes(t, line, stale)
			}
		}
		want := []string{"Chat 5", "Chat 6", "Chat 7", "Chat 8", "Chat 9", "Chat 10", "Chat 11", "Editor 0", "Editor 1", "Editor 2"}
		if !slices.Equal(v, want) {
			t.Fatalf("viewport=%q, want %q", v, want)
		}
	})
}
