package cli

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/rpcclient"
)

func BenchmarkRPCMessageUpdateProjection(b *testing.B) {
	for _, blocks := range []int{0, 1, 1024} {
		b.Run(fmt.Sprint(blocks), func(b *testing.B) {
			content := make([]ai.AssistantContentBlock, blocks)
			for i := range content {
				content[i] = ai.ToolCall{ID: "call", Name: "lookup", Arguments: ai.JsonObject{"value": i}}
			}
			partial := &ai.AssistantMessage{Content: content}
			message := &agent.AssistantMessage{Role: agent.RoleAssistant, Content: content, Usage: &ai.Usage{}}
			event := agent.MessageUpdateEvent{Message: agent.AgentMessage{Assistant: message}, AssistantMessageEvent: ai.TextDeltaEvent{ContentIndex: 0, Delta: "x", Partial: partial}}
			b.ReportAllocs()
			for b.Loop() {
				wire, err := rpcMessageUpdate(event)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := rpcclient.SerializeJsonLine(wire); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Pi json-event.ts:21-37 omits partial before serialization and only reads tool identity for toolcall_start; it never invokes discarded arguments' toJSON hooks.
func TestRPCMessageUpdateDoesNotTraverseDiscardedPartial(t *testing.T) {
	for _, shape := range []string{"event-partial", "agent-content", "tool-identity"} {
		t.Run(shape, func(t *testing.T) {
			visits := 0
			content := []ai.AssistantContentBlock{ai.ToolCall{ID: "call", Name: "lookup", Arguments: ai.JsonObject{
				"probe": printModeJSONMarshaler(func() ([]byte, error) { visits++; return []byte(`"safe"`), nil }),
			}}}
			message := &agent.AssistantMessage{Role: agent.RoleAssistant, Usage: &ai.Usage{}}
			partial := &ai.AssistantMessage{}
			var providerEvent ai.AssistantMessageEvent
			if shape == "agent-content" {
				message.Content = content
			} else {
				partial.Content = content
			}
			if shape == "tool-identity" {
				providerEvent = ai.ToolCallStartEvent{ContentIndex: 0, Partial: partial}
			} else {
				providerEvent = ai.TextDeltaEvent{ContentIndex: 0, Delta: "x", Partial: partial}
			}
			wire, err := rpcMessageUpdate(agent.MessageUpdateEvent{Message: agent.AgentMessage{Assistant: message}, AssistantMessageEvent: providerEvent})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := rpcclient.SerializeJsonLine(wire)
			if err != nil {
				t.Fatal(err)
			}
			if visits != 0 {
				t.Errorf("discarded argument marshaler ran %d times: wire=%s", visits, encoded)
			}
		})
	}
}
