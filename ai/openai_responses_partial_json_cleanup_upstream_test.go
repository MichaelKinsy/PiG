package ai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// upstream: packages/ai/test/openai-responses-partial-json-cleanup.test.ts
// Upstream keeps the streamed argument text in a partialJson property on the block and deletes it at response.output_item.done. Since Pi f715a15c1 the provider keeps it in its own stream state (ToolCallArgumentsStream) and never on the block; a Go ToolCall has no such member either, so the observable contract is the same: no block, streaming, persisted or emitted on toolcall_end, carries partialJson.

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
	for _, event := range events {
		switch event := event.(type) {
		case ToolCallEndEvent:
			end = &event
		case ToolCallDeltaEvent:
			// Pi keeps the argument text in the provider's own stream state, so no block carries it while the call streams.
			assertNoPartialJSON(t, "streaming block", event.Partial.Observe().Content[event.ContentIndex].(ToolCall))
		}
	}
	if end == nil {
		t.Fatal("no toolcall_end event")
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

// Pi 87f9a364 (#10897): "marks malformed final arguments instead of keeping the parsed prefix".
func TestOpenAIResponsesMarksMalformedFinalArguments(t *testing.T) {
	const argumentsJSON = `{\"path\":\"README.md\",\"content\":\"updated\",\"mode\":.5}`
	sse := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_test","call_id":"call_test","name":"edit","arguments":""}}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"` + argumentsJSON + `"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_test","call_id":"call_test","name":"edit","arguments":"` + argumentsJSON + `"}}

data: {"type":"response.completed","sequence_number":5,"response":{"id":"resp_test","status":"completed"}}

`
	result, _ := collectResponsesEvents(t, sse)
	call, ok := result.Content[0].(ToolCall)
	if !ok {
		t.Fatalf("content[0] = %T", result.Content[0])
	}
	if len(call.Arguments) != 0 || !strings.HasSuffix(call.ArgumentsError, "Received arguments:\n"+`{"path":"README.md","content":"updated","mode":.5}`) {
		t.Fatalf("arguments = %#v error = %q", call.Arguments, call.ArgumentsError)
	}
}
