//go:build !race

package tui

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// streamedAnswer is a long streamed answer: paragraphs, lists and code, as the sweep's Markdown stream sends.
func streamedAnswer() string {
	var b strings.Builder
	for i := range 60 {
		fmt.Fprintf(&b, "## Section %d\n\nSome **bold** text and `code` with a [link](https://example.com/%d) in paragraph %d.\n\n", i, i, i)
		fmt.Fprintf(&b, "- item one of %d\n- item two with *emphasis*\n\n```go\nfunc f%d() int { return %d }\n```\n\n", i, i, i)
	}
	return b.String()
}

// streamThenRender streams text into a Markdown in 200 deltas, rendering each, then sets the full text and, when finish
// is set, reports the stream finished before the frame that draws the last delta, as message_end does
// (interactive_events.go: FinishStreaming, then the next frame).
func streamThenRender(text string, finish bool) {
	md := NewMarkdown("")
	step := len(text) / 200
	for n := step; n < len(text); n += step {
		md.SetText(text[:n])
		md.Render(120)
	}
	md.SetText(text)
	if finish {
		md.FinishStreaming()
	}
	md.Render(120)
}

// The frame after message_end draws the last delta from the streaming memos, as every frame before it did; the memos are
// dropped after that render. Dropping them before it made the answer render again from its first block: in the sweep's
// 60,000-character stream, about 23,000 allocations at the end of each answer.
func TestFinishedMarkdownRendersItsLastDeltaFromTheStreamingMemos(t *testing.T) {
	// The full re-render this guards against costs thousands of allocations (3,692 for this answer); the margin absorbs the
	// allocations other goroutines add to a run, and the least of several runs per side is each side's own count.
	const margin = 100
	text := streamedAnswer()
	least := func(finish bool) float64 {
		got := math.MaxFloat64
		for range 5 {
			got = min(got, testing.AllocsPerRun(3, func() { streamThenRender(text, finish) }))
		}
		return got
	}
	streamed, finished := least(false), least(true)
	if finished-streamed > margin {
		t.Fatalf("finishing the stream before its last frame costs %.0f allocations, %.0f more than rendering the delta (%.0f)",
			finished, finished-streamed, streamed)
	}
}
