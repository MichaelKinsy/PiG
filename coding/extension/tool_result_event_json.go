package extension

import (
	"reflect"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// contentBlockMemberOrder is the order Pi's tools and extensions write a TextContent or ImageContent in (packages/ai/src/types.ts).
var contentBlockMemberOrder = []string{"type", "text", "textSignature", "data", "mimeType"}

// marshalToolResultEvent writes a tool_result event as agent-session.ts:649-660 builds it: type, toolName, toolCallId, parentToolCallId, input, content, details, structuredContent, isError, usage. Go's struct order would put the embedded base first, and a map sorts the members of `input` and of each content block. A subprocess extension receives this text, and JSON.stringify of Pi's event keeps the order above.
func marshalToolResultEvent(base ToolResultEventBase, toolName string, details any) ([]byte, error) {
	var fields []orderedField
	add := func(name string, value any) error {
		encoded, err := orderedjson.Marshal(value)
		if err != nil {
			return err
		}
		fields = append(fields, orderedField{name, encoded})
		return nil
	}
	if err := add("type", base.Type); err != nil {
		return nil, err
	}
	if err := add("toolName", toolName); err != nil {
		return nil, err
	}
	if err := add("toolCallId", base.ToolCallID); err != nil {
		return nil, err
	}
	if base.ParentToolCallID != "" {
		if err := add("parentToolCallId", base.ParentToolCallID); err != nil {
			return nil, err
		}
	}
	if len(base.WireInput) > 0 && json.Valid(base.WireInput) {
		fields = append(fields, orderedField{"input", base.WireInput})
	} else if err := add("input", base.Input); err != nil {
		return nil, err
	}
	content, err := orderedjson.MarshalArray(base.Content, contentBlockMemberOrder...)
	if err != nil {
		return nil, err
	}
	fields = append(fields, orderedField{"content", content})
	if !isNilValue(details) {
		if err := add("details", details); err != nil {
			return nil, err
		}
	}
	if len(base.StructuredContent) > 0 {
		fields = append(fields, orderedField{"structuredContent", base.StructuredContent})
	}
	if err := add("isError", base.IsError); err != nil {
		return nil, err
	}
	if !isNilValue(base.Usage) {
		if err := add("usage", base.Usage); err != nil {
			return nil, err
		}
	}
	return writeOrderedFields(fields)
}

// writeOrderedFields writes an object whose members keep the order of fields.
func writeOrderedFields(fields []orderedField) ([]byte, error) {
	out := []byte{'{'}
	for i, field := range fields {
		if i > 0 {
			out = append(out, ',')
		}
		name, err := orderedjson.Marshal(field.name)
		if err != nil {
			return nil, err
		}
		out = append(out, name...)
		out = append(out, ':')
		out = append(out, field.value...)
	}
	return append(out, '}'), nil
}

type orderedField struct {
	name  string
	value []byte
}

// isNilValue reports an absent value: nil, or a nil pointer, map or slice behind an interface.
func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
		return reflected.IsNil()
	}
	return false
}

// MarshalJSON writes the event in Pi's member order; see [marshalToolResultEvent].
func (e BashToolResultEvent) MarshalJSON() ([]byte, error) {
	return marshalToolResultEvent(e.ToolResultEventBase, e.ToolName, e.Details)
}

// MarshalJSON writes the event in Pi's member order; see [marshalToolResultEvent].
func (e PowerShellToolResultEvent) MarshalJSON() ([]byte, error) {
	return marshalToolResultEvent(e.ToolResultEventBase, e.ToolName, e.Details)
}

// MarshalJSON writes the event in Pi's member order; see [marshalToolResultEvent].
func (e ReadToolResultEvent) MarshalJSON() ([]byte, error) {
	return marshalToolResultEvent(e.ToolResultEventBase, e.ToolName, e.Details)
}

// MarshalJSON writes the event in Pi's member order; see [marshalToolResultEvent].
func (e EditToolResultEvent) MarshalJSON() ([]byte, error) {
	return marshalToolResultEvent(e.ToolResultEventBase, e.ToolName, e.Details)
}

// MarshalJSON writes the event in Pi's member order; see [marshalToolResultEvent].
func (e WriteToolResultEvent) MarshalJSON() ([]byte, error) {
	return marshalToolResultEvent(e.ToolResultEventBase, e.ToolName, e.Details)
}

// MarshalJSON writes the event in Pi's member order; see [marshalToolResultEvent].
func (e GrepToolResultEvent) MarshalJSON() ([]byte, error) {
	return marshalToolResultEvent(e.ToolResultEventBase, e.ToolName, e.Details)
}

// MarshalJSON writes the event in Pi's member order; see [marshalToolResultEvent].
func (e FindToolResultEvent) MarshalJSON() ([]byte, error) {
	return marshalToolResultEvent(e.ToolResultEventBase, e.ToolName, e.Details)
}

// MarshalJSON writes the event in Pi's member order; see [marshalToolResultEvent].
func (e LsToolResultEvent) MarshalJSON() ([]byte, error) {
	return marshalToolResultEvent(e.ToolResultEventBase, e.ToolName, e.Details)
}

// MarshalJSON writes the event in Pi's member order; see [marshalToolResultEvent].
func (e CustomToolResultEvent) MarshalJSON() ([]byte, error) {
	return marshalToolResultEvent(e.ToolResultEventBase, e.ToolName, e.Details)
}
