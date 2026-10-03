package ai

import (
	"encoding/json"
)

// upstream: .upstream/v0.99.1/packages/ai/src/utils/validation.ts:317-339 (validateToolArguments returns structuredClone of
// toolCall.arguments) and utils/json-parse.ts (parseStreamingJson). A JavaScript object enumerates its members in insertion
// order, integer-like keys first and ascending, so JSON.stringify of a tool call's arguments writes the order the model sent.
// A Go map has no order. ToolCall keeps the model's order in an unexported sidecar next to the map, as ToolSchema keeps
// parameterOrder. The sidecar is recorded only for an object whose order differs from sorted order, so a call whose objects
// are all sorted carries none. A member the caller adds to the map afterwards, or removes, never invents or drops a member:
// the order lists the members of the model's text, and members it does not list follow them in sorted order.

// ArgumentsJSON encodes Arguments in the member order the model sent them. A nil Arguments is the empty object, as Pi's
// arguments are always an object.
func (content ToolCall) ArgumentsJSON() ([]byte, error) {
	if content.Arguments == nil {
		return []byte("{}"), nil
	}
	return marshalInRecordedOrder(map[string]any(content.Arguments), content.argumentOrder)
}

// SetStreamingArguments parses a streamed tool-argument text as ParseStreamingJson does and keeps its member order.
func (content *ToolCall) SetStreamingArguments(input string) {
	content.Arguments, content.argumentOrder = parseStreamingJsonArguments(input)
}

// SetArgumentsJSON decodes a complete JSON object into Arguments and keeps its member order. A text that is not a JSON object is an error and leaves the call unchanged.
func (content *ToolCall) SetArgumentsJSON(raw []byte) error {
	var decoded JsonObject
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	order, err := readSchemaObjectOrder(raw)
	if err != nil {
		return err
	}
	content.Arguments, content.argumentOrder = decoded, order
	return nil
}

// MarshalJSONInSourceOrder encodes value with the object member order of the JSON text source. Members source does not have follow in sorted order. A source that is not JSON gives sorted order.
func MarshalJSONInSourceOrder(value any, source []byte) ([]byte, error) {
	order, err := readSchemaObjectOrder(source)
	if err != nil {
		order = nil
	}
	return marshalInRecordedOrder(value, order)
}

// marshalInRecordedOrder encodes value with order; a nil order is plain sorted encoding.
func marshalInRecordedOrder(value any, order schemaObjectOrder) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil || order == nil {
		return encoded, err
	}
	// The plain encoding above rejects cycles and values with no JSON form before the ordered walk recurses.
	return marshalSchemaWithOrder(value, order, "")
}
