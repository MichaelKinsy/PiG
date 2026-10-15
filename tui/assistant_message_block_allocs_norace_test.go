//go:build !race

package tui

import (
	"math"
	"strconv"
	"testing"
)

// leastAllocs is the least of several AllocsPerRun measurements: allocations of goroutines other tests left running only add to
// a run, so the least is the operation's own count.
func leastAllocs(runs int, f func()) float64 {
	got := math.MaxFloat64
	for range 5 {
		got = min(got, testing.AllocsPerRun(runs, f))
	}
	return got
}

// The per-delta gate of the streamed assistant block: a tool-call argument delta (SetHasToolCalls with the value it already
// has) changes nothing and allocates nothing, and a text delta of a message with a thinking run, text and tool calls keeps the
// block's children (spacers, the thinking run's mouse region) instead of building them again. Before, the tool-call delta
// rebuilt every child (6 allocations) and a text delta allocated 11 times (2 of them this test's own text).
func TestAssistantMessageDeltaAllocationCeiling(t *testing.T) {
	block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	message := AssistantMessage{Content: []AssistantContentBlock{
		{Type: "thinking", Thinking: "Planning the change in a few words."},
		{Type: "text", Text: "Here is the answer"},
		{Type: "toolCall"},
	}}
	block.UpdateContent(message, true)
	block.Render(100)

	toolCallDelta := leastAllocs(100, func() { block.SetHasToolCalls(true) })
	n := 0
	textDelta := leastAllocs(100, func() {
		n++
		message.Content[1].Text = "Here is the answer " + strconv.Itoa(n)
		block.UpdateContent(message, true)
	})
	t.Logf("allocations: tool-call delta %.1f, text delta %.1f", toolCallDelta, textDelta)
	if toolCallDelta > 0 {
		t.Errorf("a tool-call delta that changes nothing allocates %.1f times, want 0", toolCallDelta)
	}
	const ceiling = 5
	if textDelta > ceiling {
		t.Errorf("a text delta allocates %.1f times, ceiling %d", textDelta, ceiling)
	}
}
