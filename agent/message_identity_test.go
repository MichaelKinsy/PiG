package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's message_start carries the object message_end and the transcript share (agent-loop.js); here it carries a copy (startEventMessage), and Identity is what ties the copy back to the message, while two messages that merely look alike stay two.
func TestIdentityOfAStartEnvelopeIsTheMessageItWasCopiedFrom(t *testing.T) {
	messages := map[string]AgentMessage{
		"system":     {System: &ai.SystemMessage{Content: ai.SystemText("s")}},
		"user":       {User: &UserMessage{Role: "user", Content: ai.UserText("u")}},
		"assistant":  {Assistant: &AssistantMessage{Role: "assistant"}},
		"toolResult": {ToolResult: &ToolResultMessage{Role: "toolResult", ToolCallID: "c"}},
		"custom":     AgentMessage{Custom: map[string]any{"role": RoleCustom, "customType": "t", "content": "x"}}.WithOrderRecord(),
	}
	for name, message := range messages {
		t.Run(name, func(t *testing.T) {
			envelope := startEventMessage(message)
			if message.Identity() == nil {
				t.Fatal("a message has no identity")
			}
			if envelope.Identity() != message.Identity() {
				t.Errorf("the start envelope's identity %v is not the message's %v", envelope.Identity(), message.Identity())
			}
			if again := startEventMessage(envelope); again.Identity() != message.Identity() {
				t.Errorf("a copy of the envelope has identity %v, want the message's", again.Identity())
			}
		})
	}
	a := AgentMessage{User: &UserMessage{Role: "user", Content: ai.UserText("same")}}
	b := AgentMessage{User: &UserMessage{Role: "user", Content: ai.UserText("same")}}
	if a.Identity() == b.Identity() {
		t.Error("two messages with the same content share an identity")
	}
	if (AgentMessage{}).Identity() != nil {
		t.Error("an empty message has an identity")
	}
}
