package coding

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// contextUsageLog is an in-memory Session log of n exchanges whose assistant usage starts at base input tokens.
func contextUsageLog(t *testing.T, n, base int) *SessionManager {
	t.Helper()
	manager, err := NewInMemorySessionManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		usage := &ai.Usage{Input: base + i, Output: 50, TotalTokens: base + 50 + i}
		for _, message := range []agent.AgentMessage{
			{User: &agent.UserMessage{Role: "user", Content: ai.UserText(fmt.Sprintf("question %d", i)), Timestamp: int64(i)}},
			{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d", i)}}, StopReason: ai.StopReasonStop, Usage: usage, Provider: "faux", ModelID: "faux-1", Timestamp: int64(i)}},
		} {
			if _, err := manager.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
		}
	}
	return manager
}

func contextUsageSession(t *testing.T, manager *SessionManager) *Session {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
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
	return s
}

func requireFreshContextUsage(t *testing.T, s *Session, step string) *SessionContextUsage {
	t.Helper()
	got := s.ContextUsage()
	if want := s.projectedContextUsage(s.inner, s.limitsModel().Capabilities.ContextWindow); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: ContextUsage = %s, a fresh estimate = %s", step, usageString(got), usageString(want))
	}
	return got
}

// createBranchedSession replaces the manager's entries in place (session-manager.ts createBranchedSession), so the reused estimate must not outlive it.
func TestContextUsageRecomputesAfterTheLogIsReplacedInPlace(t *testing.T) {
	manager := contextUsageLog(t, 6, 1000)
	s := contextUsageSession(t, manager)
	before := requireFreshContextUsage(t, s, "before the branch")
	firstUser := manager.GetEntries()[0].Base().ID
	if _, err := manager.CreateBranchedSession(firstUser); err != nil {
		t.Fatal(err)
	}
	if after := requireFreshContextUsage(t, s, "after createBranchedSession"); reflect.DeepEqual(after, before) {
		t.Fatalf("the branched log must estimate differently from the full one: %s", usageString(after))
	}
}

// A Session that switches to another log (new, resume, fork) must not reuse an estimate of the old log whose revision happens to equal the new log's.
func TestContextUsageRecomputesAfterTheSessionSwitchesLogs(t *testing.T) {
	first := contextUsageLog(t, 6, 1000)
	s := contextUsageSession(t, first)
	// Raise the first log's revision above any the second log reaches, without changing its branch.
	for range 1000 {
		if err := first.SetLeafID(first.GetLeafID()); err != nil {
			t.Fatal(err)
		}
	}
	before := requireFreshContextUsage(t, s, "on the first log")
	second := contextUsageLog(t, 6, 30_000)
	s.ReplaceInner(second)
	for second.Revision() < first.Revision() {
		if err := second.SetLeafID(second.GetLeafID()); err != nil {
			t.Fatal(err)
		}
	}
	if second.Revision() != first.Revision() {
		t.Fatalf("the second log's revision %d must equal the first log's %d for this case", second.Revision(), first.Revision())
	}
	if after := requireFreshContextUsage(t, s, "after switching logs"); reflect.DeepEqual(after, before) {
		t.Fatalf("the second log must estimate differently from the first: %s", usageString(after))
	}
}

// getContextUsage reads the branch's messages (agent-session.ts getContextUsage); it never needs a copy of them. The estimate of a long branch allocates less than copying that branch's messages does.
func TestProjectedContextUsageDoesNotCopyTheBranchMessages(t *testing.T) {
	manager := contextUsageLog(t, 200, 1000)
	s := contextUsageSession(t, manager)
	window := s.limitsModel().Capabilities.ContextWindow
	s.projectedContextUsage(s.inner, window)
	estimate := testing.AllocsPerRun(10, func() { s.projectedContextUsage(s.inner, window) })
	copied := testing.AllocsPerRun(10, func() { s.inner.BuildSessionProjection() })
	if estimate >= copied {
		t.Fatalf("estimating the context used %.0f allocations; copying its messages takes %.0f", estimate, copied)
	}
}
