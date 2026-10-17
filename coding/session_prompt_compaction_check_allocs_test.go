//go:build !race

package coding

import (
	"fmt"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// promptCompactionCheckAllocs returns what one prompt()'s compaction check allocates over a session of the given number of question/answer pairs.
func promptCompactionCheckAllocs(t *testing.T, pairs int) float64 {
	t.Helper()
	h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":100}}`, contextWindow: 1_000_000, maxTokens: 100}, nil)
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
	h.session.RefreshContext()
	check := func() {
		if due, ok, err := h.session.BeginPromptCompactionCheck(t.Context()); err != nil || !ok || due != nil {
			t.Fatalf("the check found a compaction due or did not run (%v, %v, %v)", due != nil, ok, err)
		}
	}
	check()
	return testing.AllocsPerRun(20, check)
}

// agent-session.ts _checkCompaction reads the session's messages to decide; it does not copy them. A prompt's check must not allocate for every earlier message of the session.
func TestPromptCompactionCheckAllocationsDoNotGrowWithTheSession(t *testing.T) {
	small, large := promptCompactionCheckAllocs(t, 5), promptCompactionCheckAllocs(t, 55)
	t.Logf("compaction check allocations: %.0f over 10 messages, %.0f over 110", small, large)
	if perMessage := (large - small) / 100; perMessage > 2 {
		t.Fatalf("the check allocates %.1f times for each message of the session (%.0f over 10 messages, %.0f over 110)", perMessage, small, large)
	}
}
