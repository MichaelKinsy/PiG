package planmode

import (
	"encoding/json"
	"reflect"
	"testing"
)

// index.ts:persistState omits undefined but retains toolsBeforePlanMode: [].
func TestPlanModeStateDistinguishesAbsentAndEmptyTools(t *testing.T) {
	for _, tools := range [][]string{nil, {}, {"read"}} {
		raw, err := json.Marshal(planModeState{ToolsBeforePlanMode: tools})
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		field, present := fields["toolsBeforePlanMode"]
		if present != (tools != nil) {
			t.Errorf("snapshot %#v serialized as %s", tools, raw)
		}
		if tools != nil {
			want, err := json.Marshal(tools)
			if err != nil {
				t.Fatal(err)
			}
			if string(field) != string(want) {
				t.Errorf("tools snapshot = %s, want %s", field, want)
			}
		}
	}
}

func TestPlanModeEmptyToolSnapshotSurvivesSaveResumeToggle(t *testing.T) {
	host := startExtensionHost(t, []string{})
	host.invokeCommand("plan")
	calls := host.callsFor("appendEntry")
	if len(calls) != 1 {
		t.Fatalf("save calls = %v", calls)
	}
	// Reuse the actual emitted payload, not an idealized handcrafted state.
	entry, err := json.Marshal(map[string]any{"type": "custom", "customType": "plan-mode", "data": calls[0].args["data"]})
	if err != nil {
		t.Fatal(err)
	}
	resumed := startExtensionHost(t, []string{"read", "bash", "edit", "write"})
	resumed.entries = []json.RawMessage{entry}
	resumed.invokeEvent("session_start", map[string]any{})
	resumed.invokeCommand("plan")
	if len(resumed.activeTools) != 0 {
		t.Fatalf("restored tools = %v, want empty saved set; entry = %s", resumed.activeTools, entry)
	}
	sets := resumed.callsFor("setActiveTools")
	if got := sets[len(sets)-1].args["tools"]; !reflect.DeepEqual(got, []any{}) {
		t.Fatalf("restored tools wire value = %#v, want []", got)
	}
	last := resumed.callsFor("appendEntry")
	data, _ := last[len(last)-1].args["data"].(map[string]any)
	if _, present := data["toolsBeforePlanMode"]; present {
		t.Fatalf("disabled plan retained snapshot: %v", data)
	}
}
