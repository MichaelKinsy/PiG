//go:build !race && !pig_strip_mermaid

package codingagent

import "testing"

// Pi's mermaid.ts lexes the whole message on every render; PiG's pre-check returns a text without "mermaid" unchanged
// before any parse (containsFoldMermaid). A streamed answer grows at its end, so the transformer checks only the bytes a
// frame added after the text it last found plain, and the len("mermaid")-1 bytes before them, and the render after the
// turn ends checks the whole answer once. Bound: over a 20,000-character answer streamed 24 bytes a frame, the pre-check
// reads at most frames*(24+len("mermaid")-1)+len(answer) bytes.
func TestStreamedAnswerMermaidPrecheckReadsOnlyTheDelta(t *testing.T) {
	m, _ := newTickRenderProbe(t, "fullscreen")
	interactiveLongAnswerRuns++
	answer := interactiveLongAnswer(20000, interactiveLongAnswerRuns)
	before := mermaidFoldScanned.Load()
	frames := streamInteractiveAnswer(m, answer)
	scanned := mermaidFoldScanned.Load() - before
	ceiling := int64(frames*(24+len("mermaid")-1) + len(answer))
	t.Logf("%d frames, pre-check read %.1f bytes a frame", frames, float64(scanned)/float64(frames))
	if scanned > ceiling {
		t.Fatalf("the Mermaid pre-check read %d bytes over %d frames of a %d-byte answer, ceiling %d", scanned, frames, len(answer), ceiling)
	}
}
