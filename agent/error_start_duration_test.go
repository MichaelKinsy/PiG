package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// agent-loop.ts emits message_start from a spread of the start partial when it consumes the event, so a response that starts and fails in one tick carries durationMs on message_start, as on message_end.
func TestTestFauxRetryableErrorCarriesDurationOnMessageStart(t *testing.T) {
	model := &ai.Model{ID: "faux-1", Provider: &ai.TestFauxProvider{}, Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 4096}}
	events := make(chan AgentEvent, 64)
	a := mustNewAgent(AgentOptions{Model: model, EventCh: events})
	_, _ = a.Send(t.Context(), "Trigger: retryable provider error")
	close(events)
	for ev := range events {
		if start, ok := ev.(MessageStartEvent); ok && start.Message.Assistant != nil {
			if start.Message.Assistant.DurationMs == nil {
				t.Fatal("message_start has no durationMs")
			}
			return
		}
	}
	t.Fatal("no assistant message_start")
}
