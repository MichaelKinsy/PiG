package parity

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestMarshalToolCallEvent_AllVariantsEmitCorrectDiscriminator is the
// **marshal-direction** parity gate. The mirror of
// TestUnmarshalToolCallEvent_AllUpstreamToolNamesDispatch in
// dispatch_parity_test.go.
//
// **The hazard.** A Go variant struct could ship with the wrong
// `ToolName` field literal hardcoded in its construction call sites, or
// with a missing/wrong JSON tag, and `MarshalToolCallEvent` would emit
// JSON that does NOT round-trip back to the same Go variant. The
// dispatch-parity gate (Unmarshal direction) wouldn't catch this: it
// proves "given upstream-shaped wire bytes, dispatch produces the right
// Go type" but not "given the right Go type, marshal produces
// upstream-shaped wire bytes".
//
// **The gate.** For every upstream `XxxToolCallEvent` interface that has
// a literal `toolName: "<value>"` discriminator (i.e. excluding
// `CustomToolCallEvent`), this test:
//
//  1. Looks up the Go variant in the eventTypeRegistry.
//  2. Constructs a zero-value instance via reflection.
//  3. Sets ToolName + Type to the upstream literals.
//  4. Marshals via [extension.MarshalToolCallEvent].
//  5. Parses the resulting JSON and asserts `toolName` + `type`
//     fields match the upstream literals byte-for-byte.
//
// This catches: missing `ToolName` field on the variant; wrong JSON
// tag on `ToolName` (e.g. `tool_name`); wrong `Type` field; missing
// case in the marshal switch (returns error); accidental HTML-escape
// regression that would mangle special characters in literals.
//
// Together with TestUnmarshalToolCallEvent_AllUpstreamToolNamesDispatch
// this provides full bi-directional parity coverage for the sum-type
// wire boundary.
func TestMarshalToolCallEvent_AllVariantsEmitCorrectDiscriminator(t *testing.T) {
	surface, err := LoadUpstream()
	if err != nil {
		t.Fatalf("LoadUpstream: %v", err)
	}

	for _, ev := range surface.EventTypes {
		if !strings.HasSuffix(ev.Name, "ToolCallEvent") || ev.Name == "CustomToolCallEvent" {
			continue
		}
		toolName, eventType, ok := upstreamDiscriminators(ev.Fields, "tool_call")
		if !ok {
			continue
		}
		t.Run(ev.Name, func(t *testing.T) {
			assertMarshalDiscriminators(t, ev.Name, toolName, eventType, "tool_call",
				func(v any) ([]byte, error) {
					ev, ok := v.(extension.ToolCallEvent)
					if !ok {
						return nil, errNotToolCallEvent
					}
					return extension.MarshalToolCallEvent(ev)
				})
		})
	}
}

// TestMarshalToolResultEvent_AllVariantsEmitCorrectDiscriminator -
// symmetric gate for ToolResultEvent.
func TestMarshalToolResultEvent_AllVariantsEmitCorrectDiscriminator(t *testing.T) {
	surface, err := LoadUpstream()
	if err != nil {
		t.Fatalf("LoadUpstream: %v", err)
	}

	for _, ev := range surface.EventTypes {
		if !strings.HasSuffix(ev.Name, "ToolResultEvent") || ev.Name == "CustomToolResultEvent" {
			continue
		}
		toolName, eventType, ok := upstreamDiscriminators(ev.Fields, "tool_result")
		if !ok {
			continue
		}
		t.Run(ev.Name, func(t *testing.T) {
			assertMarshalDiscriminators(t, ev.Name, toolName, eventType, "tool_result",
				func(v any) ([]byte, error) {
					ev, ok := v.(extension.ToolResultEvent)
					if !ok {
						return nil, errNotToolResultEvent
					}
					return extension.MarshalToolResultEvent(ev)
				})
		})
	}
}

// upstreamDiscriminators extracts the literal `toolName` and `type`
// values from a parsed upstream interface's fields, returning ok=false
// if either is missing or non-literal (i.e. `string` or empty).
func upstreamDiscriminators(fields map[string]string, defaultEventType string) (toolName, eventType string, ok bool) {
	tn, has := fields["toolName"]
	if !has {
		return "", "", false
	}
	tn = strings.Trim(tn, `"`)
	if tn == "string" || tn == "" {
		return "", "", false
	}
	et := defaultEventType
	if rawType, has := fields["type"]; has {
		et = strings.Trim(rawType, `"`)
		if et == "string" || et == "" {
			et = defaultEventType
		}
	}
	return tn, et, true
}

// assertMarshalDiscriminators is the shared body for the ToolCall and
// ToolResult marshal-parity tests. It does the reflection ritual:
// look up the Go type, construct an instance with the discriminators
// set, marshal it through the supplied function, parse the result,
// and assert the discriminators round-trip correctly.
func assertMarshalDiscriminators(
	t *testing.T,
	upstreamName, toolName, eventType, defaultEventType string,
	marshal func(any) ([]byte, error),
) {
	t.Helper()

	goType, found := eventTypeRegistry[upstreamName]
	if !found {
		gap(t, "event:"+upstreamName, "%s missing from eventTypeRegistry: add it to registry.go", upstreamName)
		return
	}
	if goType.Kind() != reflect.Struct {
		t.Fatalf("%s registry entry is not a struct (kind=%s)", upstreamName, goType.Kind())
	}

	// Construct zero instance, set ToolName + Type via reflection.
	instPtr := reflect.New(goType)
	inst := instPtr.Elem()

	tnField := inst.FieldByName("ToolName")
	if !tnField.IsValid() {
		t.Fatalf("%s has no ToolName field", upstreamName)
	}
	if !tnField.CanSet() {
		t.Fatalf("%s.ToolName is not settable (unexported?)", upstreamName)
	}
	tnField.SetString(toolName)

	// Type may be on the outer struct OR on an embedded base struct
	// (e.g. ToolCallEventBase). reflect.FieldByName traverses anonymous
	// fields, so a single FieldByName("Type") finds either.
	typeField := inst.FieldByName("Type")
	if !typeField.IsValid() {
		t.Fatalf("%s has no Type field (neither direct nor on embedded base)", upstreamName)
	}
	if typeField.CanSet() {
		typeField.SetString(eventType)
	}

	// Marshal through the sum-type helper.
	data, err := marshal(inst.Interface())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Parse and assert the wire-format discriminator fields.
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse marshaled output: %v\n  bytes: %s", err, data)
	}
	if got := parsed["toolName"]; got != toolName {
		t.Errorf("wire toolName field:\n  got:  %v\n  want: %q\n  full bytes: %s", got, toolName, data)
	}
	if got := parsed["type"]; got != eventType {
		t.Errorf("wire type field:\n  got:  %v\n  want: %q\n  full bytes: %s", got, eventType, data)
	}
}

// Sentinels for the marshal helpers above. Test-only: production code
// uses the typed sealed-interface methods directly.
var (
	errNotToolCallEvent   = sentinelError("registry entry is not a ToolCallEvent")
	errNotToolResultEvent = sentinelError("registry entry is not a ToolResultEvent")
)

type sentinelError string

func (e sentinelError) Error() string { return string(e) }
