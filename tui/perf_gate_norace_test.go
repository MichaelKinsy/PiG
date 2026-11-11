//go:build !race && linux

package tui

import (
	"io"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// keystrokeScreen is a main screen with history messages of markdown above a focused editor, rendered once. Its frames are due on the
// owner loop (SetRenderDeadline), as the interactive mode's are, so a requested frame arms no timer goroutine that could paint it before the
// test's own Render does.
func keystrokeScreen(history int) (*TuiMainScreen, *Editor) { return frameScreen(history, true) }

// frameScreen is keystrokeScreen with frames due on the owner loop, or on a timer goroutine as a screen with no owner arms them.
func frameScreen(history int, owned bool) (*TuiMainScreen, *Editor) {
	ui := NewWithOutput(io.Discard, 100, 40)
	if owned {
		ui.SetRenderDeadline(func(time.Time) {})
	}
	transcript := NewContainer()
	for i := range history {
		transcript.AddChild(NewMarkdown("Message " + strconv.Itoa(i) + ": some **bold** text, `code`, and a [link](https://example.com).\n\n- item one\n- item two"))
	}
	editor := NewEditor()
	ui.Add(transcript)
	ui.Add(editor)
	ui.SetFocus(editor)
	ui.Render()
	return ui, editor
}

// A keystroke in the editor, through input dispatch and one differential frame, costs a bounded number of allocations that does not depend on the length of the transcript above it (AGENTS.md: no TUI input handler scans unbounded history).
func TestKeystrokeToFrameAllocationGate(t *testing.T) {
	const keys = 64
	perKey := func(history int) perfgate.Work {
		ui, editor := keystrokeScreen(history)
		work := perfgate.Measure(func() {
			for i := range keys {
				ui.HandleTerminalInput(string(rune('a' + i%26)))
				ui.Render()
			}
			editor.SetText("")
			ui.Render()
		})
		return perfgate.Work{Allocs: work.Allocs / keys, Bytes: work.Bytes / keys}
	}
	short, long := perKey(10), perKey(1000)
	t.Logf("perfgate: tui.keystroke_frame per keystroke: history 10: %.1f allocs %.0f B; history 1000: %.1f allocs %.0f B", short.Allocs, short.Bytes, long.Allocs, long.Bytes)
	perfgate.Check(t, "tui.keystroke_frame.allocs", long.Allocs)
	perfgate.Check(t, "tui.keystroke_frame.bytes", short.Bytes)
	// The frame keeps the last frame's line slice and rebuilds from the first changed child, so the bytes each history message adds to a keystroke are zero.
	perfgate.Check(t, "tui.keystroke_frame.bytes_per_history_message", (long.Bytes-short.Bytes)/990)
	if long.Allocs > 1.1*short.Allocs+2 {
		t.Errorf("a keystroke allocates %.1f times under 1000 history messages and %.1f under 10: the input or render path scans the transcript", long.Allocs, short.Allocs)
	}
}

// Each streamed token appended to the last assistant message costs a bounded number of allocations per frame, and the frame re-renders only the growing message from its second-last block, so the allocation count per token grows neither with the history above it nor with the completed blocks of the message (here a blank line every 40 tokens). allocs_growth_per_100_tokens is half the difference between the per-token average over a 400-token message and over a 200-token one: the growth of the average for each 100 tokens of message length, which is half the growth of a single frame's cost. Its target is zero. A block that is still open is laid out again whole on each token, so a message that never breaks its paragraph still costs per token what the paragraph's rows cost (BenchmarkStreamFrame, open).
func TestStreamingMarkdownFrameAllocationGate(t *testing.T) {
	perToken := func(history, tokens int) perfgate.Work {
		ui, _ := keystrokeScreen(history)
		message := NewMarkdown("")
		ui.Add(message)
		ui.Render()
		var text strings.Builder
		work := perfgate.Measure(func() {
			text.Reset()
			message.SetText("")
			for i := range tokens {
				text.WriteString("word" + strconv.Itoa(i%13) + " ")
				if i%40 == 39 {
					text.WriteString("\n\n")
				}
				message.SetText(text.String())
				ui.Render()
			}
		})
		return perfgate.Work{Allocs: work.Allocs / float64(tokens), Bytes: work.Bytes / float64(tokens)}
	}
	short, long, longer := perToken(10, 200), perToken(1000, 200), perToken(10, 400)
	t.Logf("perfgate: tui.stream_frame per token: history 10: %.1f allocs %.0f B; history 1000: %.1f allocs %.0f B; 400-token message: %.1f allocs %.0f B", short.Allocs, short.Bytes, long.Allocs, long.Bytes, longer.Allocs, longer.Bytes)
	perfgate.Check(t, "tui.stream_frame.allocs", long.Allocs)
	perfgate.Check(t, "tui.stream_frame.bytes", short.Bytes)
	perfgate.Check(t, "tui.stream_frame.bytes_per_history_message", (long.Bytes-short.Bytes)/990)
	perfgate.Check(t, "tui.stream_frame.allocs_growth_per_100_tokens", (longer.Allocs-short.Allocs)/2)
	if long.Allocs > 1.1*short.Allocs+2 {
		t.Errorf("a streamed token allocates %.1f times under 1000 history messages and %.1f under 10: the frame re-renders the transcript", long.Allocs, short.Allocs)
	}
}

// GC cycles started by 10,000 keystrokes under 1,000 history messages at GOGC=100. A cycle started by the render goroutine's allocation makes that goroutine assist the mark, which is the frame-time risk; the target is a render path that allocates nothing, so no cycle starts. The pause and assist times themselves are measured only on the cluster.
func TestKeystrokeGCCyclesGate(t *testing.T) {
	ui, editor := keystrokeScreen(1000)
	defer debug.SetGCPercent(debug.SetGCPercent(100))
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := range 10_000 {
		ui.HandleTerminalInput(string(rune('a' + i%26)))
		ui.Render()
		if i%64 == 63 {
			editor.SetText("")
		}
	}
	runtime.ReadMemStats(&after)
	perfgate.Check(t, "tui.keystroke_10k.gc_cycles", float64(after.NumGC-before.NumGC))
}

// BenchmarkKeystrokeFrame is one editor keystroke through input dispatch and one differential frame, under a short and a long transcript.
func BenchmarkKeystrokeFrame(b *testing.B) {
	for _, owned := range []bool{true, false} {
		for _, history := range []int{10, 1000} {
			b.Run("owned="+strconv.FormatBool(owned)+"/history="+strconv.Itoa(history), func(b *testing.B) {
				ui, editor := frameScreen(history, owned)
				b.ReportAllocs()
				i := 0
				for b.Loop() {
					ui.HandleTerminalInput(string(rune('a' + i%26)))
					ui.Render()
					if i++; i%64 == 0 {
						editor.SetText("")
					}
				}
			})
		}
	}
}

// BenchmarkStreamFrame is one streamed token appended to a Markdown message and one frame, for a short and a long message under a short and a
// long transcript. A message of paragraphs of 40 tokens re-lays out only its last paragraph; one that never breaks its paragraph (open) re-lays
// out all of it, so its cost per token still grows with the message.
func BenchmarkStreamFrame(b *testing.B) {
	for _, tc := range []struct {
		history, tokens, paragraph int
	}{{10, 200, 40}, {1000, 200, 40}, {10, 2000, 40}, {1000, 2000, 40}, {10, 200, 0}, {10, 2000, 0}} {
		name := "history=" + strconv.Itoa(tc.history) + "/tokens=" + strconv.Itoa(tc.tokens)
		if tc.paragraph == 0 {
			name += "/open"
		}
		b.Run(name, func(b *testing.B) {
			ui, _ := keystrokeScreen(tc.history)
			message := NewMarkdown("")
			ui.Add(message)
			ui.Render()
			var text strings.Builder
			token := func(i int) {
				text.WriteString("word" + strconv.Itoa(i%13) + " ")
				if tc.paragraph > 0 && i%tc.paragraph == tc.paragraph-1 {
					text.WriteString("\n\n")
				}
				message.SetText(text.String())
				ui.Render()
			}
			for i := range tc.tokens {
				token(i)
			}
			b.ReportAllocs()
			i := tc.tokens
			for b.Loop() {
				token(i)
				i++
			}
		})
	}
}
