package ai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
)

func writeMatrixToolCall(t *testing.T, w http.ResponseWriter, api API, id, name string, args JsonObject) {
	t.Helper()
	arguments, err := json.Marshal(args)
	if err != nil {
		t.Error(err)
		return
	}
	write := func(format string, values ...any) {
		if _, err := fmt.Fprintf(w, format, values...); err != nil {
			t.Error(err)
		}
	}
	sse := func(value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Error(err)
			return
		}
		write("data: %s\n\n", raw)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	switch api {
	case APIAnthropicMessages:
		for _, event := range []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": "msg_tool", "usage": map[string]any{"input_tokens": 10, "output_tokens": 0}}},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(arguments)}},
			{"type": "content_block_stop", "index": 0},
			{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]any{"output_tokens": 6}},
			{"type": "message_stop"},
		} {
			write("event: %s\n", event["type"])
			sse(event)
		}
	case APIOpenAICompletions, APIMistralConversations:
		sse(map[string]any{"id": "chatcmpl_tool", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(arguments)}}}}, "finish_reason": nil}}})
		sse(map[string]any{"id": "chatcmpl_tool", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 6, "total_tokens": 16}})
		write("data: [DONE]\n\n")
	case APIOpenAIResponses, APIAzureOpenAIResponses, APIOpenAICodexResponses:
		item := map[string]any{"type": "function_call", "id": "fc_" + id, "call_id": id, "name": name, "arguments": ""}
		sse(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item})
		sse(map[string]any{"type": "response.function_call_arguments.delta", "output_index": 0, "delta": string(arguments)})
		item["arguments"] = string(arguments)
		sse(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		sse(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_tool", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 6, "total_tokens": 16}}})
	case APIGoogleGenerativeAI, APIGoogleVertex:
		sse(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"functionCall": map[string]any{"id": id, "name": name, "args": args}}}}, "finishReason": "STOP"}}, "usageMetadata": map[string]any{"promptTokenCount": 10, "candidatesTokenCount": 6, "totalTokenCount": 16}})
	case APIBedrockConverseStream:
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		encoder := eventstream.NewEncoder()
		emit := func(kind string, value any) {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Error(err)
				return
			}
			headers := eventstream.Headers{}
			headers.Set(":message-type", eventstream.StringValue("event"))
			headers.Set(":event-type", eventstream.StringValue(kind))
			headers.Set(":content-type", eventstream.StringValue("application/json"))
			if err := encoder.Encode(w, eventstream.Message{Headers: headers, Payload: raw}); err != nil {
				t.Error(err)
			}
		}
		emit("messageStart", map[string]any{"role": "assistant"})
		emit("contentBlockStart", map[string]any{"contentBlockIndex": 0, "start": map[string]any{"toolUse": map[string]any{"toolUseId": id, "name": name}}})
		emit("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"toolUse": map[string]any{"input": string(arguments)}}})
		emit("contentBlockStop", map[string]any{"contentBlockIndex": 0})
		emit("messageStop", map[string]any{"stopReason": "tool_use"})
		emit("metadata", map[string]any{"usage": map[string]any{"inputTokens": 10, "outputTokens": 6, "totalTokens": 16}})
	default:
		t.Errorf("unsupported tool matrix API %s", api)
	}
}
