package subprocess

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// unwrapMessageEndReply returns the handler's result from the reply of an extension process's message_end handler, and applies the in-place edit of the message the reply carries to event.
// upstream: runner.ts emitMessageEnd hands every handler the one event.message and agent-session.ts stores that object, so a handler's in-place edit of it is the message the later handlers, turn_end, agent_end and the saved entry hold. An extension process cannot share the object: every SDK's reply carries the message it left, when that differs from the one it received, even if the handler failed.
func unwrapMessageEndReply(event any, raw json.RawMessage) (json.RawMessage, error) {
	var reply MessageEndResponsePayload
	//portlint:allow hotpathjson one decode per message_end of an extension (a message, not a delta), and only the reply's two members are read
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("decode message_end response: %w", err)
	}
	if len(reply.Message) > 0 {
		if err := applyMessageEndEdit(event, reply.Message); err != nil {
			return nil, fmt.Errorf("decode message_end message: %w", err)
		}
	}
	return reply.Result, nil
}

// unwrapToolResultReply returns the handler's result from the reply of an extension process's tool_result handler, and hands the members the handler edited to the sink event carries.
// upstream: runner.ts emitToolResult hands every handler the one currentEvent and agent-session.ts _afterToolCall reads the caller's result back, so a handler's in-place edit of content, details or isError is what the next handler sees and, for an edit to the object the caller handed over, what the caller keeps, also when the handler throws. An extension process cannot share the event: its reply names the members it left changed. The Runner holds the chain, so the edits go to the sink the event carries.
func unwrapToolResultReply(event any, raw json.RawMessage) (json.RawMessage, error) {
	var reply ToolResultResponsePayload
	//portlint:allow hotpathjson one decode per tool_result handler of an extension (a tool result, not a delta), and only the reply's two members are read
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
