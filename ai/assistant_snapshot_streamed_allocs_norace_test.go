//go:build !race

package ai

import (
	"reflect"
	"testing"
)

// Publishing a streamed tool call whose arguments are the parser's immutable snapshot keeps that snapshot and the block the producer already boxed: the
// only allocation of a publication is the content slice. Boxing the call again on every delta cost one more object per delta of every streamed tool call.
func TestCloneAssistantContentSharingReusesTheBoxedStreamedCall(t *testing.T) {
	first := JsonObject{"path": "a.txt", "content": "x"}
	previous := cloneAssistantContent([]AssistantContentBlock{ToolCall{ID: "call", Name: "write", Arguments: first}})
	snapshot := JsonObject{"path": "a.txt", "content": "xy"}
	blocks := []AssistantContentBlock{ToolCall{ID: "call", Name: "write", Arguments: snapshot}}
	streamed := map[int]JsonObject{0: snapshot}
	var got []AssistantContentBlock
	allocs := testing.AllocsPerRun(100, func() { got = cloneAssistantContentSharing(blocks, previous, streamed) })
	if allocs > 1 {
		t.Errorf("cloneAssistantContentSharing allocates %.0f objects for a streamed tool call, want 1 (the content slice)", allocs)
	}
	if call := got[0].(ToolCall); reflect.ValueOf(call.Arguments).Pointer() != reflect.ValueOf(snapshot).Pointer() {
		t.Error("the publication copied the streamed snapshot instead of keeping it")
	}
	if !reflect.DeepEqual(got, blocks) {
		t.Errorf("published content = %#v, want %#v", got, blocks)
	}
}
