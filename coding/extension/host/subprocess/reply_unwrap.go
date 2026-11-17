package subprocess

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The result members of the reply wrappers (see [EventResponsePayload] and [ToolResultResponsePayload]); each equals the member's json tag.
const (
	eventResultMember      = "_pigEventResult"
	toolResultResultMember = "_pigToolResultResult"
)

// unwrapEventReply returns the handler's result from the reply of an extension process's message_start, message_update, turn_start, agent_start, message_end or agent_end handler, and applies the in-place edits of the messages the reply carries to event.
// upstream: runner.ts emit hands every handler the one event, and so the one message of a message_start or message_end and the one list of an agent_end, and agent-session.ts stores those objects, so a handler's in-place edit of a message is what the later handlers and the saved entry hold. An extension process cannot share the objects: every SDK's reply carries the messages it left, when they differ from the ones it received, even if the handler failed.
func unwrapEventReply(ctx context.Context, name string, event any, raw json.RawMessage) (json.RawMessage, error) {
	if result, ok := soleMember(raw, eventResultMember); ok {
		return result, nil
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var reply EventResponsePayload
	//portlint:allow hotpathjson one decode per event of an extension that edited a message or replied in another shape (a message, not a delta), and only the reply's two members are read
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", name, err)
	}
	if err := applyEventEdits(ctx, event, reply.Edits); err != nil {
		return nil, fmt.Errorf("apply %s response: %w", name, err)
	}
	return reply.Result, nil
}

// unwrapToolResultReply returns the handler's result from the reply of an extension process's tool_result handler, and hands the members the handler edited to the sink event carries.
// upstream: runner.ts emitToolResult hands every handler the one currentEvent and agent-session.ts _afterToolCall reads the caller's result back, so a handler's in-place edit of content, details or isError is what the next handler sees and, for an edit to the object the caller handed over, what the caller keeps, also when the handler throws. An extension process cannot share the event: its reply names the members it left changed. The Runner holds the chain, so the edits go to the sink the event carries.
func unwrapToolResultReply(event any, raw json.RawMessage) (json.RawMessage, error) {
	if result, ok := soleMember(raw, toolResultResultMember); ok {
		return result, nil
	}
	var reply ToolResultResponsePayload
	//portlint:allow hotpathjson one decode per tool_result handler of an extension that edited the event or replied in another shape (a tool result, not a delta), and only the reply's two members are read
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("decode tool_result response: %w", err)
	}
	if reply.Edits != nil && len(reply.Edits.Changed) > 0 {
		if sink := extension.ToolResultEditsSink(event); sink != nil {
			sink.Members, sink.Changed, sink.Shared = reply.Edits.Members, reply.Edits.Changed, reply.Edits.Shared
		}
	}
	return reply.Result, nil
}

// soleMember reports the value of key in raw when raw is a JSON object whose only member is key, or an empty object (the value is then nil): the reply of a handler that edited nothing.
// The value aliases raw. It decodes nothing and allocates nothing, so a reply that edits nothing does not pay for the strict decode of the wrapper; any other reply (an edit, another member, an escaped or repeated key) reports false and takes that decode.
// The value is accepted only when it is one JSON value, so a reply with another member after it (`{"key":1,"other":2}`) is not read as the result `1,"other":2`.
func soleMember(raw []byte, key string) (json.RawMessage, bool) {
	start := skipJSONSpace(raw, 0)
	if start >= len(raw) || raw[start] != '{' {
		return nil, false
	}
	end := len(raw)
	for end > start && isJSONSpace(raw[end-1]) {
		end--
	}
	if end-start < 2 || raw[end-1] != '}' {
		return nil, false
	}
	object := raw[:end-1] // up to the closing brace
	i := skipJSONSpace(object, start+1)
	if i == len(object) {
		return nil, true
	}
	if len(object)-i < len(key)+3 || object[i] != '"' || string(object[i+1:i+1+len(key)]) != key || object[i+1+len(key)] != '"' {
		return nil, false
	}
	i = skipJSONSpace(object, i+len(key)+2)
	if i >= len(object) || object[i] != ':' {
		return nil, false
	}
	value := object[skipJSONSpace(object, i+1):]
	for len(value) > 0 && isJSONSpace(value[len(value)-1]) {
		value = value[:len(value)-1]
	}
	if len(value) == 0 || !json.Valid(value) {
		return nil, false
	}
	return value, true
}

func skipJSONSpace(data []byte, from int) int {
	for from < len(data) && isJSONSpace(data[from]) {
		from++
	}
	return from
}

func isJSONSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }
