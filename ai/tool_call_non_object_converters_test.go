//go:build !pig_strip_mistral_conversations && !pig_strip_bedrock_converse_stream

package ai

import (
	"encoding/json"
	"strings"
	"testing"

	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
)

// nonObjectCall is a stored tool call whose arguments parsed to a value that is not an object.
func nonObjectCall(text string) ToolCall {
	call := ToolCall{ID: "c1", Name: "read"}
	call.SetStreamingArguments(text)
	return call
}

// Each converter writes a stored call's arguments as Pi's does: anthropic-messages.ts:1444 and google-shared.ts:270 send `arguments ?? {}`,
// mistral-conversations.ts:849 sends `JSON.stringify(arguments || {})`, bedrock-converse-stream.ts:1035 sends the value itself.
func TestNonObjectToolArgumentsReplayAsEachProviderDoes(t *testing.T) {
	for _, tc := range []struct{ text, nullish, falsy string }{
		{`[]`, `[]`, `[]`}, {`null`, `{}`, `{}`}, {`5`, `5`, `5`}, {`0`, `0`, `{}`}, {`"s"`, `"s"`, `"s"`}, {`""`, `""`, `{}`}, {`false`, `false`, `{}`}, {`true`, `true`, `true`},
	} {
		t.Run(tc.text, func(t *testing.T) {
			messages := []Message{UserMessage{Content: UserText("go")}, AssistantMessage{Content: []AssistantContentBlock{nonObjectCall(tc.text)}}}
			var anthropic []struct {
				Content json.RawMessage `json:"content"`
			}
			wireJSON(t, anthConvertMessages(messages, false, false, false), &anthropic)
			var input json.RawMessage
			for _, message := range anthropic {
				var blocks []struct {
					Type  string          `json:"type"`
					Input json.RawMessage `json:"input"`
				}
				_ = json.Unmarshal(message.Content, &blocks) // a user message's content is a string
				for _, block := range blocks {
					if block.Type == "tool_use" {
						input = block.Input
					}
				}
			}
			if string(input) != tc.nullish {
				t.Errorf("anthropic input = %s, want %s", input, tc.nullish)
			}
			var args json.RawMessage
			for _, content := range geminiConvertMessages(messages, "google", "gemini-2.5-flash", false) {
				for _, part := range content.Parts {
					if part.FunctionCall != nil {
						args = part.FunctionCall.Args
					}
				}
			}
			if string(args) != tc.nullish {
				t.Errorf("google args = %s, want %s", args, tc.nullish)
			}
			var mistral []struct {
				ToolCalls []struct {
					Function struct {
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"toolCalls"`
			}
			wireJSON(t, (&mistralProvider{}).convertMessages(messages, false), &mistral)
			var arguments string
			for _, message := range mistral {
				for _, call := range message.ToolCalls {
					arguments = call.Function.Arguments
				}
			}
			if arguments != tc.falsy {
				t.Errorf("mistral arguments = %s, want %s", arguments, tc.falsy)
			}
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
			if document != tc.text {
				t.Errorf("bedrock input = %s, want %s", document, tc.text)
			}
		})
	}
}

// google-generative-ai.ts:207 reads `arguments: part.functionCall.args ?? {}`: a null args is the empty object, and the delta is "{}".
func TestGoogleNullFunctionCallArgsAreTheEmptyObject(t *testing.T) {
	for _, tc := range []struct{ args, want string }{{`null`, `{}`}, {`[]`, `[]`}, {`{"path":"a"}`, `{"path":"a"}`}} {
		wire := `data: {"candidates":[{"content":{"parts":[{"functionCall":{"id":"c1","name":"read","args":` + tc.args + `}}]},"finishReason":"STOP"}]}` + "\n\n"
		builder := newAssistantStreamBuilder(t.Context(), APIGoogleGenerativeAI, "google", "gemini-2.5-flash")
		(&googleProvider{}).parseGeminiSSE(t.Context(), strings.NewReader(wire), builder)
		var deltas []string
		for event := range builder.stream.Events(t.Context()) {
			if delta, ok := event.(ToolCallDeltaEvent); ok {
				deltas = append(deltas, delta.Delta)
			}
		}
		message := builder.stream.Result()
		call, ok := message.Content[0].(ToolCall)
		if !ok {
			t.Fatalf("args %s: content = %#v", tc.args, message.Content)
		}
		if got, _ := call.ArgumentsJSON(); string(got) != tc.want || strings.Join(deltas, "") != tc.want {
			t.Errorf("args %s: arguments %s, delta %q; want %s", tc.args, got, deltas, tc.want)
		}
	}
}

// wireJSON reads a converted request value back through its JSON form, as the provider receives it.
func wireJSON(t *testing.T, value, into any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, into); err != nil {
		t.Fatalf("%v in %s", err, encoded)
	}
}
