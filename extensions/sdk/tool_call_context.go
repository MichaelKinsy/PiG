package sdk

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// ToolCallIndex is the position of the running tool call among the tool calls of the assistant message that issued it, the batch agent-loop.ts executes (the tool_call content blocks of that message, counted from 0). ok is false outside a tool handler and for a call no assistant message issued, such as a nested [Context.ExecuteTool] call.
//
// The host sends it with the call, so a batch is ordered without reading the session's leaf entry.
func (c Context) ToolCallIndex() (index int, ok bool) {
	if c.toolIndex == nil {
		return 0, false
	}
	return *c.toolIndex, true
}

// ToolInput is the running tool call's arguments as the JSON text JSON.stringify gives for the object Pi hands execute: members in the order the model wrote them (array-index keys first, ascending, as JavaScript orders them), numbers as JavaScript prints them. The arguments are the ones the host sent the handler, after the tool's prepareArguments. Nil outside a tool handler. A [ToolFunc]'s params map has no order; this is the text to read it from.
func (c Context) ToolInput() json.RawMessage {
	if c.toolInput == nil || len(*c.toolInput) == 0 {
		return nil
	}
	value, err := parseOrderedJSON(*c.toolInput)
	if err != nil {
		return nil
	}
	return json.RawMessage(jsonStringify(value))
}

// ToolParams is the running tool call's arguments as a JavaScript object keeps them: every object, at any depth, is an [*OrderedObject] whose members are in the order Object.keys gives (array-index keys ascending, then the others in insertion order), arrays are []any, numbers float64. Pi hands execute that object, and an extension that writes the arguments back out (JSON.stringify(params.metadata)) relies on the order. Nil outside a tool handler or when the arguments are not an object.
func (c Context) ToolParams() *OrderedObject {
	if c.toolInput == nil || len(*c.toolInput) == 0 {
		return nil
	}
	value, err := parseOrderedJSON(*c.toolInput)
	if err != nil {
		return nil
	}
	object, ok := orderedFromParsed(value).(*OrderedObject)
	if !ok {
		return nil
	}
	return object
}

// orderedFromParsed converts a parseOrderedJSON value into the public ordered form.
func orderedFromParsed(value any) any {
	switch typed := value.(type) {
	case *jsObject:
		out := &OrderedObject{values: make(map[string]any, len(typed.keys))}
		for _, key := range typed.orderedKeys() {
			out.set(key, orderedFromParsed(typed.values[key]))
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, element := range typed {
			out[i] = orderedFromParsed(element)
		}
		return out
	}
	return value
}
