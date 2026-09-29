package parity

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestUnmarshalToolCallEvent_AllUpstreamToolNamesDispatch is the silent-drift
// gate for sealed-interface sum types.
//
// **The hazard.** When upstream pi adds a new builtin tool variant
// (e.g. `RgToolCallEvent` with `toolName: "rg"`), pig must:
//  1. Add the Go variant struct to coding/extension/events.go.
//  2. Add the marker method (`func (RgToolCallEvent) isToolCallEvent() {}`).
//  3. Add a `case "rg":` to UnmarshalToolCallEvent in marshalling.go.
//
// If step 3 is forgotten, payloads with `{"toolName":"rg",...}` fall through
// to the `default:` branch and unmarshal as `CustomToolCallEvent`. The
// extension's `OnToolCall` handler that type-switches on `*RgToolCallEvent`
// never fires. The bug surfaces only when a user reports it: silent drift.
//
// **The gate.** This test walks every upstream `XxxToolCallEvent` interface
// declared in `.upstream/current/.../types.ts`, extracts the literal
// `toolName` discriminator, and asserts that pig's
// `extension.UnmarshalToolCallEvent` dispatches each one to the matching
// Go variant: NOT to `CustomToolCallEvent`.
//
// **What it does NOT cover.** A net-new `XxxToolCallEvent` interface added
// upstream that pig has not yet ported gets caught by the existing
// `TestEventTypes_AllUpstreamEventTypesExistInGo` gate (registry mismatch).
// This test layers over the top: assuming the variant exists in Go, does
// the dispatch work?
func TestUnmarshalToolCallEvent_AllUpstreamToolNamesDispatch(t *testing.T) {
	surface, err := LoadUpstream()
	if err != nil {
		t.Fatalf("LoadUpstream: %v", err)
	}

	for _, ev := range surface.EventTypes {
		if !strings.HasSuffix(ev.Name, "ToolCallEvent") {
			continue
		}
		// CustomToolCallEvent has `toolName: string` (no literal); dispatch
		// for it is the default fallback, which is correct by construction.
		if ev.Name == "CustomToolCallEvent" {
			continue
		}
		toolName, ok := ev.Fields["toolName"]
		if !ok {
			continue // not a per-tool variant (e.g. ToolExecutionStartEvent)
		}
		// The parser stores the literal type as the raw TS string with
		// quotes (e.g. `"bash"`). Strip them.
		toolName = strings.Trim(toolName, `"`)
		if toolName == "string" || toolName == "" {
			// untyped or generic discriminator: not a fixed variant
			continue
		}
		t.Run(ev.Name+"_"+toolName, func(t *testing.T) {
			payload := []byte(`{"type":"tool_call","toolCallId":"x","toolName":"` + toolName + `","input":{}}`)
			got, err := extension.UnmarshalToolCallEvent(payload)
			if err != nil {
				t.Fatalf("UnmarshalToolCallEvent: %v", err)
			}
			gotName := typeName(got)
			// "Custom" fallback is the silent-drift indicator: it means
			// dispatch fell through. Acceptable only when toolName=="";
			// here toolName is a known builtin literal.
			if gotName == "CustomToolCallEvent" {
				gap(t, "dispatch:tool_call:"+toolName, "toolName=%q fell through to CustomToolCallEvent: missing case in marshalling.go UnmarshalToolCallEvent", toolName)
				return
			}
			if gotName != ev.Name {
				t.Errorf("toolName=%q dispatched to %s, want %s", toolName, gotName, ev.Name)
			}
		})
	}
}

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
