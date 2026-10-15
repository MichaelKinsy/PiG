//go:build !race

// The race detector instruments allocations, so these allocation ratchets run without it, as the repository's other
// allocation ratchets do.

package codingagent_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// QUALITY-AUDIT-050 item 12: a reload rebuilds the transcript from the session, and each retained assistant message cost 30
// allocations (the benchmark's 1000-entry case: 30,252 allocations), because the redraw copied a message it already owned a
// second time. The budget is per retained entry, measured as the difference between two sizes so the fixed cost of a reload
// does not count: 26 after item 12, 22 after the theme is shared per code-block indent, 21 after the branch holds each parsed message by
// value; the budget keeps one allocation of headroom, since the fixed cost of the two reloads it subtracts varies by a few dozen allocations.
func TestInteractiveSessionReloadAllocatesPerRetainedMessageWithinBudget(t *testing.T) {
	allocations := func(entries int) float64 {
		session, h, _ := reloadOriginalPair(t, func(...any) (any, error) { return nil, nil })
		for range entries {
			if _, err := session.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "retained answer"}}}}); err != nil {
				t.Fatal(err)
			}
		}
		return testing.AllocsPerRun(3, func() {
			var err error
			h.Do(func() { err = h.Reload() })
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	empty, filled := allocations(0), allocations(300)
	perEntry := (filled - empty) / 300
	const budget = 22.0
	if perEntry > budget {
		t.Fatalf("a reload allocates %.1f times per retained assistant message (%.0f with none, %.0f with 300), budget %.0f", perEntry, empty, filled, budget)
	}
}
