package tui

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// render_profile_test.go measures three long-input rendering boundaries:
//
//  A. steady-state frames reuse settled child renders and row-width decisions,
//     while logical-frame assembly remains proportional to the line count;
//  B. a scrolled-off mutation uses the full recovery path because stale
//     physical history cannot be repaired from the visible viewport;
//  C. editor wrapping reuses unchanged visual lines while editing a large paste.
//
// The path tests assert which recovery class is valid. The benchmarks report
// the remaining time and allocation scaling without turning timing into a
// correctness assertion.

// TestApplyLineResetsCachedMatchesReference proves the cached line-reset path is
// byte-identical to widthx.ApplyLineResets across a sequence of frames that
// exercise reuse, per-line change, image lines, grow, shrink, and an empty
// frame. Any drift is a visible rendering regression, so the oracle is the
// un-cached reference run on the same inputs.
func TestApplyLineResetsCachedMatchesReference(t *testing.T) {
	hyperlink := "\x1b]8;;https://example.com\x07link\x1b]8;;\x07"
	image := "\x1b_Gimage;payload\x1b\\"
	frames := [][]string{
		{"plain line", "\033[31mred\033[0m", hyperlink, "caf\u00e9"},
		{"plain line", "\033[31mred\033[0m", hyperlink, "caf\u00e9"},   // all reuse
		{"plain line", "\033[32mgreen\033[0m", hyperlink, "caf\u00e9"}, // one change
		{image, "plain line", "\033[32mgreen\033[0m"},                  // image + shrink
		{"plain line", "\033[32mgreen\033[0m", hyperlink, "x", "y"},    // grow
		{},                                   // empty resets the cache
		{"plain line", "\033[31mred\033[0m"}, // repopulate after empty
	}
	tui := NewWithOutput(io.Discard, 120, 40)
	for fi, frame := range frames {
		in := append([]string(nil), frame...)
		want := widthx.ApplyLineResets(append([]string(nil), frame...))
		got := tui.applyLineResetsCached(in)
		if len(got) != len(want) {
			t.Fatalf("frame %d: length %d != reference %d", fi, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("frame %d line %d:\n got  %q\n want %q", fi, i, got[i], want[i])
			}
		}
	}
}

// clearSeq marks a clearing full render. \x1b[2J wipes the visible screen and is
// used when scrollback is being discarded anyway; the scrollback-preserving path
// clears from the cursor down with \x1b[J instead, because \x1b[2J acts on the
// reader's scrolled view rather than the live region.
const clearSeq = "\x1b[2J"
const clearFromCursorSeq = "\x1b[J"

// countingWriter records total bytes and full-clear repaints so a test can
// assert which render branch fired without depending on ns/op.
type countingWriter struct {
	bytes  int
	clears int
	seen   strings.Builder
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.bytes += len(p)
	text := string(p)
	// \x1b[2J contains \x1b[J as a substring only if counted naively; count the
	// screen clear first and the cursor-down clear from what is left.
	w.clears += strings.Count(text, clearSeq)
	w.clears += strings.Count(strings.ReplaceAll(text, clearSeq, ""), clearFromCursorSeq)
	w.seen.Write(p)
	return len(p), nil
}

// buildConversation returns a TUI holding a realistic long transcript: n
// user/assistant turns plus a trailing streaming line whose handle is
// returned so a caller can mutate the bottom of the buffer. The first child
// handle is returned so a caller can mutate scrolled-off content. Width/height
// are fixed so most of the transcript sits above the viewport.
func buildConversation(out io.Writer, n int) (t *TUI, top, bottom *Text) {
	t = NewWithOutput(out, 120, 40)
	top = NewText("\033[2m● first turn marker\033[0m")
	t.Add(top)
	for i := range n {
		t.Add(NewUserMessageBlock(fmt.Sprintf("refactor the %d-th handler and keep the tests green", i)))
		t.Add(NewMarkdown(fmt.Sprintf(
			"Here is turn %d. It touches `pkg/foo`, `pkg/bar`, and a couple of call sites.\n\n"+
				"- first point about the change\n- second point with more detail\n- third trade-off\n\n"+
				"```go\nfunc handler%d() error { return nil }\n```\n", i, i)))
	}
	bottom = NewText("streaming...")
	t.Add(bottom)
	t.Render() // establish prevLines / viewport
	return t, top, bottom
}

// TestRenderPathSelection tracks which render branch each scenario takes. It
// proves the steady-state bottom edit stays on the differential path while a
// scrolled-off edit takes Pi's clearing full-redraw path.
func TestRenderPathSelection(t *testing.T) {
	t.Run("bottom-edit stays on diff path", func(t *testing.T) {
		w := &countingWriter{}
		tui, _, bottom := buildConversation(w, 400)
		before := w.clears
		for i := range 20 {
			bottom.SetText(fmt.Sprintf("streaming token %02d", i)) // same width, same line count
			tui.Render()
		}
		if got := w.clears - before; got != 0 {
			t.Fatalf("bottom edit must stay on the diff path; got %d full clears in 20 frames", got)
		}
	})

	t.Run("scrolled-off edit uses Pi full redraw", func(t *testing.T) {
		w := &countingWriter{}
		tui, top, _ := buildConversation(w, 400)
		before := w.clears
		const frames = 20
		for i := range frames {
			top.SetText(fmt.Sprintf("\033[2m● first turn marker %02d\033[0m", i))
			tui.Render()
		}
		if got := w.clears - before; got != frames {
			t.Fatalf("scrolled-off edit full redraws = %d, want %d", got, frames)
		}
	})
}

// BenchmarkRenderSteadyState measures a frame where only the bottom line changes.
// Settled component renders and width decisions are reused; frame assembly still
// scales with the number of logical lines.
func BenchmarkRenderSteadyState(b *testing.B) {
	for _, n := range []int{50, 500, 5000} {
		b.Run(fmt.Sprintf("turns=%d", n), func(b *testing.B) {
			tui, _, bottom := buildConversation(io.Discard, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				bottom.SetText(fmt.Sprintf("streaming token %06d", i))
				tui.Render()
			}
		})
	}
}

// BenchmarkRenderScrollOffChange measures the full recovery path for a changed
// line above the visible viewport.
func BenchmarkRenderScrollOffChange(b *testing.B) {
	for _, n := range []int{50, 500, 5000} {
		b.Run(fmt.Sprintf("turns=%d", n), func(b *testing.B) {
			tui, top, _ := buildConversation(io.Discard, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				top.SetText(fmt.Sprintf("\033[2m● marker %06d\033[0m", i))
				tui.Render()
			}
		})
	}
}

// TestEditorWrapCacheTransparent proves the editor wrap cache is invisible: a
// warm editor and a forced-recompute editor produce identical frames.
func TestEditorWrapCacheTransparent(t *testing.T) {
	long := strings.Repeat("word ", 60) // wraps at width 80/120
	multi := "alpha\nbeta\n" + long + "\ngamma"
	states := []struct {
		text  string
		cur   [2]int
		width int
	}{
		{"hello world", [2]int{0, 3}, 120},
		{"hello world", [2]int{0, 8}, 120},                     // cursor move only, text+width unchanged
		{"hello brave world", [2]int{0, 11}, 120},              // edit the line
		{long, [2]int{0, 4}, 120},                              // wrapping line
		{long, [2]int{0, 4}, 60},                               // width change forces re-wrap
		{long, [2]int{0, 200}, 60},                             // cursor deep inside a wrapped line
		{multi, [2]int{2, 5}, 80},                              // multi-line with a wrapped line
		{multi, [2]int{2, 5}, 80},                              // identical -> all reuse
		{"alpha\nBETA\n" + long + "\ngamma", [2]int{1, 2}, 80}, // change one short line
		{"", [2]int{0, 0}, 80},                                 // empty buffer
		{multi, [2]int{3, 1}, 80},                              // repopulate after empty
	}
	warm := NewEditor()
	warm.Focused = true
	cold := NewEditor()
	cold.Focused = true
	for i, s := range states {
		warm.SetText(s.text)
		warm.cursor = s.cur
		cold.SetText(s.text)
		cold.cursor = s.cur
		// Force a true un-cached reference: empty the cache so there is nothing
		// to reuse this frame regardless of how the reuse gate is computed.
		cold.wrapCacheWidth = -2
		cold.wrapCacheLines = nil
		cold.wrapCacheChunks = nil
		got := strings.Join(warm.Render(s.width), "\n")
		want := strings.Join(cold.Render(s.width), "\n")
		if got != want {
			t.Fatalf("state %d (text=%q width=%d cur=%v): warm cache diverged\n warm=%q\n cold=%q", i, s.text, s.width, s.cur, got, want)
		}
	}
}

// BenchmarkEditorRenderPaste measures editing one line in a large pasted buffer.
// Unchanged wrapped lines reuse their cached chunks.
func BenchmarkEditorRenderPaste(b *testing.B) {
	for _, lines := range []int{1, 200, 2000} {
		b.Run(fmt.Sprintf("bufferlines=%d", lines), func(b *testing.B) {
			e := NewEditor()
			e.Focused = true
			var sb strings.Builder
			for i := range lines {
				fmt.Fprintf(&sb, "line %d: the quick brown fox jumps over the lazy dog and then keeps going for a while\n", i)
			}
			e.SetText(sb.String())
			_ = e.Render(120) // warm the cache once
			base := e.lines[0]
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				if i%2 == 0 {
					e.lines[0] = base + "!" // one line changes; the rest reuse cached wrapping
				} else {
					e.lines[0] = base
				}
				_ = e.Render(120)
			}
		})
	}
}
