//go:build !race

package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// A message_start, message_update or message_end of a streamed response carries its stream scope, which the handler's context holds (AgentEventContext). A runner with no handler for the event builds no context: the per-delta dispatch then allocates nothing (AS-F2/F12).
func TestDispatchAgentLoopEventBuildsNoContextWithoutHandler(t *testing.T) {
	provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux"})
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("streamed in several deltas, one per chunk")}})})
	var scoped []agent.AgentEvent
	a := mustNewAgent(agent.AgentOptions{
		Model: &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: "faux", ProviderID: "faux"}},
		OnEvent: func(ev agent.AgentEvent) {
			if AgentLoopEventType(ev) != "" && agent.EventObservation(ev) != nil {
				scoped = append(scoped, ev)
			}
		},
	})
	if err := a.Prompt(t.Context(), "hi"); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, ev := range scoped {
		kinds[AgentLoopEventType(ev)] = true
	}
	if !kinds[EventMessageUpdate] || !kinds[EventMessageEnd] {
		t.Fatalf("the run delivered no scoped message_update and message_end: %v", kinds)
	}
	noop := func(...any) (any, error) { return nil, nil }
	runner := inproc.NewRunner([]extension.Extension{{Path: "/tmp/turns.ts", Handlers: map[string][]extension.HandlerFn{EventTurnStart: {noop}}}}, ".")
	for _, ev := range scoped {
		if allocs := testing.AllocsPerRun(50, func() { DispatchAgentLoopEvent(runner, ev, nil) }); allocs != 0 {
			t.Errorf("%s without a handler: %v allocations per dispatch, want 0", AgentLoopEventType(ev), allocs)
		}
	}
}
