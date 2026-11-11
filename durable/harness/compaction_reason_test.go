package harness

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// CompactionReason is the union "manual" | "threshold" | "overflow" (packages/durable/src/harness/types.ts); each constant is the exact wire value that the compaction input and the pi.compaction entry carry.
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
	}
}
