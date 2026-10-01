package ai

import (
	"reflect"
	"testing"
)

// createSlot replaces the slot at an output index only for an item type that opens a block; for any other item it returns undefined and leaves the
// slot in place (openai-responses-shared.ts createSlot). Without output_index every event shares one index, so a web_search_call added in the middle of
// a message must not drop the rest of the message's text.
func TestResponsesOutputItemWithoutABlockKeepsTheOpenSlot(t *testing.T) {
	wire := []string{
		`{"type":"response.created","response":{"id":"resp_slot"}}`,
		`{"type":"response.output_item.added","item":{"type":"message","id":"msg_1","role":"assistant","status":"in_progress","content":[]}}`,
		`{"type":"response.output_text.delta","delta":"a"}`,
		`{"type":"response.output_item.added","item":{"type":"web_search_call","id":"ws_1","status":"in_progress"}}`,
		`{"type":"response.output_text.delta","delta":"b"}`,
		`{"type":"response.output_item.done","item":{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ab"}]}}`,
		`{"type":"response.completed","response":{"id":"resp_slot","status":"completed"}}`,
	}
	provider, err := directAPIProvider(chatGPTTestModel("https://api.openai.com/v1", nil), "sk-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := provider.Stream(t.Context(), responsesUpstreamContext(), StreamOptions{Fetch: sseResponse(sseFrames(wire...))})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	for event := range stream.Events(t.Context()) {
		if delta, ok := event.(TextDeltaEvent); ok {
			deltas = append(deltas, delta.Delta)
		}
	}
	result := stream.Result()
	if result.StopReason != StopReasonStop || len(result.Content) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if text, ok := result.Content[0].(TextContent); !ok || text.Text != "ab" {
		t.Fatalf("content = %#v", result.Content)
	}
	if !reflect.DeepEqual(deltas, []string{"a", "b"}) {
		t.Fatalf("text deltas = %q", deltas)
	}
}
