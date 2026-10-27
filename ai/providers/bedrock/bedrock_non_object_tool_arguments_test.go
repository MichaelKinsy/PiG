//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
)

// bedrock-converse-stream.ts:1035 sends a stored call's arguments as the value itself, whether or not it is an object.
func TestBedrockNonObjectToolArgumentsReplayAsTheValueItself(t *testing.T) {
	for _, text := range []string{`[]`, `null`, `5`, `0`, `"s"`, `""`, `false`, `true`} {
		t.Run(text, func(t *testing.T) {
			call := ai.ToolCall{ID: "c1", Name: "read"}
			call.SetStreamingArguments(text)
			messages := []ai.Message{ai.UserMessage{Content: ai.UserText("go")}, ai.AssistantMessage{Content: []ai.AssistantContentBlock{call}}}
			converted, err := convertBedrockMessages(messages, "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "Claude Sonnet 4.5 (US)", bedrockCacheNone, nil)
			if err != nil {
				t.Fatal(err)
			}
			var document string
			for _, message := range converted {
				for _, block := range message.Content {
					if use, ok := block.(*btypes.ContentBlockMemberToolUse); ok {
						encoded, err := use.Value.Input.MarshalSmithyDocument()
						if err != nil {
							t.Fatal(err)
						}
						document = string(encoded)
					}
				}
			}
			if document != text {
				t.Errorf("bedrock input = %s, want %s", document, text)
			}
		})
	}
}
