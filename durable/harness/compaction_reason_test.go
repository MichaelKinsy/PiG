package harness

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// CompactionReason is the union "manual" | "threshold" | "overflow" (packages/durable/src/harness/types.ts); each constant is the exact wire value that the compaction input, the compaction events and the pi.compaction entry carry.
func TestCompactionReasonConstantsAreTheUpstreamUnionMembers(t *testing.T) {
	for reason, want := range map[durable.CompactionReason]string{
		durable.CompactionManual:    "manual",
		durable.CompactionThreshold: "threshold",
		durable.CompactionOverflow:  "overflow",
	} {
		input, err := json.Marshal(CompactionInput{Reason: reason})
		if err != nil || string(input) != `{"reason":"`+want+`"}` {
			t.Fatalf("CompactionInput{%q} = %s, %v; want reason %q", reason, input, err, want)
		}
		event, err := json.Marshal(CompactionStartEvent{TaskId: 7, Reason: reason, Blocking: true})
		if err != nil || string(event) != `{"type":"compaction_start","taskId":7,"reason":"`+want+`","blocking":true}` {
			t.Fatalf("CompactionStartEvent{%q} = %s, %v", reason, event, err)
		}
		var decoded CompactionEndEvent
		if err := json.Unmarshal([]byte(`{"taskId":7,"reason":"`+want+`"}`), &decoded); err != nil || decoded.Reason != reason {
			t.Fatalf("CompactionEndEvent reason %q decoded as %q, %v", want, decoded.Reason, err)
		}
	}
}
