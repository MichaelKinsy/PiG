package ai

import (
	"reflect"
	"strings"
	"testing"
)

// A published snapshot's tool arguments were copied through cloneJSONValue when the cell published them, so observing the snapshot copies them without reading their strings again; the copy equals cloneJSONValue's and owns its containers.
func TestPublishedArgumentsCopyWithoutRevalidation(t *testing.T) {
	arguments := JsonObject{"path": "a.ts", "content": strings.Repeat("x", 1<<16), "nested": map[string]any{"list": []any{"y", 1.5, nil, true}}}
	published := JsonObject(cloneJSONValue(arguments).(map[string]any))
	copied := JsonObject(clonePublishedJSONValue(published).(map[string]any))
	if !reflect.DeepEqual(copied, JsonObject(cloneJSONValue(published).(map[string]any))) {
		t.Fatalf("published copy %#v differs from cloneJSONValue", copied)
	}
	copied["nested"].(map[string]any)["list"].([]any)[0] = "mutated"
	if published["nested"].(map[string]any)["list"].([]any)[0] != "y" {
		t.Fatal("the copy shares a container with the published snapshot")
	}
	// The string check is the publication's: an invalid byte the snapshot could not hold passes through unread.
	if got := clonePublishedJSONValue(map[string]any{"s": "\xff"}).(map[string]any)["s"]; got != "\xff" {
		t.Fatalf("published copy read the string: %q", got)
	}
	if got := cloneJSONValue(map[string]any{"s": "\xff"}).(map[string]any)["s"]; got == "\xff" {
		t.Fatal("cloneJSONValue kept an invalid byte; the check above proves nothing")
	}
}

// Every observation of a cell (the stable view, Observe, ShallowCopy, delivered, and Observe of a shallow copy) copies the published arguments without reading their strings: a published snapshot holds a byte the string check would rewrite, and each copy keeps it. A copy that re-validates reads the whole arguments per observer per streamed delta.
func TestCellObservationsCopyPublishedArgumentsUnread(t *testing.T) {
	cell := newAssistantMessageCell(&AssistantMessage{Model: "model"})
	// Planted as published state: publishing through the cell would itself rewrite the byte.
	cell.current.Content = []AssistantContentBlock{ToolCall{ID: "call_0", Name: "write", Arguments: JsonObject{"content": "\xff"}}}
	cell.nested.content = &cell.current.Content
	view := cell.view()
	shallow := view.ShallowCopy()
	for name, message := range map[string]*AssistantMessage{"view": view, "Observe": view.Observe(), "ShallowCopy": shallow, "delivered": view.delivered(), "shallow Observe": shallow.Observe()} {
		if got := message.Content[0].(ToolCall).Arguments["content"]; got != "\xff" {
			t.Errorf("%s re-validated the published arguments: %q", name, got)
		}
	}
}

// Observing a streamed tool call returns what a full clone of the published state returns.
func TestObservedStreamingToolCallEqualsFullClone(t *testing.T) {
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "test", "model")
	var observed, full []*AssistantMessage
	runManagedBuilder(builder, func() {
		builder.start()
		builder.toolCallDelta(streamToolCallDelta{index: 0, id: "call_0", name: "write"})
		for _, piece := range []string{`{"path":"a.ts",`, `"content":"line \u00e9`, `\n two \ud83d`, `\ude00 end"}`} {
			builder.toolCallDelta(streamToolCallDelta{index: 0, argumentsDelta: piece})
			view := builder.publish()
			observed = append(observed, view.Observe(), view.delivered(), view.ShallowCopy())
			current, _, _ := view.observation.capture(view)
			clone := cloneAssistantMessage(current)
			full = append(full, &clone, &clone, &clone)
		}
		builder.endToolCall(0)
		builder.done(StopReasonToolUse, &Usage{}, "")
	})
	for i := range observed {
		got, want := *observed[i], *full[i]
		got.observation, want.observation = nil, nil
		if !reflect.DeepEqual(got.Content, want.Content) {
			t.Fatalf("observation %d: %#v, want %#v", i, got.Content, want.Content)
		}
	}
}

// Only a published snapshot skips the string check: Observe on a message no cell published still copies its tool arguments through cloneJSONValue, so an invalid byte the caller wrote is replaced as before.
func TestObserveOfAnUnpublishedMessageStillChecksArgumentStrings(t *testing.T) {
	message := &AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "call_0", Name: "write", Arguments: JsonObject{"content": "a\xffb"}}}}
	got := message.Observe().Content[0].(ToolCall).Arguments["content"]
	want := cloneJSONValue(map[string]any{"content": "a\xffb"}).(map[string]any)["content"]
	if got != want || got == "a\xffb" {
		t.Fatalf("Observe of an unpublished message copied %q, want cloneJSONValue's %q", got, want)
	}
}
