//go:build !race

package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A host dispatcher (coding.Session.forwardAgentEvent) refreshes every listener-delivered message_update so a listener
// reads the stream state at its tick. When the partial has published nothing since the event was built, the event already
// holds that state, so the refresh returns it as it is instead of deep-cloning the agent message and the partial (TU-T7
// rest, coding/session.go RefreshEvent).
func TestRefreshEventKeepsAnEventWhosePublicationIsUnchanged(t *testing.T) {
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	arguments := map[string]any{"path": "a.txt", "edits": []any{map[string]any{"oldText": "one", "newText": "two"}}}
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{
		ai.FauxText("an answer streamed in several chunks"),
		ai.FauxToolCall("edit", arguments, nil),
		ai.FauxToolCall("edit", arguments, nil),
	}})})
	measured, worst := 0, 0.0
	a := mustNewAgent(AgentOptions{Model: &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: "faux", ProviderID: "faux"}}, OnEvent: func(ev AgentEvent) {
		update, ok := ev.(MessageUpdateEvent)
		if !ok {
			return
		}
		if _, ok := update.AssistantMessageEvent.(ai.ToolCallDeltaEvent); !ok {
			return
		}
		cell, before := update.Message.Assistant.Publication()
		if cell == 0 {
			return
		}
		allocs := testing.AllocsPerRun(20, func() { _ = RefreshEvent(ev) })
		if _, after := update.Message.Assistant.Publication(); after != before {
			return // the stream published while measuring; the event was stale
		}
		measured++
		worst = max(worst, allocs)
	}})
	if err := a.Prompt(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	if measured == 0 {
		t.Fatal("no tool call delta was measured at an unchanged publication")
	}
	if worst != 0 {
		t.Fatalf("RefreshEvent of a message_update whose publication is unchanged allocates %.0f, want 0 (it clones the agent message through Observe and the partial through delivered); %d deltas measured", worst, measured)
	}
}
