package harness

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// The built-in tasks persist their input, checkpoint and result as JSON, so the member names and order are the Pi object literals'. Each case cites
// the Pi declaration and the literal that builds the value (durable/src/harness/compaction.ts, tool.ts, generation.ts).
func TestHarnessTaskDataTypesSerializeLikePi(t *testing.T) {
	model := durable.ModelRef{Provider: "p", ModelId: "m"}
	request := &SummaryRequest{Attempt: 1, Model: model, ThinkingLevel: "off", MaxTokens: 100, Tail: 9, FirstKept: 4}
	until := 1234.0
	terminate := true
	handoff := "agent"
	cases := []struct {
		name  string
		value any
		want  string
	}{
		// compaction.ts:46-49 and :106 (`{ phase: "select" }`): the select phase has no request members.
		{"checkpoint select", CompactionCheckpoint{Phase: "select"}, `{"phase":"select"}`},
		// compaction.ts:146 `{ phase: "summarize", ...request }`: phase, then the request's members in declaration order (:34-44).
		{"checkpoint summarize", CompactionCheckpoint{Phase: "summarize", SummaryRequest: request}, `{"phase":"summarize","attempt":1,"model":{"provider":"p","modelId":"m"},"thinkingLevel":"off","streamOptions":{},"maxTokens":100,"tail":9,"firstKept":4}`},
		// compaction.ts:198 `{ phase: "retry", ...request, until }`: until comes last.
		{"checkpoint retry", CompactionCheckpoint{Phase: "retry", SummaryRequest: request, Until: &until}, `{"phase":"retry","attempt":1,"model":{"provider":"p","modelId":"m"},"thinkingLevel":"off","streamOptions":{},"maxTokens":100,"tail":9,"firstKept":4,"until":1234}`},
		// compaction.ts:34-44.
		{"summary request", request, `{"attempt":1,"model":{"provider":"p","modelId":"m"},"thinkingLevel":"off","streamOptions":{},"maxTokens":100,"tail":9,"firstKept":4}`},
		// tool.ts:33 and :40: `control` is optional, `terminate` is the literal true, absent otherwise.
		{"tool input", ToolTaskInput{Assistant: 7, CallId: "call_1"}, `{"assistant":7,"callId":"call_1"}`},
		{"tool result without control", ToolTaskResult{EntryId: 8}, `{"entryId":8}`},
		{"tool result with control", ToolTaskResult{EntryId: 8, Control: &durable.ToolControl{AddTools: []string{"a"}, Terminate: terminate, Handoff: &handoff}}, `{"entryId":8,"control":{"addTools":["a"],"terminate":true,"handoff":"agent"}}`},
		// generation.ts GenerationResult `{ entryId }`.
		{"generation result", GenerationResult{EntryId: 12}, `{"entryId":12}`},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.value)
		if err != nil || string(got) != c.want {
			t.Errorf("%s: %s, %v; want %s", c.name, got, err, c.want)
		}
	}
	var back CompactionCheckpoint
	if err := json.Unmarshal([]byte(cases[2].want), &back); err != nil || back.Phase != "retry" || back.SummaryRequest == nil || back.Tail != 9 || back.Until == nil || *back.Until != until {
		t.Errorf("retry checkpoint round trip = %+v, %v", back, err)
	}
}
