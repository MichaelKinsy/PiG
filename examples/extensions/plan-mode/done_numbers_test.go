package planmode

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

// utils.ts:extractDoneSteps retains every finite Number; count is not the number of matched todos.
func TestDoneStepsRetainsFiniteJavaScriptNumbers(t *testing.T) {
	message := "[DONE:0] [DONE:1] [DONE:9007199254740993] [DONE:9223372036854775807] [DONE:9223372036854775808] [DONE:100000000000000000000] [DONE:" + strings.Repeat("9", 309) + "]"
	want := []float64{0, 1, 9007199254740992, 9223372036854775808, 9223372036854775808, 1e20}
	if got := ExtractDoneSteps(message); !reflect.DeepEqual(got, want) {
		t.Errorf("parsed Numbers = %v, want %v", got, want)
	}
	if got := MarkCompletedSteps(message, nil); got != len(want) {
		t.Errorf("finite marker count = %d, want %d", got, len(want))
	}
}

func TestDoneStepsNeverConvertsLargeMarkerToNegativeStep(t *testing.T) {
	items := []TodoItem{{Step: math.MinInt, Text: "Not a positive step"}, {Step: 1, Text: "Actual step"}}
	if count := MarkCompletedSteps("[DONE:9223372036854775808] [DONE:1] [DONE:100000000000000000000]", items); count != 3 {
		t.Errorf("finite marker count = %d, want 3", count)
	}
	if items[0].Completed || !items[1].Completed {
		t.Errorf("completion state = %+v, want only step 1 completed", items)
	}
}

func TestPlanModeFiniteUnknownDoneMarkerUpdatesStatus(t *testing.T) {
	host := startExtensionHost(t, []string{"read"})
	host.selectValue = "Execute the plan (track progress)"
	host.invokeCommand("plan")
	host.invokeEvent("agent_end", map[string]any{"messages": []any{assistantMessage("Plan:\n1. Inspect the implementation")}})
	host.clearEffects()
	host.invokeEvent("turn_end", map[string]any{"message": assistantMessage("[DONE:100000000000000000000]")})
	if calls := host.callsFor("ui.setStatus"); len(calls) != 1 {
		t.Fatalf("finite marker status updates = %v, want one", calls)
	}
	if state := lastSavedState(t, host); state.Todos[0].Completed {
		t.Fatalf("unknown marker completed a step: %+v", state)
	}
}
