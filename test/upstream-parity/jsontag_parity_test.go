package parity

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
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
		// A MarshalJSON decides the wire members, so its field tags name nothing: the members it writes for the samples must name each upstream field the Go type carries.
		var written map[string]bool
		if writesItsOwnJSON(goType) {
			written = marshaledMembers(t, decl.Name, goType)
		}
		for _, fieldName := range decl.FieldOrder {
			goName := CamelToGoField(fieldName)
			f, found := goType.FieldByName(goName)
			if !found {
				continue // covered by Test*_AllUpstreamFieldsHaveGoCounterpart
			}
			tag := f.Tag.Get("json")
			if written != nil {
				if tag != "-" && !written[fieldName] {
					t.Errorf("extension.%s.%s is not written as %q by its MarshalJSON for any sample", decl.Name, goName, fieldName)
				}
				continue
			}
			if tag == "-" {
				// Explicitly excluded from wire format. Common for fields
				// that hold non-serialisable Go types (e.g. context.Context
				// from the D8 AbortSignal→context migration). The pig side
				// has consciously chosen not to wire-format the field; the
				// divergence is documented in docs/parity/DIVERGENCES.md.
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

// allowedExtraJSONFieldDivergences names wire members PiG writes that Pi's interface does not declare. Each value is the
// divergence that records it.
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

	usedAllowances := map[string]bool{}
	defer func() {
		for typeName, members := range allowedExtraJSONFieldDivergences {
			for member := range members {
				if !usedAllowances[typeName+"."+member] {
					t.Errorf("allowedExtraJSONFieldDivergences names %s.%s, which no sample writes; delete the stale allowance or add a sample that reaches it", typeName, member)
				}
			}
		}
	}()
	for typeName, goType := range eventTypeRegistry {
		if goType.Kind() != reflect.Struct {
			continue
		}
		upstreamFields, ok := upstreamFieldsByType[typeName]
		if !ok {
			continue // pig-only type (currently none in registry)
		}
		if writesItsOwnJSON(goType) {
			members := marshaledMembers(t, typeName, goType)
			// Upstream interfaces extend a base whose members the parser does not fold in; the Go type embeds that base, and the walk of plain structs skips embedded fields the same way.
			inherited := map[string]bool{}
			for f := range goType.Fields() {
				embedded := f.Type
				if embedded.Kind() == reflect.Pointer {
					embedded = embedded.Elem() // TurnEndEvent embeds *BoundaryState, Pi's `extends BoundaryState`
				}
				if !f.Anonymous || embedded.Kind() != reflect.Struct {
					continue
				}
				for base := range embedded.Fields() {
					name, _, _ := strings.Cut(base.Tag.Get("json"), ",")
					inherited[name] = true
				}
			}
			for member := range members {
				if allowedExtraJSONFieldDivergences[typeName][member] != "" {
					usedAllowances[typeName+"."+member] = true
				}
				if !upstreamFields[member] && !inherited[member] && allowedExtraJSONFieldDivergences[typeName][member] == "" {
					gap(t, "extra-field:"+typeName+"."+member, "extension.%s marshals member %q, which has no upstream counterpart", typeName, member)
				}
			}
			continue
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
					usedAllowances[typeName+"."+tagName] = true
					continue
				}
				gap(t, "extra-field:"+typeName+"."+tagName, "extension.%s.%s json tag %q has no upstream counterpart",
					typeName, f.Name, tagName)
			}
		}
	}
}

// writesItsOwnJSON reports whether the struct's wire form comes from its own MarshalJSON, so its field tags do not name
// wire members.
func writesItsOwnJSON(goType reflect.Type) bool {
	return goType.Implements(reflect.TypeFor[json.Marshaler]())
}

// variantSamples are the samples of an upstream union member that one Go struct carries for the whole union: its MarshalJSON writes
// the member's wire form only for values of that member, so the members it writes are read from these samples alone.
var variantSamples = map[string][]any{
	"BeforeAgentStartUserEvent": {
		extension.BeforeAgentStartEvent{MessageType: extension.BeforeAgentStartUser, Images: []extension.ImageContent{{}}},
	},
	"BeforeAgentStartCustomEvent": {
		extension.BeforeAgentStartEvent{MessageType: extension.BeforeAgentStartCustom, Message: &extension.CustomMessage{}},
	},
	// TurnEndEvent and AgentBeforeSettleEvent write Pi's member order with MarshalJSON; their AgentMessage is a union with
	// exactly one variant, which the generic zero and filled samples cannot build.
	"TurnEndEvent": {
		extension.TurnEndEvent{Type: "turn_end", Message: extension.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant"}}, ToolResults: []extension.ToolResultMessage{{Role: "toolResult"}}, ToolResultEntryIds: []string{"x"}},
		extension.TurnEndEvent{Type: "turn_end", Message: extension.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant"}}, BoundaryState: &extension.BoundaryState{Entries: []extension.SessionBoundaryDraft{{Type: extension.SessionBoundaryDraftCustom}}, Continue: true}},
	},
	"AgentBeforeSettleEvent": {
		extension.AgentBeforeSettleEvent{Type: "agent_before_settle", BoundaryState: extension.BoundaryState{Entries: []extension.SessionBoundaryDraft{{Type: extension.SessionBoundaryDraftCustom}}, Continue: true}},
	},
}

// marshaledMembers is the union of the members a type's MarshalJSON writes for its zero value and a value with every
// exported field set (so omitempty members appear), or for its variantSamples when it has them.
func marshaledMembers(t *testing.T, typeName string, goType reflect.Type) map[string]bool {
	t.Helper()
	values, ok := variantSamples[typeName]
	if !ok {
		values = []any{reflect.New(goType).Elem().Interface(), filledValue(goType, 0).Interface()}
	}
	members := map[string]bool{}
	for _, v := range values {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Errorf("extension.%s sample %+v does not marshal: %v", typeName, v, err)
			continue
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Errorf("extension.%s marshals to %s, not an object: %v", typeName, raw, err)
			continue
		}
		for member := range object {
			members[member] = true
		}
	}
	return members
}

// filledValue returns a value of t whose exported fields, recursively to a small depth, are not zero.
func filledValue(t reflect.Type, depth int) reflect.Value {
	v := reflect.New(t).Elem()
	if depth > 3 {
		return v
	}
	if t == reflect.TypeFor[json.RawMessage]() {
		v.SetBytes([]byte("1"))
		return v
	}
	switch t.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	case reflect.Pointer:
		p := reflect.New(t.Elem())
		p.Elem().Set(filledValue(t.Elem(), depth+1))
		v.Set(p)
	case reflect.Slice:
		s := reflect.MakeSlice(t, 1, 1)
		s.Index(0).Set(filledValue(t.Elem(), depth+1))
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(t)
		m.SetMapIndex(filledValue(t.Key(), depth+1), filledValue(t.Elem(), depth+1))
		v.Set(m)
	case reflect.Struct:
		for i := range t.NumField() {
			if f := v.Field(i); f.CanSet() {
				f.Set(filledValue(t.Field(i).Type, depth+1))
			}
		}
	case reflect.Interface:
		if t.NumMethod() == 0 {
			v.Set(reflect.ValueOf("x"))
		}
	}
	return v
}
