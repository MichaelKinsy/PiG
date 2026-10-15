//go:build !race

package agent

import "testing"

// The conversion of a streamed update allocates the agent message and nothing it can take from the observation.
func TestObservedAgentAssistantMessageAllocations(t *testing.T) {
	source := observedMessageCases()["text and thinking"]
	observed := source.Observe()
	copying := testing.AllocsPerRun(100, func() { _ = agentAssistantMessage(observed) })
	taking := testing.AllocsPerRun(100, func() { _ = observedAgentAssistantMessage(observed) })
	if taking != copying-2 {
		t.Fatalf("observed conversion allocates %v, the copying conversion %v; want two fewer (content, usage)", taking, copying)
	}
}
