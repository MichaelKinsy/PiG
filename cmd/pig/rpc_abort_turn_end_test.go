package main

import (
	"slices"
	"testing"
)

// An RPC abort while an extension tool runs still delivers turn_end for the
// tool-call turn and for the aborted response turn: Pi dispatches turn_end
// from the Agent's finishTurn hook through emitBoundary, which takes no abort
// signal (agent-session.ts:626-683, agent-loop.ts:244-252,285). Compare the
// extension's events and the stdout event types with the pinned Pi.
func TestRPCAbortMidToolExtensionEventsComparedWithPi(t *testing.T) {
	run := func(t *testing.T, p *rpcProcess, report string) ([]string, []string) {
		t.Helper()
		startInFlightPrompt(p)
		p.sendJSON(map[string]any{"id": "abort", "type": "abort"})
		var stdout []string
		p.await("agent_settled after abort", func(r rpcRecord) bool {
			kind, _ := r["type"].(string)
			stdout = append(stdout, kind)
			return kind == "agent_settled"
		})
		p.closeAndWait("after the abort settled")
		return stdout, readRPCShutdownReport(t, report)
	}
	piProcess, piReport := startPiRPCShutdownFixture(t)
	piStdout, piEvents := run(t, piProcess, piReport)
	if !slices.Contains(piEvents, "turn_end") {
		t.Fatalf("Pi extension events = %v, want turn_end after the abort", piEvents)
	}
	pigProcess, pigReport := startRPCShutdownFixture(t)
	pigStdout, pigEvents := run(t, pigProcess, pigReport)
	if !slices.Equal(pigEvents, piEvents) || !slices.Equal(pigStdout, piStdout) {
		t.Fatalf("pig extension events = %v, stdout = %v; Pi extension events = %v, stdout = %v", pigEvents, pigStdout, piEvents, piStdout)
	}
}
