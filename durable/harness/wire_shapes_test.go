package harness

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Pi packages/durable/src/harness/tool.ts:33 ToolTaskInput = { assistant: EntryId; callId: string }: the task input is exactly those two members on the wire.
func TestToolTaskInputWireShape(t *testing.T) {
	data, err := json.Marshal(ToolTaskInput{Assistant: 7, CallId: "call-1"})
	if err != nil || string(data) != `{"assistant":7,"callId":"call-1"}` {
		t.Fatalf("ToolTaskInput = %s, %v", data, err)
	}
	var back ToolTaskInput
	if err := json.Unmarshal(data, &back); err != nil || back.Assistant != 7 || back.CallId != "call-1" {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
}

// Pi packages/durable/src/harness/compaction.ts:46-49 CompactionCheckpoint: select carries no request members, summarize and retry carry the pinned SummaryRequest, and only retry carries until.
func TestCompactionCheckpointWireShapes(t *testing.T) {
	request := &SummaryRequest{Attempt: 1, Model: durable.ModelRef{Provider: "p", ModelId: "m"}, ThinkingLevel: "off", MaxTokens: 100, Tail: 9, FirstKept: 5}
	until := 12.5
	for name, tc := range map[string]struct {
		checkpoint CompactionCheckpoint
		members    map[string]bool
	}{
		"select":    {CompactionCheckpoint{Phase: "select"}, map[string]bool{"phase": true}},
		"summarize": {CompactionCheckpoint{Phase: "summarize", SummaryRequest: request}, map[string]bool{"phase": true, "attempt": true, "model": true, "thinkingLevel": true, "streamOptions": true, "maxTokens": true, "tail": true, "firstKept": true}},
		"retry":     {CompactionCheckpoint{Phase: "retry", SummaryRequest: request, Until: &until}, map[string]bool{"phase": true, "until": true, "attempt": true, "model": true, "thinkingLevel": true, "streamOptions": true, "maxTokens": true, "tail": true, "firstKept": true}},
	} {
		data, err := json.Marshal(tc.checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		var members map[string]json.RawMessage
		if err := json.Unmarshal(data, &members); err != nil {
			t.Fatal(err)
		}
		if len(members) != len(tc.members) {
			t.Errorf("%s members = %s, want %v", name, data, tc.members)
		}
		for member := range tc.members {
			if _, ok := members[member]; !ok {
				t.Errorf("%s lacks %q: %s", name, member, data)
			}
		}
	}
}
