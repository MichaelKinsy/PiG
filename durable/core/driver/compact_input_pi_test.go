package driver_test

// pi: packages/durable/src/harness/conversation.ts compact (harness-compaction.test.ts:735-765)

import (
	"encoding/json"
	"strings"
	"testing"
)

// conversation.compact(instructions) starts a manual compaction task whose input is `{reason: "manual"}` plus `instructions` only when given (harness-compaction.test.ts:735-765: the beforeCompact hook sees
// reason "manual" and instructions "why"; with undefined instructions the input has none).
// mutation-checked: sending `instructions` when nil, or never sending it, fails the cases.
func TestCoreCompactCarriesItsInstructionsInTheTaskInput(t *testing.T) {
	why := "why"
	for _, tc := range []struct {
		name         string
		instructions *string
		want         string
	}{
		{"with instructions", &why, `"instructions":"why"`},
		{"without instructions", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, root := openTasksHarness(t)
			id, err := root.Compact(t.Context(), tc.instructions)
			if err != nil {
				t.Fatal(err)
			}
			task, err := h.GetTask(t.Context(), id)
			if err != nil || task == nil {
				t.Fatalf("task = %v, %v", task, err)
			}
			raw, err := json.Marshal(task.Input)
			if err != nil {
				t.Fatal(err)
			}
			input := string(raw)
			if !strings.Contains(input, `"reason":"manual"`) {
				t.Errorf("input = %s, want reason manual", input)
			}
			if tc.want == "" {
				if strings.Contains(input, "instructions") {
					t.Errorf("input = %s, want no instructions", input)
				}
			} else if !strings.Contains(input, tc.want) {
				t.Errorf("input = %s, want %s", input, tc.want)
			}
		})
	}
}
