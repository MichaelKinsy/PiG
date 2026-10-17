package coding

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// prompt()'s compaction check resumes without I/O unless it compacts (agent-session.ts _checkCompaction). BeginPromptCompactionCheck decides on the
// calling goroutine and hands back only a due compaction, so a mode keeps the check on its loop when nothing is due.
func TestBeginPromptCompactionCheckReturnsOnlyADueCompaction(t *testing.T) {
	for _, tokens := range []int{100, 9_950} {
		h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":100}}`, contextWindow: 10_000, maxTokens: 100}, nil)
		h.provider.responses = []scriptedResponse{fauxReply("summary", ai.StopReasonStop, 0)}
		inner := h.session.Inner()
		for i, text := range []string{"old question", "old answer"} {
			message := agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: time.Now().UnixMilli() - 10}}
			if i == 1 {
				message = agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, Provider: "faux", ModelID: "faux-1",
					Usage: &ai.Usage{Input: tokens - 5, Output: 5, TotalTokens: tokens}, StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli() - 9}}
			}
			if _, err := inner.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
		}
		h.session.RefreshContext()
		// The overflow guard resets with the new user message: at once when nothing is due, after the compaction when one is.
		h.session.overflowRecoveryAttempted.Store(true)
		due, ok, err := h.session.BeginPromptCompactionCheck(t.Context())
		if err != nil || !ok {
			t.Fatal(err)
		}
		if n := len(h.entries("compaction")); n != 0 {
			t.Fatalf("tokens=%d: the check compacted (%d entries) before the caller ran the compaction", tokens, n)
		}
		if (due != nil) != (tokens > 9_000) {
			t.Fatalf("tokens=%d: due = %v, want a compaction only over the threshold", tokens, due != nil)
		}
		if guard := h.session.overflowRecoveryAttempted.Load(); guard != (due != nil) {
			t.Fatalf("tokens=%d: the overflow guard is %v with due = %v, want it reset only when nothing is due", tokens, guard, due != nil)
		}
		if due == nil {
			continue
		}
		if err := due(t.Context()); err != nil {
			t.Fatal(err)
		}
		if h.session.overflowRecoveryAttempted.Load() {
			t.Fatalf("tokens=%d: the overflow guard stays set after the compaction", tokens)
		}
		if n := len(h.entries("compaction")); n != 1 {
			t.Fatalf("running the due compaction wrote %d compaction entries, want 1", n)
		}
	}
}

// A prompt that finds an explicit overflow error as the last response compacts and replays that response (agent-session.ts _checkCompaction: a retried
// overflow stores _failedResponse once the compaction succeeded). Both the inline check and the deferred one keep the response.
func TestPromptCompactionCheckReplaysAnOverflowedResponse(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":100}}`, contextWindow: 10_000, maxTokens: 100}, nil)
		h.provider.responses = []scriptedResponse{fauxReply("summary", ai.StopReasonStop, 0)}
		inner := h.session.Inner()
		for i, text := range []string{"old question", "old answer", "second question"} {
			message := agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: time.Now().UnixMilli() - 20 + int64(i)}}
			if i == 1 {
				message = agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, Provider: "faux", ModelID: "faux-1",
					Usage: &ai.Usage{Input: 95, Output: 5, TotalTokens: 100}, StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli() - 15}}
			}
			if _, err := inner.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
		}
		overflow := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "faux", ModelID: "faux-1", StopReason: ai.StopReasonError,
			ErrorMessage: "Input is too long: prompt is too long: 213462 tokens > 200000 maximum", Timestamp: time.Now().UnixMilli() - 5}}
		if _, err := inner.AppendMessage(overflow); err != nil {
			t.Fatal(err)
		}
		h.session.RefreshContext()
		if deferred {
			due, ok, err := h.session.BeginPromptCompactionCheck(t.Context())
			if err != nil || !ok || due == nil {
				t.Fatalf("deferred check: due = %v, err = %v, want a compaction", due != nil, err)
			}
			if h.session.failedResponse.Load() != nil {
				t.Fatal("the response is stored before the compaction ran")
			}
			if err := due(t.Context()); err != nil {
				t.Fatal(err)
			}
		} else if err := h.session.CheckPromptCompaction(t.Context()); err != nil {
			t.Fatal(err)
		}
		if n := len(h.entries("compaction")); n != 1 {
			t.Fatalf("deferred=%v: %d compaction entries, want 1", deferred, n)
		}
		if h.session.failedResponse.Load() == nil {
			t.Fatalf("deferred=%v: the overflowed response is not kept for the replay", deferred)
		}
	}
}

// The input loop of a mode calls BeginPromptCompactionCheck, so it never waits for the Session lock: while another operation holds it (a compaction in
// progress) it reports ok = false at once and the mode runs CheckPromptCompaction off its loop. Pi's check is an await, and Pi answers get_state while a
// compaction runs (rev-audit-followups-r2).
func TestBeginPromptCompactionCheckDoesNotWaitForTheSessionLock(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":100}}`, contextWindow: 10_000, maxTokens: 100}, nil)
	h.session.mu.Lock()
	answered := make(chan bool, 1)
	go func() {
		_, ok, _ := h.session.BeginPromptCompactionCheck(t.Context())
		answered <- ok
	}()
	select {
	case ok := <-answered:
		if ok {
			t.Error("the check ran while another operation held the Session lock")
		}
	case <-time.After(2 * time.Second):
		t.Error("the check waited for the Session lock")
	}
	h.session.mu.Unlock()
}
