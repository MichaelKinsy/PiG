package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

// upstream: packages/ai/test/openai-responses-partial-json-cleanup.test.ts
// Upstream keeps the streamed argument text in a partialJson property on the block and deletes it at response.output_item.done. A Go ToolCall keeps it in scratch state that MarshalJSON writes as partialJson while the call streams (types.go ToolCall, provider_tool_scratch.go), so the observable contract is the same: the finalized block, as persisted and as emitted on toolcall_end, carries no partialJson.

func TestOpenAIResponsesRemovesPartialJSONFromPersistedToolCallBlocksAtOutputItemDone(t *testing.T) {
	// upstream: openai-responses-partial-json-cleanup.test.ts:68 "removes partialJson from persisted tool-call blocks at output_item.done"
	const argumentsJSON = `{\"path\":\"README.md\",\"content\":\"updated\"}`
	sse := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_test","call_id":"call_test","name":"edit","arguments":""}}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":\"README.md\""}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":",\"content\":\"updated\"}"}

data: {"type":"response.function_call_arguments.done","output_index":0,"arguments":"` + argumentsJSON + `"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_test","call_id":"call_test","name":"edit","arguments":"` + argumentsJSON + `"}}

data: {"type":"response.completed","sequence_number":5,"response":{"id":"resp_test","status":"completed"}}

`
	result, events := collectResponsesEvents(t, sse)

	if len(result.Content) != 1 {
		t.Fatalf("content blocks = %#v", result.Content)
	}
	persisted, ok := result.Content[0].(ToolCall)
	if !ok {
		t.Fatalf("content[0] = %T, want a tool call", result.Content[0])
	}
	if want := (JsonObject{"path": "README.md", "content": "updated"}); !reflect.DeepEqual(persisted.Arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", persisted.Arguments, want)
	}
	assertNoPartialJSON(t, "persisted block", persisted)

	var end *ToolCallEndEvent
	var sawPartialWhileStreaming bool
	for _, event := range events {
		switch event := event.(type) {
		case ToolCallEndEvent:
			end = &event
		case ToolCallDeltaEvent:
			// The streaming block still carries the argument text, so the cleanup assertion below can fail.
			encoded, err := json.Marshal(event.Partial.Observe().Content[event.ContentIndex])
			if err != nil {
				t.Fatal(err)
			}
			var block map[string]any
			if err := json.Unmarshal(encoded, &block); err != nil {
				t.Fatal(err)
			}
			if _, ok := block["partialJson"]; ok {
				sawPartialWhileStreaming = true
			}
		}
	}
	if end == nil {
		t.Fatal("no toolcall_end event")
	}
	if !sawPartialWhileStreaming {
		t.Error("no streaming block carried partialJson; the cleanup assertion proves nothing")
	}
	assertNoPartialJSON(t, "toolcall_end tool call", end.ToolCall)
	if !reflect.DeepEqual(end.ToolCall, persisted) {
		t.Errorf("toolcall_end tool call = %#v, want the persisted block %#v", end.ToolCall, persisted)
	}
}

func assertNoPartialJSON(t *testing.T, what string, call ToolCall) {
	t.Helper()
	encoded, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	var block map[string]any
	if err := json.Unmarshal(encoded, &block); err != nil {
		t.Fatal(err)
	}
	if _, present := block["partialJson"]; present {
		t.Errorf("%s still serializes partialJson: %s", what, encoded)
	}
}
