package parity

import (
	"reflect"
	"strings"
	"testing"
)

// TestJSONTags_AllEventFieldsAreCamelCase asserts that every Go event struct
// field has a `json:"<camelCaseName>"` tag matching the upstream TS field
// name. This guarantees:
//
//  1. Session JSONL written by upstream pi can be round-tripped through pig
//     without field-name mismatch.
//  2. The subprocess JSON protocol derived from these structs uses the same
//     field names as upstream's JSON, so cross-language SDKs see identical
//     names.
//
// Without this gate, a worker can innocently write
//
//	ToolName string  // no json tag
//
// which marshals to `"ToolName"` (Go default) instead of upstream's
// `"toolName"`. Drift becomes invisible until a user moves a session between
// the two binaries.
func TestJSONTags_AllEventFieldsAreCamelCase(t *testing.T) {
	s, err := LoadUpstream()
	if err != nil {
		t.Fatalf("upstream mirror missing: %v", err)
	}
	for _, decl := range append(append([]EventTypeDecl{}, s.EventTypes...), s.EventResults...) {
		goType, ok := eventTypeRegistry[decl.Name]
		if !ok {
			continue
		}
		if goType.Kind() != reflect.Struct {
			continue
		}
		for _, fieldName := range decl.FieldOrder {
			goName := CamelToGoField(fieldName)
			f, found := goType.FieldByName(goName)
			if !found {
				continue // covered by Test*_AllUpstreamFieldsHaveGoCounterpart
			}
			tag := f.Tag.Get("json")
			if tag == "-" {
				// Explicitly excluded from wire format. Common for fields
				// that hold non-serialisable Go types (e.g. context.Context
				// from the D8 AbortSignal→context migration). The pig side
				// has consciously chosen not to wire-format the field; the
				// divergence is documented in DIVERGENCES.md.
				continue
			}
			tagName, _, _ := strings.Cut(tag, ",")
			if tagName != fieldName {
				t.Errorf("extension.%s.%s json tag %q, want %q",
					decl.Name, goName, tagName, fieldName)
			}
		}
	}
}

var allowedExtraJSONFieldDivergences = map[string]map[string]string{}

// TestJSONTags_NoExtraGoFieldsBeyondUpstream asserts that every JSON-tagged
// field on a Go event struct has a corresponding upstream TS field.
// Catches drift in the OTHER direction: a worker adds an internal field
// without `json:"-"` and accidentally extends the wire format.
//
// Embedded fields (e.g. ToolCallEventBase inside BashToolCallEvent) are
// skipped: only top-level direct fields are walked, mirroring how the
// upstream parser scans interface bodies (it doesn't follow `extends`).
func TestJSONTags_NoExtraGoFieldsBeyondUpstream(t *testing.T) {
	s, err := LoadUpstream()
	if err != nil {
		t.Fatalf("upstream mirror missing: %v", err)
	}
	upstreamFieldsByType := map[string]map[string]bool{}
	for _, decl := range append(append([]EventTypeDecl{}, s.EventTypes...), s.EventResults...) {
		set := map[string]bool{}
		for _, f := range decl.FieldOrder {
			set[f] = true
		}
		upstreamFieldsByType[decl.Name] = set
	}

	for typeName, goType := range eventTypeRegistry {
		if goType.Kind() != reflect.Struct {
			continue
		}
		upstreamFields, ok := upstreamFieldsByType[typeName]
		if !ok {
			continue // pig-only type (currently none in registry)
		}
		for f := range goType.Fields() {
			if f.Anonymous {
				continue // embedded base; upstream parser ignores `extends` chain
			}
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue // explicitly excluded from wire format
			}
			tagName, _, _ := strings.Cut(tag, ",")
			if tagName == "" {
				// Field has no json tag at all → would marshal as Go name.
				// Treat as drift unless the upstream surface also uses
				// the Go-native casing (rare; flag for review).
				t.Errorf("extension.%s.%s has no json tag (would marshal as %q)",
					typeName, f.Name, f.Name)
				continue
			}
			if !upstreamFields[tagName] {
				if allowedExtraJSONFieldDivergences[typeName][tagName] != "" {
					continue
				}
				gap(t, "extra-field:"+typeName+"."+tagName, "extension.%s.%s json tag %q has no upstream counterpart",
					typeName, f.Name, tagName)
			}
		}
	}
}
