package durable

import "testing"

// Ports packages/durable/src/harness/types.ts CompactionReason: "manual" | "threshold" | "overflow".
func TestCompactionReasonValuesMatchPi(t *testing.T) {
	for reason, want := range map[CompactionReason]string{CompactionManual: "manual", CompactionThreshold: "threshold", CompactionOverflow: "overflow"} {
		if string(reason) != want {
			t.Errorf("CompactionReason %q, want %q", reason, want)
		}
	}
}
