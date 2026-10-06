package extension

import (
	"reflect"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// InputJSON is the input as JSON in the order a JavaScript object holds its members: the order the model wrote them in, with the edits applied to the order the way an assignment applies them. The wire bytes are used as they are while they still describe Input, so a number keeps its exact text. Once an in-process handler has edited the map, a member the wire bytes list keeps its place, a member they do not list follows in sorted order, and a removed member is gone. A map has no insertion order, so the order of two members an in-process handler added is sorted.
func (e CustomToolCallEvent) InputJSON() ([]byte, error) {
	if e.WireInput != nil && len(*e.WireInput) > 0 {
		wire := *e.WireInput
		var decoded map[string]any
		if err := json.Unmarshal(wire, &decoded); err == nil && decoded != nil {
			if reflect.DeepEqual(decoded, e.Input) {
				return wire, nil
			}
			return orderedjson.MarshalInSourceOrder(e.Input, wire)
		}
	}
	return orderedjson.Marshal(e.Input)
}

// MarshalJSON writes the event as agent-session.ts:_beforeToolCall builds it: type, toolName, toolCallId, parentToolCallId, input. Go's struct order would put the embedded base first, and a map sorts the members of `input`. A subprocess extension receives this text, and JSON.stringify of Pi's event keeps the order above.
func (e CustomToolCallEvent) MarshalJSON() ([]byte, error) {
	var fields []orderedField
	add := func(name string, value any) error {
		encoded, err := orderedjson.Marshal(value)
		if err != nil {
			return err
		}
		fields = append(fields, orderedField{name, encoded})
		return nil
	}
	if err := add("type", e.Type); err != nil {
		return nil, err
	}
	if err := add("toolName", e.ToolName); err != nil {
		return nil, err
	}
	if err := add("toolCallId", e.ToolCallID); err != nil {
		return nil, err
	}
	if e.ParentToolCallID != "" {
		if err := add("parentToolCallId", e.ParentToolCallID); err != nil {
			return nil, err
		}
	}
	input, err := e.InputJSON()
	if err != nil {
		return nil, err
	}
	fields = append(fields, orderedField{"input", input})
	return writeOrderedFields(fields)
}
