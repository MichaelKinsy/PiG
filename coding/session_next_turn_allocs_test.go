//go:build !race

package coding

import (
	"fmt"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// nextTurnAllocs returns what one prepareNextTurn allocates over a session of the given number of question/answer pairs, and the projected context it returns.
func nextTurnAllocs(t *testing.T, pairs int) (float64, []agent.AgentMessage) {
	t.Helper()
	h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"enabled":false}}`, contextWindow: 1_000_000, maxTokens: 100}, nil)
	inner := h.session.Inner()
	stamp := time.Now().UnixMilli() - 100_000
	for i := range pairs {
		question := agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("question %d", i)}}, Timestamp: stamp}}
		answer := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d", i)}}, Provider: "faux", ModelID: "faux-1",
			Usage: &ai.Usage{Input: 95, Output: 5, TotalTokens: 100}, StopReason: ai.StopReasonStop, Timestamp: stamp + 1}}
		for _, message := range []agent.AgentMessage{question, answer} {
			if _, err := inner.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
		}
		stamp += 2
	}
	var messages []agent.AgentMessage
	prepare := func() {
		update, err := h.session.prepareNextTurn(t.Context(), agent.PrepareNextTurnContext{})
		if err != nil || update == nil || update.Context == nil {
			t.Fatalf("prepareNextTurn = %v, %v; want a context", update, err)
		}
		messages = update.Context.Messages
	}
	prepare()
	return testing.AllocsPerRun(20, prepare), messages
}

// agent-session.ts _installAgentNextTurnRefresh hands the loop the session's own message objects: buildSessionContext copies the array, not the messages. The refresh before every request of a long run must not allocate for every earlier message of the session.
func TestNextTurnRefreshAllocationsDoNotGrowWithTheSession(t *testing.T) {
	small, smallMessages := nextTurnAllocs(t, 5)
	large, largeMessages := nextTurnAllocs(t, 55)
	t.Logf("next-turn refresh allocations: %.0f over 10 messages, %.0f over 110", small, large)
	if len(smallMessages) != 10 || len(largeMessages) != 110 {
		t.Fatalf("the refresh projected %d and %d messages, want 10 and 110", len(smallMessages), len(largeMessages))
	}
	if perMessage := (large - small) / 100; perMessage > 0.5 {
		t.Fatalf("the refresh allocates %.1f times for each message of the session (%.0f over 10 messages, %.0f over 110)", perMessage, small, large)
	}
}
