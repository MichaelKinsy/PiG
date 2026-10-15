package parity

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestUnmarshalToolResultEvent_AllUpstreamToolNamesDispatch: symmetric
// gate for ToolResultEvent. Same drift hazard, same dispatch rule.
func TestUnmarshalToolResultEvent_AllUpstreamToolNamesDispatch(t *testing.T) {
	surface, err := LoadUpstream()
	if err != nil {
		t.Fatalf("LoadUpstream: %v", err)
	}

	for _, ev := range surface.EventTypes {
		if !strings.HasSuffix(ev.Name, "ToolResultEvent") {
			continue
		}
		if ev.Name == "CustomToolResultEvent" {
			continue
		}
		toolName, ok := ev.Fields["toolName"]
		if !ok {
			continue
		}
		toolName = strings.Trim(toolName, `"`)
		if toolName == "string" || toolName == "" {
			continue
		}
		t.Run(ev.Name+"_"+toolName, func(t *testing.T) {
			payload := []byte(`{"type":"tool_result","toolCallId":"x","toolName":"` + toolName + `","content":[]}`)
			got, err := extension.UnmarshalToolResultEvent(payload)
			if err != nil {
				t.Fatalf("UnmarshalToolResultEvent: %v", err)
			}
			gotName := typeName(got)
			if gotName == "CustomToolResultEvent" {
				gap(t, "dispatch:tool_result:"+toolName, "toolName=%q fell through to CustomToolResultEvent: missing case in marshalling.go UnmarshalToolResultEvent", toolName)
				return
			}
			if gotName != ev.Name {
				t.Errorf("toolName=%q dispatched to %s, want %s", toolName, gotName, ev.Name)
			}
		})
	}
}

// typeName returns the unqualified Go type name (e.g. "BashToolCallEvent")
// of a value, used for dispatch-correctness assertions.
func typeName(v any) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", v), "extension.")
}
