package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// upstream: packages/coding-agent/src/modes/interactive/components/footer.ts:62-75 (constructor(session: AgentSession, footerData), setSession(session)):
// the footer takes the real AgentSession, here *Session, and reads from it on each render the usage totals of its sessionManager's entries
// (:103-154) and its routedModel (:241). The test passes *Session itself to NewFooterComponentForSession and SetSession: a footer bound to a
// session shows that session's cost, a second session replaces it, and under a virtual model selection the footer shows the physical model
// that answered (:241-244).
func TestFooterComponentTakesTheRealAgentSession(t *testing.T) {
	f := createVirtualRuntime(t)
	first, firstManager := resumeVirtual(t, f, nil)
	second, secondManager := resumeVirtual(t, f, nil)
	appendCost := func(manager *icodingagent.Session, cost float64) {
		t.Helper()
		if _, err := manager.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Usage: &ai.Usage{Input: 100, Output: 10, Cost: ai.UsageCost{Total: cost}}}}); err != nil {
			t.Fatal(err)
		}
	}
	appendCost(firstManager, 0.5)
	appendCost(secondManager, 2.25)

	footer := icodingagent.NewFooterComponentForSession(first, nil)
	footer.SetModel(first.Model())
	stats := func() string {
		t.Helper()
		lines := footer.Render(160)
		if len(lines) < 2 {
			t.Fatalf("footer rendered %d lines", len(lines))
		}
		return strings.Join(lines[1:], "\n")
	}
	if got := stats(); !strings.Contains(got, "$0.500") || !strings.Contains(got, "large") {
		t.Fatalf("footer of the first session = %q, want its $0.500 and the routed model large", got)
	}
	footer.SetSession(second)
	if got := stats(); !strings.Contains(got, "$2.250") || strings.Contains(got, "$0.500") {
		t.Fatalf("footer after SetSession(second) = %q, want the second session's $2.250 only", got)
	}
	appendCost(secondManager, 0.25)
	if got := stats(); !strings.Contains(got, "$2.500") {
		t.Fatalf("footer after an entry was appended = %q, want $2.500", got)
	}
}
