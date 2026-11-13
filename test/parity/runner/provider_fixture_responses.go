//go:build parity

package runner

import (
	"encoding/json"
	"fmt"
)

// responsesLastTurn reads OpenAI Responses input items (Pi openai-responses-shared.ts convertResponsesMessages): the newest item is a user
// message with input_text content or a function_call_output whose output is a string or input_text parts.
func responsesLastTurn(body []byte) (string, bool, error) {
	var request struct {
		Input []struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
			Output  json.RawMessage `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &request); err != nil || len(request.Input) == 0 {
		return "", false, fmt.Errorf("no input")
	}
	last := request.Input[len(request.Input)-1]
	if last.Type == "function_call_output" {
		text, err := contentText(last.Output)
		return text, true, err
	}
	text, err := contentText(last.Content)
	return text, false, err
}

// responsesStream is one Responses stream with a single output item: created, the item added, its delta, the item done, completed. Both
// readers take the event type from the data record, so the records carry no SSE event name.
func responsesStream(added, delta, done map[string]any) []sseEvent {
	delta["output_index"] = 0
	delta["item_id"] = done["id"]
	usage := map[string]any{"input_tokens": 10, "output_tokens": 3, "total_tokens": 13, "input_tokens_details": map[string]int{"cached_tokens": 0}}
	return []sseEvent{
		{data: map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_strict", "status": "in_progress"}}},
		{data: map[string]any{"type": "response.output_item.added", "output_index": 0, "item": added}},
		{data: delta},
		{data: map[string]any{"type": "response.output_item.done", "output_index": 0, "item": done}},
		{data: map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_strict", "status": "completed", "usage": usage}}},
	}
}

func responsesToolCall() []sseEvent {
	item := func(arguments, status string) map[string]any {
		return map[string]any{"type": "function_call", "id": "fc_strict_read", "call_id": "strict-read", "name": "read", "arguments": arguments, "status": status}
	}
	return responsesStream(item("", "in_progress"),
		map[string]any{"type": "response.function_call_arguments.delta", "delta": readArguments},
		item(readArguments, "completed"))
}

func responsesReply(text string) []sseEvent {
	item := func(status string, content []any) map[string]any {
		return map[string]any{"type": "message", "id": "msg_strict", "role": "assistant", "status": status, "content": content}
	}
	return responsesStream(item("in_progress", []any{}),
		map[string]any{"type": "response.output_text.delta", "content_index": 0, "delta": text},
		item("completed", []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}))
}
