package sdk

import (
	"encoding/json"
	"fmt"
	"testing"
)

// Pi ToolDefinition.constrainedSampling is false | ConstrainedSamplingConfig | undefined. Explicit false stays present; nil and a nil configuration pointer are omitted.
func TestToolSamplingFalsePresence(t *testing.T) {
	ext := New("sampling")
	var nilConfig *ConstrainedSampling
	for i, value := range []ToolConstrainedSampling{nil, DisabledConstrainedSampling{}, nilConfig, &ConstrainedSampling{Type: "json_schema", Strict: "prefer"}} {
		ext.RegisterTool(ToolDefinition{Name: fmt.Sprint("probe", i), Label: "Probe", Parameters: Schema{"type": "object"}, ConstrainedSampling: value, Execute: func(Context, map[string]any) (any, error) { return "ok", nil }})
	}
	ext.ToolWithConstrainedSampling("helper", "Helper", Schema{"type": "object"}, DisabledConstrainedSampling{}, func(Context, map[string]any) (any, error) { return "ok", nil })
	data, err := json.Marshal(ext.tools)
	if err != nil {
		t.Fatal(err)
	}
	var tools []map[string]any
	if err := json.Unmarshal(data, &tools); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 2} {
		if _, ok := tools[i]["constrained_sampling"]; ok {
			t.Fatalf("omitted sampling %d was sent: %s", i, data)
		}
	}
	for _, i := range []int{1, 4} {
		if value, ok := tools[i]["constrained_sampling"]; !ok || value != false {
			t.Fatalf("explicit false %d = %s", i, data)
		}
	}
	if config, ok := tools[3]["constrained_sampling"].(map[string]any); !ok || config["type"] != "json_schema" || config["strict"] != "prefer" {
		t.Fatalf("configuration = %s", data)
	}
}
