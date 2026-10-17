package coding

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// getContextUsage (agent-session.ts getContextUsage) is a function of the context window and the current branch. The Session reuses its estimate while neither changes, because the state pushed to a subprocess extension before each event carries it, and recomputes it after every change of the log, the leaf or the window.
func TestContextUsageReusesTheEstimateUntilTheLogOrWindowChanges(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewInMemorySessionManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		usage := &ai.Usage{Input: 1000 + i, Output: 50, TotalTokens: 1050 + i}
		for _, message := range []agent.AgentMessage{
			{User: &agent.UserMessage{Role: "user", Content: ai.UserText(fmt.Sprintf("question %d", i)), Timestamp: int64(i)}},
			{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d", i)}}, StopReason: ai.StopReasonStop, Usage: usage, Provider: "faux", ModelID: "faux-1", Timestamp: int64(i)}},
		} {
			if _, err := manager.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
		}
	}
	model := fakeModel()
	model.Capabilities.ContextWindow = 50_000
	s, err := NewSession(services, SessionOptions{Model: model, SessionManager: manager, ResourceLoader: NoResources, SkipBuiltinTools: true, SystemPrompt: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	fresh := func() *SessionContextUsage {
		window := s.limitsModel().Capabilities.ContextWindow
		return s.projectedContextUsage(s.inner, window)
	}
	check := func(step string) *SessionContextUsage {
		t.Helper()
		got := s.ContextUsage()
		if want := fresh(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: ContextUsage = %s, a fresh estimate = %s", step, usageString(got), usageString(want))
		}
		return got
	}

	first := check("first call")
	projections := s.inner.BuildSessionProjectionCalls()
	second := s.ContextUsage()
	if calls := s.inner.BuildSessionProjectionCalls() - projections; calls != 0 {
		t.Fatalf("a second call with an unchanged log projected the branch %d times", calls)
	}
	if !reflect.DeepEqual(first, second) || first.Tokens == second.Tokens {
		t.Fatalf("the reused estimate must equal the first and be a copy: %s %s", usageString(first), usageString(second))
	}
	*second.Tokens = -1
	if third := s.ContextUsage(); *third.Tokens == -1 {
		t.Fatal("a caller's edit of a returned estimate changed the reused one")
	}

	answer := &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "a longer answer that adds trailing tokens to the estimate"}}, StopReason: ai.StopReasonStop, Timestamp: 99}
	if _, err := manager.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("one more question with several words in it"), Timestamp: 98}}); err != nil {
		t.Fatal(err)
	}
	if after := check("after an append"); reflect.DeepEqual(after, first) {
		t.Fatalf("the estimate did not change after an append: %s", usageString(after))
	}
	if _, err := manager.AppendMessage(agent.AgentMessage{Assistant: answer}); err != nil {
		t.Fatal(err)
	}
	check("after a second append")

	firstAnswer := manager.GetEntries()[1].Base().ID
	if err := manager.Branch(firstAnswer); err != nil {
		t.Fatal(err)
	}
	check("after moving the leaf")

	model.Capabilities.ContextWindow = 80_000
	if after := check("after the window changed"); after.ContextWindow != 80_000 {
		t.Fatalf("the estimate kept the old window: %s", usageString(after))
	}

	if _, err := manager.AppendCompaction("summary", firstAnswer, 1000, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	if after := check("after a compaction"); after.Tokens != nil {
		t.Fatalf("a compaction without later usage must report null tokens, got %s", usageString(after))
	}

	// The manager keeps its object when it replaces its log (createBranchedSession, newSession, setSessionFile), so the reuse must not survive the replacement.
	if _, err := manager.CreateBranchedSession(firstAnswer); err != nil {
		t.Fatal(err)
	}
	if after := check("after the log was replaced in place"); after.Tokens == nil {
		t.Fatalf("the branch before the compaction must report tokens, got %s", usageString(after))
	}

	manager.ResetLeaf()
	check("after resetting the leaf")
}

func usageString(u *SessionContextUsage) string {
	if u == nil {
		return "nil"
	}
	tokens, percent := "null", "null"
	if u.Tokens != nil {
		tokens = fmt.Sprint(*u.Tokens)
	}
	if u.Percent != nil {
		percent = fmt.Sprint(*u.Percent)
	}
	return fmt.Sprintf("{tokens %s, window %d, percent %s}", tokens, u.ContextWindow, percent)
}
