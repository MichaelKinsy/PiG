package coding

// pi: packages/coding-agent/src/core/agent-session.ts

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// agent-session.ts:2087-2109 (_runBeforeSettleBoundary): an abort() called while the agent_before_settle handlers run sets
// _abortDuringBeforeSettle (:2608), and the check after the handlers (:2099) ends the boundary before the continuation they asked
// for is weighed. The continuation is neither run nor, when the context could not continue, reported as invalid (:2100-2103);
// the run settles as aborted.
func TestAbortDuringAgentBeforeSettleDropsTheContinuationWithoutReportingIt(t *testing.T) {
	var h *recoveryHarness
	calls := 0
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"agent_before_settle": {func(...any) (any, error) {
		calls++
		h.session.RequestAbort()
		return extension.BoundaryResult{Continue: new(true)}, nil
	}}}}
	h = newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("first", ai.StopReasonStop, 0), boundaryReply("must not run", ai.StopReasonStop, 0))
	diagnostics := []string{}
	h.session.currentRunner().AddErrorListener(func(err *extension.ExtensionError) { diagnostics = append(diagnostics, err.Event+": "+err.Error) })
	boundaryPrompt(t, h, "start")
	if calls != 1 {
		t.Fatalf("agent_before_settle ran %d times, want once", calls)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("the boundary reported %q after an abort during it; Pi returns before it weighs the continuation", diagnostics)
	}
	if got := h.provider.callCount(); got != 1 {
		t.Fatalf("provider calls = %d, want 1: the continuation of an aborted boundary ran", got)
	}
	settled := boundaryEvents[agent.AgentSettledEvent](h)
	if len(settled) != 1 || !settled[0].Aborted {
		t.Fatalf("settled events = %+v, want one with Aborted=true", settled)
	}
}

// The same handler without the abort is the control: the continuation it asks for cannot run after a final assistant message,
// and Pi reports it (agent-session.ts:2100-2103). The report is what the abort above suppresses.
func TestAgentBeforeSettleReportsAContinuationThatCannotRun(t *testing.T) {
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"agent_before_settle": {func(...any) (any, error) {
		return extension.BoundaryResult{Continue: new(true)}, nil
	}}}}
	h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("first", ai.StopReasonStop, 0), boundaryReply("must not run", ai.StopReasonStop, 0))
	diagnostics := []string{}
	h.session.currentRunner().AddErrorListener(func(err *extension.ExtensionError) { diagnostics = append(diagnostics, err.Event+": "+err.Error) })
	boundaryPrompt(t, h, "start")
	want := "agent_before_settle: agent_before_settle requested continuation without runnable model context"
	if len(diagnostics) != 1 || diagnostics[0] != want {
		t.Fatalf("diagnostics = %q, want [%q]", diagnostics, want)
	}
	if got := h.provider.callCount(); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}
	settled := boundaryEvents[agent.AgentSettledEvent](h)
	if len(settled) != 1 || settled[0].Aborted {
		t.Fatalf("settled events = %+v, want one with Aborted=false", settled)
	}
}
