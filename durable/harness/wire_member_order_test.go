package harness

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Pi writes these variants as object literals, so JSON.stringify lists their members in literal order: events.ts:372-385 builds each
// MessageChange as { type, contentIndex, delta | path, delta | block }, and task-graph.ts:210-212 builds the graph state as
// { status, phase } or { status: "waiting", phase, on, policy }. A Go map would list them sorted.
func TestMessageChangeAndTaskGraphStateKeepPiMemberOrder(t *testing.T) {
	for _, c := range []struct {
		value any
		want  string
	}{
		{MessageChange{Type: "text_delta", ContentIndex: 1, Delta: "x"}, `{"type":"text_delta","contentIndex":1,"delta":"x"}`},
		{MessageChange{Type: "toolcall_delta", ContentIndex: 2, Path: []any{"a", 0.0}, Delta: "y"}, `{"type":"toolcall_delta","contentIndex":2,"path":["a",0],"delta":"y"}`},
		{TaskGraphState{Status: durable.TaskRunning, Phase: "effect"}, `{"status":"running","phase":"effect"}`},
		{TaskGraphState{Status: durable.TaskWaiting, Phase: "join", On: []durable.TaskId{4}, Policy: durable.JoinAllSettled}, `{"status":"waiting","phase":"join","on":[4],"policy":"allSettled"}`},
	} {
		got, err := json.Marshal(c.value)
		if err != nil || string(got) != c.want {
			t.Fatalf("%#v encodes as %s (%v), want %s", c.value, got, err, c.want)
		}
	}
}
