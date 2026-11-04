package harness

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Pi writes these variants as object literals, so JSON.stringify lists their members in literal order: task-graph.ts:210-212
// builds the graph state as { status, phase } or { status: "waiting", phase, on, policy }. A Go map would list them sorted.
func TestTaskGraphStateKeepsPiMemberOrder(t *testing.T) {
	for _, c := range []struct {
		value any
		want  string
	}{
		{TaskGraphState{Status: durable.TaskRunning, Phase: "effect"}, `{"status":"running","phase":"effect"}`},
		{TaskGraphState{Status: durable.TaskWaiting, Phase: "join", On: []durable.TaskId{4}, Policy: durable.JoinAllSettled}, `{"status":"waiting","phase":"join","on":[4],"policy":"allSettled"}`},
	} {
		got, err := json.Marshal(c.value)
		if err != nil || string(got) != c.want {
			t.Fatalf("%#v encodes as %s (%v), want %s", c.value, got, err, c.want)
		}
	}
}
