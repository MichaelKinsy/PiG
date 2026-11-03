package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Boundary obligations of /session's renderInfo (packages/coding-agent/src/modes/interactive/interactive-mode.ts handleSessionCommand):
// the Cost section appears for a re-billed miss even when the session cost is zero, the status is snapshotted when the command runs,
// and a session without a selected model compares its usage key with "undefined/undefined".

func infoSession(t *testing.T, provider, model string, usages ...ai.Usage) *Session {
	t.Helper()
	session := NewSession("info-contract", t.TempDir())
	for i, usage := range usages {
		message := &agent.AssistantMessage{Role: "assistant", Provider: provider, ModelID: model, Timestamp: int64(i + 1), StopReason: ai.StopReasonStop, Usage: &usage}
		if _, err := session.AppendMessage(agent.AgentMessage{Assistant: message}); err != nil {
			t.Fatal(err)
		}
	}
	return session
}

func infoText(sc *SlashContext) string { return stripANSITest(sessionInfoRenderer(sc)()) }

func TestSessionInfoShowsTheCostSectionForAMissWithoutCost(t *testing.T) {
	session := infoSession(t, "test", "model",
		ai.Usage{Input: 1_000, CacheWrite: 49_000},
		ai.Usage{Input: 50_000})
	got := infoText(&SlashContext{CurrentSession: func() *Session { return session }})
	if !strings.Contains(got, "Cost") || !strings.Contains(got, "Cache Re-billed:") {
		t.Fatalf("a re-billed miss with no cost has no Cost section:\n%s", got)
	}
	free := infoSession(t, "test", "model", ai.Usage{Input: 10}, ai.Usage{Input: 10})
	if got := infoText(&SlashContext{CurrentSession: func() *Session { return free }}); strings.Contains(got, "Cost") {
		t.Fatalf("a session with no cost and no miss has a Cost section:\n%s", got)
	}
}

func TestSessionInfoSnapshotsTheCacheWarmingStatusWhenItRuns(t *testing.T) {
	status := &CacheWarmingStatus{State: "inactive", Reason: "no cache", Decision: &CacheWarmingDecision{Phase: "idle", MissCost: 0.5, WarmCost: 0.1, EconomicsAvailable: true}}
	session := infoSession(t, "test", "model", ai.Usage{Input: 10})
	render := sessionInfoRenderer(&SlashContext{CurrentSession: func() *Session { return session }, CacheWarmingStatus: func() *CacheWarmingStatus { return status }})
	status.Decision = &CacheWarmingDecision{Phase: "idle", MissCost: 9, WarmCost: 8, EconomicsAvailable: true}
	status.State = "refreshing"
	got := stripANSITest(render())
	if !strings.Contains(got, "Cache miss penalty: $0.500") || strings.Contains(got, "refreshing") {
		t.Fatalf("the status changed after the command ran:\n%s", got)
	}
}

func TestSessionInfoWithoutAModelHidesAnUndefinedUsageKey(t *testing.T) {
	session := infoSession(t, "undefined", "undefined", ai.Usage{Input: 10, Cost: ai.UsageCost{Total: 0.5}})
	got := infoText(&SlashContext{CurrentSession: func() *Session { return session }})
	if !strings.Contains(got, "Total: $0.500") || strings.Contains(got, "undefined/undefined:") {
		t.Fatalf("the single undefined usage key was listed against an undefined selection:\n%s", got)
	}
}
