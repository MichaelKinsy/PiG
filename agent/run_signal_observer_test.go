package agent

import (
	"slices"
	"testing"
)

// Pi's ctx.signal is `agent.signal`, a getter that reads the active run at the moment of the call (agent.ts:336-338, runner.ts:917-920): the run's signal from the instant `activeRun` is set (agent.ts:517) to the instant it is cleared (agent.ts:554-555).
// An owner that replicates the signal elsewhere learns of both instants from ObserveRunSignal, and the state the observer reads through Signal is already the new one.
func TestAgentObserveRunSignalReportsTheRunStartAndEnd(t *testing.T) {
	a := NewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("ok")})})
	var states []bool
	a.ObserveRunSignal(func() { states = append(states, a.Signal() != nil) })
	if len(states) != 0 {
		t.Fatalf("observer ran without a run: %v", states)
	}
	for range 2 {
		if _, err := a.Send(t.Context(), "hello"); err != nil {
			t.Fatal(err)
		}
	}
	if want := []bool{true, false, true, false}; !slices.Equal(states, want) {
		t.Fatalf("Signal() read by the observer = %v, want %v", states, want)
	}
}

// A claim that fails before its run starts still ends: the observer sees the signal appear and disappear, as Pi's activeRun is set and cleared around a run that throws (agent.ts:508-555).
func TestAgentObserveRunSignalReportsAClaimThatFailsToStart(t *testing.T) {
	a := NewAgent(AgentOptions{})
	var states []bool
	a.ObserveRunSignal(func() { states = append(states, a.Signal() != nil) })
	if _, err := a.BeginSendContent(t.Context(), nil); err == nil {
		t.Fatal("a claim without a model succeeded")
	}
	if want := []bool{true, false}; !slices.Equal(states, want) {
		t.Fatalf("Signal() read by the observer = %v, want %v", states, want)
	}
}

func TestAgentObserveRunSignalStopsAfterTheReturnedFunctionRuns(t *testing.T) {
	a := NewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("ok")})})
	var calls, other int
	stop := a.ObserveRunSignal(func() { calls++ })
	a.ObserveRunSignal(func() { other++ })
	stop()
	stop()
	if _, err := a.Send(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || other != 2 {
		t.Fatalf("stopped observer ran %d times, the other %d, want 0 and 2", calls, other)
	}
}
