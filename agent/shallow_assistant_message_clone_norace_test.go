//go:build !race

package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// shallowAssistantMessage builds the agent message of a message_update from the provider partial. Its ShallowCopy is
// already an owned copy of the published state, so the message takes that copy's fields: converting a live partial costs
// one copy of the published content plus the agent message, not a second deep clone through Observe (TU-T7 rest).
func TestShallowAssistantMessageClonesThePartialOnce(t *testing.T) {
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	arguments := map[string]any{"path": "a.txt", "edits": []any{
		map[string]any{"oldText": "one", "newText": "two"},
		map[string]any{"oldText": "three", "newText": "four"},
	}, "options": map[string]any{"a": 1, "b": []any{"x", "y"}}}
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{
		ai.FauxThinking("thinking it over"),
		ai.FauxText("an answer"),
		ai.FauxToolCall("edit", arguments, nil),
		ai.FauxToolCall("edit", arguments, nil),
		ai.FauxToolCall("edit", arguments, nil),
	}})})
	var partial *ai.AssistantMessage
	a := mustNewAgent(AgentOptions{Model: &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: "faux", ProviderID: "faux"}}, OnEvent: func(ev AgentEvent) {
		update, ok := ev.(MessageUpdateEvent)
		if !ok {
			return
		}
		if delta, ok := update.AssistantMessageEvent.(ai.ToolCallDeltaEvent); ok {
			partial = delta.Partial
		}
	}})
	if err := a.Prompt(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	if partial == nil {
		t.Fatal("no tool call delta reached the listener")
	}
	if cell, _ := partial.Publication(); cell == 0 {
		t.Fatal("the delta's partial is not a live view")
	}
	copyOnce := testing.AllocsPerRun(100, func() { _ = partial.ShallowCopy() })
	convert := testing.AllocsPerRun(100, func() { _ = shallowAssistantMessage(partial) })
	// The agent message itself, and at most one more for a field the copy cannot lend.
	if limit := copyOnce + 2; convert > limit {
		t.Fatalf("shallowAssistantMessage allocates %.0f per delta, ShallowCopy alone %.0f; want at most %.0f (the partial is cloned a second time through view.Observe)", convert, copyOnce, limit)
	}
}
