package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestNormalizeMessagesReplayIdentityIncludesProviderAPIAndModel(t *testing.T) {
	for _, test := range []struct {
		name            string
		messageProvider string
		messageAPI      ai.API
		messageModel    string
		targetAPI       ai.API
		targetModel     string
		wantSignatures  bool
	}{
		{"same identity", "static-fake", ai.APIOpenAIResponses, "same", ai.APIOpenAIResponses, "same", true},
		{"different provider", "other", ai.APIOpenAIResponses, "same", ai.APIOpenAIResponses, "same", false},
		{"different API", "static-fake", ai.APIOpenAICompletions, "same", ai.APIOpenAIResponses, "same", false},
		{"different model", "static-fake", ai.APIOpenAIResponses, "other", ai.APIOpenAIResponses, "same", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &staticProvider{}
			target := fakeTestModel(provider)
			target.ID = test.targetModel
			target.ProviderMeta.API = test.targetAPI
			message := AgentMessage{Assistant: &AssistantMessage{
				Role: "assistant", Provider: test.messageProvider, API: test.messageAPI, ModelID: test.messageModel,
				Content: []ai.AssistantContentBlock{
					ai.TextContent{Text: "answer", TextSignature: "text-signature"},
					ai.ThinkingContent{Thinking: "thought", ThinkingSignature: "thinking-signature"},
					ai.ToolCall{ID: "call", Name: "tool", Arguments: ai.JsonObject{}, ThoughtSignature: "tool-signature"},
				},
			}}
			got := NormalizeMessages([]AgentMessage{message}, target)[0].Assistant.Content
			text := got[0].(ai.TextContent)
			if (text.TextSignature != "") != test.wantSignatures {
				t.Fatalf("text signature = %q, want retained=%t", text.TextSignature, test.wantSignatures)
			}
			if test.wantSignatures {
				if got[1].(ai.ThinkingContent).ThinkingSignature != "thinking-signature" || got[2].(ai.ToolCall).ThoughtSignature != "tool-signature" {
					t.Fatalf("same-identity content = %#v", got)
				}
				return
			}
			// Another model's thinking is dropped, not sent as text (Pi #11000).
			if len(got) != 2 {
				t.Fatalf("cross-identity content = %#v, want the text and the tool call", got)
			}
			if got[1].(ai.ToolCall).ThoughtSignature != "" {
				t.Fatalf("cross-identity tool signature = %#v", got[1])
			}
		})
	}
}
