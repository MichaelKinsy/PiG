//go:build !race

package tui

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"
)

// streamingAltFrame builds a fullscreen frame like a streaming turn: a transcript of settled messages and an assistant
// answer that grows by a few words per frame. step appends the next words and renders one frame.
func streamingAltFrame(tb testing.TB) (step func()) {
	tb.Helper()
	out := &bytes.Buffer{}
	alt := newAltScreenForTest(out, 120, 40, TuiAltScreenOptions{})
	transcript := NewContainer()
	for i := range 30 {
		transcript.Add(NewMarkdown(fmt.Sprintf("Message %d: %s", i, strings.Repeat("settled words ", 20))))
	}
	answer := NewMarkdown("")
	transcript.Add(answer)
	alt.Add(transcript)
	alt.Add(NewText("footer: model, tokens and cwd"))
	alt.Start()
	text := ""
	n := 0
	return func() {
		n++
		text += fmt.Sprintf("word%d ", n)
		if n%17 == 0 {
			text += "\n\n"
		}
		answer.SetText(text)
		out.Reset()
		alt.Render()
	}
}

// TestAltScreenStreamingFrameAllocationCeiling is the per-frame gate for a fullscreen streaming frame: the allocations of one
// frame (lay out, diff the rows, write) may only go down. Before the row writes stopped going through fmt and the frame buffer
// started at the last frame's length the figure was 47.
func TestAltScreenStreamingFrameAllocationCeiling(t *testing.T) {
	const ceiling = 37
	step := streamingAltFrame(t)
	for range 50 {
		step()
	}
	// Allocations of goroutines other tests left running only add to a run, so the least of several runs is the frame's own count.
	got := math.MaxFloat64
	for range 5 {
		got = min(got, testing.AllocsPerRun(100, step))
	}
	if got > ceiling {
		t.Fatalf("a streaming frame allocates %.1f times, ceiling %d", got, ceiling)
	}
}

func BenchmarkAltScreenStreamingFrame(b *testing.B) {
	step := streamingAltFrame(b)
	for range 50 {
		step()
	}
	b.ReportAllocs()
	for b.Loop() {
		step()
	}
}
