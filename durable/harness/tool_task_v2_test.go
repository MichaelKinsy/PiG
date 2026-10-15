package harness

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// Pi 42a3497d harness/tool.ts: the built-in tool task is version 2 and its input and result are tagged unions
// (`kind: "model"` for a model-issued call, `kind: "nested"` for a call a running tool made through executeTool()).
// Version 1 had only model-issued calls and no `kind`.

func TestToolTaskIsVersion2(t *testing.T) {
	if got := ToolTask.AnyDefinition().Version; got != 2 {
		t.Fatalf("pi.tool version = %d, want 2 (tool.ts ToolTask)", got)
	}
}

func TestToolTaskUnionsSerializeLikePi(t *testing.T) {
	progressOff := false
	call := ai.ToolCall{ID: "c1", Name: "read", Arguments: map[string]any{"path": "a"}}
	cases := []struct {
		name  string
		value any
		want  string
	}{
		// tool.ts:52-62 member order: kind first.
		{"model input", ToolTaskInput{Kind: "model", Assistant: 7, CallId: "call_1"}, `{"kind":"model","assistant":7,"callId":"call_1"}`},
		{"nested input", ToolTaskInput{Kind: "nested", Parent: 3, ParentCallId: "p1", Key: "k", Call: &call}, `{"kind":"nested","parent":3,"parentCallId":"p1","key":"k","call":{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"a"}}}`},
		{"nested input without progress", ToolTaskInput{Kind: "nested", Parent: 3, ParentCallId: "p1", Key: "k", Call: &call, Progress: &progressOff}, `{"kind":"nested","parent":3,"parentCallId":"p1","key":"k","call":{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"a"}},"progress":false}`},
		// tool.ts:74: `{ kind: "model"; entryId; control? } | { kind: "nested" }`.
		{"model result", ToolTaskResult{Kind: "model", EntryId: 8}, `{"kind":"model","entryId":8}`},
		{"model result with control", ToolTaskResult{Kind: "model", EntryId: 8, Control: &durable.ToolControl{AddTools: []string{"a"}}}, `{"kind":"model","entryId":8,"control":{"addTools":["a"]}}`},
		{"nested result", ToolTaskResult{Kind: "nested"}, `{"kind":"nested"}`},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.value)
		if err != nil || string(got) != c.want {
			t.Errorf("%s: %s, %v; want %s", c.name, got, err, c.want)
		}
	}
}

// A version 1 task migrates to `{ kind: "model", ...input }` with its checkpoint unchanged (tool.ts:111-115); a version 1
// task that was already `completing` finishes with a result that has no `kind`, read as a model-issued call's (tool.ts:70-73).
func TestToolTaskMigratesVersion1(t *testing.T) {
	migrate := ToolTask.AnyDefinition().Migrate
	if migrate == nil {
		t.Fatal("pi.tool has no migration from version 1")
	}
	input, err := durable.ToJsonValue(map[string]any{"assistant": 7.0, "callId": "call_1"})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := durable.ToJsonValue(map[string]any{"phase": "call"})
	if err != nil {
		t.Fatal(err)
	}
	gotInput, gotCheckpoint, err := migrate(input, checkpoint, 1)
	if err != nil {
		t.Fatal(err)
	}
	in, _ := json.Marshal(gotInput)
	cp, _ := json.Marshal(gotCheckpoint)
	if string(in) != `{"kind":"model","assistant":7,"callId":"call_1"}` || string(cp) != `{"phase":"call"}` {
		t.Errorf("migrated input %s checkpoint %s", in, cp)
	}
	var result ToolTaskResult
	if err := json.Unmarshal([]byte(`{"entryId":8}`), &result); err != nil || result.EntryId != 8 || (result.Kind != "model" && result.Kind != "") {
		t.Errorf("a version 1 result without kind decodes as %+v, %v", result, err)
	}
}
