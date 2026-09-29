package ai

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// Pi's provider decoders preserve property types until validation.ts runs:
// openai-completions.ts:650, openai-responses-shared.ts:657-714,
// anthropic-messages.ts:703-740, google-generative-ai.ts:205-206.
func TestProvidersPreserveNoncanonicalToolArguments(t *testing.T) {
	want := JsonObject{"path": "file.txt", "offset": "10", "limit": nil, "extra": true}
	assert := func(t *testing.T, message *AssistantMessage) {
		t.Helper()
		if message.StopReason != StopReasonToolUse || len(message.Content) != 1 {
			t.Fatalf("message = %+v", message)
		}
		call, ok := message.Content[0].(ToolCall)
		if !ok || call.Name != "read" || !reflect.DeepEqual(call.Arguments, want) {
			t.Fatalf("call = %+v; want arguments %+v", call, want)
		}
	}
	chat := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"file.txt\\\",\\\"offset\\\":\\\"1\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"0\\\",\\\"limit\\\":null,\\\"extra\\\":true}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
	for _, provider := range []string{"openai", "openrouter", "ollama", "github-copilot"} {
		t.Run("chat/"+provider, func(t *testing.T) { assert(t, runOpenAICompletionsSSEForProvider(t, provider, chat)) })
	}
	responses := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call","name":"read","arguments":""}}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":\"file.txt\",\"offset\":\"1"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call","name":"read","arguments":"{\"path\":\"file.txt\",\"offset\":\"10\",\"limit\":null,\"extra\":true}"}}

data: {"type":"response.completed","response":{"status":"completed"}}

`
	for _, provider := range []string{"openai", "github-copilot"} {
		t.Run("responses/"+provider, func(t *testing.T) {
			builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, provider, "model")
			p := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: provider}}
			p.parseResponsesSSE(t.Context(), strings.NewReader(responses), builder, nil)
			assert(t, builder.stream.Result())
		})
	}
	t.Run("anthropic", func(t *testing.T) {
		result, _ := runAnthropicEvents(t, `event: message_start
data: {"message":{"usage":{}}}

event: content_block_start
data: {"index":0,"content_block":{"type":"tool_use","id":"call","name":"read","input":{}}}

event: content_block_delta
data: {"index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"file.txt\",\"offset\":\"1"}}

event: content_block_delta
data: {"index":0,"delta":{"type":"input_json_delta","partial_json":"0\",\"limit\":null,\"extra\":true}"}}

event: content_block_stop
data: {"index":0}

event: message_delta
data: {"delta":{"stop_reason":"tool_use"},"usage":{}}

event: message_stop
data: {}

`)
		assert(t, result)
	})
	t.Run("google", func(t *testing.T) {
		builder := newAssistantStreamBuilder(context.Background(), APIGoogleGenerativeAI, "google", "model")
		provider := &googleProvider{}
		provider.parseGeminiSSE(t.Context(), strings.NewReader(`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"read","args":{"path":"file.txt","offset":"10","limit":null,"extra":true}}}]},"finishReason":"STOP"}]}

`), builder)
		assert(t, builder.stream.Result())
	})
}
