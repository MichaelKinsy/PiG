package agent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// projection is what ObserveProjection returns, gathered for comparison.
type projection struct {
	Content      []ai.AssistantContentProjection
	StopReason   ai.StopReason
	ErrorMessage string
}

// project is ObserveProjection into a new slice.
func project(message *AssistantMessage) projection {
	content, stopReason, errorMessage := message.ObserveProjection(nil)
	return projection{Content: content, StopReason: stopReason, ErrorMessage: errorMessage}
}

// observedProjection is the projection of an owned observation, which ObserveProjection must return without copying it.
func observedProjection(message *AssistantMessage) projection {
	observed := projection{StopReason: message.StopReason, ErrorMessage: message.ErrorMessage}
	for _, block := range message.Content {
		switch block := block.(type) {
		case ai.TextContent:
			observed.Content = append(observed.Content, ai.AssistantContentProjection{Kind: "text", Text: block.Text})
		case ai.ThinkingContent:
			observed.Content = append(observed.Content, ai.AssistantContentProjection{Kind: "thinking", Thinking: block.Thinking})
		case ai.ToolCall:
			observed.Content = append(observed.Content, ai.AssistantContentProjection{Kind: "toolCall", ToolCallID: block.ID, ToolCallName: block.Name, Arguments: block.Arguments})
		}
	}
	return observed
}

// Each message_update's message projects what its Observe copies, and its Publication is its provider partial's, advancing over the stream. Retained past the stream, it keeps its copied scalars and still projects what Observe copies. A plain message projects its own fields and has no publication.
func TestAssistantMessageProjectionEqualsObserve(t *testing.T) {
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{
		ai.FauxThinking("thinking it over in a few chunks"),
		ai.FauxText("an answer streamed in several chunks"),
		ai.FauxToolCall("write", map[string]any{"path": "a.txt", "content": "some content to stream"}, nil),
	}})})
	var retained []*AssistantMessage
	var seqs []uint64
	var failure string
	a := mustNewAgent(AgentOptions{Model: &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{API: "faux", ProviderID: "faux"}}, OnEvent: func(ev AgentEvent) {
		update, ok := ev.(MessageUpdateEvent)
		if !ok || failure != "" {
			return
		}
		message := update.Message.Assistant
		if got, want := project(message), observedProjection(message.Observe()); !reflect.DeepEqual(got, want) {
			failure = "projection differs from Observe"
		}
		cell, seq := message.Publication()
		var partial *ai.AssistantMessage
		switch event := update.AssistantMessageEvent.(type) {
		case ai.TextDeltaEvent:
			partial = event.Partial
		case ai.ThinkingDeltaEvent:
			partial = event.Partial
		case ai.ToolCallDeltaEvent:
			partial = event.Partial
		default:
			return
		}
		if partialCell, partialSeq := partial.Publication(); cell == 0 || partialCell != cell || partialSeq != seq {
			failure = "message and partial observe different publications"
		}
		retained = append(retained, message)
		seqs = append(seqs, seq)
	}})
	if err := a.Prompt(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
	if len(seqs) < 3 || seqs[len(seqs)-1] <= seqs[0] {
		t.Fatalf("delta publications %v do not advance", seqs)
	}
	for index, message := range retained {
		if got, want := project(message), observedProjection(message.Observe()); !reflect.DeepEqual(got, want) {
			t.Fatalf("retained update %d: projection\n%#v\nwant\n%#v", index, got, want)
		}
	}
	plain := &AssistantMessage{Role: RoleAssistant, StopReason: ai.StopReasonError, ErrorMessage: "boom", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "t"}, ai.ToolCall{ID: "c", Name: "n", Arguments: ai.JsonObject{"k": "v"}}}}
	if got, want := project(plain), observedProjection(plain.Observe()); !reflect.DeepEqual(got, want) {
		t.Fatalf("plain: projection %#v, want %#v", got, want)
	}
	if cell, seq := plain.Publication(); cell != 0 || seq != 0 {
		t.Fatalf("plain publication (%d, %d)", cell, seq)
	}
}
