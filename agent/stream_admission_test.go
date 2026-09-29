package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi agent-loop.ts:421-438 ignores a content event when no start has established partialMessage; terminal fallback then emits start/end.
func TestAssistantIgnoresUpdatesBeforeStart(t *testing.T) {
	stream := ai.NewAssistantMessageEventStream()
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "early"}}, StopReason: ai.StopReasonPending}
	if err := stream.Push(ai.TextDeltaEvent{ContentIndex: 0, Delta: "early", Partial: partial}); err != nil {
		t.Fatal(err)
	}
	final := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "final"}}, StopReason: ai.StopReasonStop}
	if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: final}); err != nil {
		t.Fatal(err)
	}
	var starts, updates, ends int
	a := NewAgent(AgentOptions{OnEvent: func(event AgentEvent) {
		switch event.(type) {
		case MessageStartEvent:
			starts++
		case MessageUpdateEvent:
			updates++
		case MessageEndEvent:
			ends++
		}
	}})
	message, _, err := a.consumeStream(t.Context(), stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	if starts != 1 || updates != 0 || ends != 1 || message.Content[0].(ai.TextContent).Text != "final" {
		t.Fatalf("pre-start update leaked: starts=%d updates=%d ends=%d message=%#v", starts, updates, ends, message)
	}
}
