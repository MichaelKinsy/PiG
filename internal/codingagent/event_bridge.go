// event_bridge.go dispatches typed extension events to the in-process Runner.
//
// Upstream reference: agent-session.ts::_emitExtensionEvent (lines 640–711)
// dispatches all agent-loop events to the extension runner. The helpers
// below mirror that function 1:1, and DispatchAgentLoopEvent composes them
// into the single per-event switch the session drives (see below).

package codingagent

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// emitSessionStart dispatches a session_start event.
// reason is one of "startup" | "reload" | "new" | "resume" | "fork".
func emitSessionStart(runner *inproc.Runner, reason string) {
	if runner != nil && runner.HasHandlers(EventSessionStart) {
		_, _ = runner.Emit(context.Background(), extension.SessionStartEvent{
			Type:   EventSessionStart,
			Reason: reason,
		})
	}
}

// emitSessionInfoChanged dispatches a session_info_changed event.
func emitSessionInfoChanged(runner *inproc.Runner, name string) {
	if runner != nil && runner.HasHandlers(EventSessionInfoChanged) {
		_, _ = runner.Emit(context.Background(), extension.SessionInfoChangedEvent{
			Type: EventSessionInfoChanged,
			Name: name,
		})
	}
}

// emitSessionShutdown dispatches a session_shutdown event.
// reason is one of "quit" | "reload" | "new" | "resume" | "fork".
// The pig-specific "slash-quit" reason is collapsed to "quit".
func emitSessionShutdown(runner *inproc.Runner, reason string) {
	if runner != nil && runner.HasHandlers(EventSessionShutdown) {
		r := reason
		if r == "slash-quit" {
			r = "quit"
		}
		_, _ = runner.Emit(context.Background(), extension.SessionShutdownEvent{
			Type:   EventSessionShutdown,
			Reason: r,
		})
	}
}

// emitBeforeAgentStart dispatches a before_agent_start event and returns the
// combined result (mutated system prompt and/or injected custom messages).
//
// systemPromptOptions mirrors the upstream
// `BuildSystemPromptOptions` (system-prompt.ts:8-25) and is forwarded
// verbatim to extension handlers via `event.systemPromptOptions` so
// they can inspect what pi loaded without re-discovering resources.
func emitBeforeAgentStart(
	runner *inproc.Runner,
	prompt string,
	systemPromptOptions extension.BuildSystemPromptOptions,
) *extension.BeforeAgentStartCombinedResult {
	return emitBeforeAgentStartWithImages(context.Background(), runner, prompt, nil, systemPromptOptions)
}

func emitBeforeAgentStartWithImages(ctx context.Context, runner *inproc.Runner, prompt string, images []ai.ImageContent, systemPromptOptions extension.BuildSystemPromptOptions) *extension.BeforeAgentStartCombinedResult {
	if runner == nil || !runner.HasHandlers(EventBeforeAgentStart) {
		return nil
	}
	result, err := runner.EmitBeforeAgentStart(
		ctx,
		prompt,
		images,
		systemPromptOptions,
	)
	if err != nil {
		return nil
	}
	return result
}

// emitAgentStart dispatches an agent_start event.
// upstream: agent-session.ts:616
func emitAgentStart(runner *inproc.Runner) {
	if runner != nil && runner.HasHandlers(EventAgentStart) {
		_, _ = runner.Emit(context.Background(), extension.AgentStartEvent{
			Type: EventAgentStart,
		})
	}
}

// emitAgentEnd dispatches an agent_end event.
// upstream: agent-session.ts:618: includes messages.
func emitAgentEnd(runner *inproc.Runner, messages []agent.AgentMessage, willRetry bool) {
	if runner != nil && runner.HasHandlers(EventAgentEnd) {
		_, _ = runner.Emit(context.Background(), extension.AgentEndEvent{
			Type:     EventAgentEnd,
			Messages: messages,
		})
	}
}

// emitAgentSettled dispatches an agent_settled event, fired after an agent run
// has fully settled (no automatic retry, compaction, or queued continuation
// will run).
// upstream: agent-session.ts:579 (_emitAgentSettled), called from the
// _runAgentPrompt finally at agent-session.ts:1069.
func emitAgentSettled(runner *inproc.Runner, aborted bool) {
	if runner != nil && runner.HasHandlers(EventAgentSettled) {
		_, _ = runner.Emit(context.Background(), extension.AgentSettledEvent{
			Type:    EventAgentSettled,
			Aborted: aborted,
		})
	}
}

// emitTurnStart dispatches a turn_start event.
// upstream: agent-session.ts:622-627
func emitTurnStart(runner *inproc.Runner, turnIndex int) {
	if runner != nil && runner.HasHandlers(EventTurnStart) {
		_, _ = runner.Emit(context.Background(), extension.TurnStartEvent{
			Type:      EventTurnStart,
			TurnIndex: turnIndex,
			Timestamp: time.Now().UnixMilli(),
		})
	}
}

// emitTurnEnd dispatches a turn_end event.
// upstream: agent-session.ts:629-635: includes the persisted message entry IDs.
func emitTurnEnd(runner *inproc.Runner, event agent.TurnEndEvent) {
	if runner != nil && runner.HasHandlers(EventTurnEnd) {
		// Convert []agent.ToolResultMessage → []any for extension type alias.
		trs := make([]extension.ToolResultMessage, len(event.ToolResults))
		copy(trs, event.ToolResults)
		_, _ = runner.Emit(context.Background(), extension.TurnEndEvent{
			Type:               EventTurnEnd,
			TurnIndex:          event.TurnIndex,
			Message:            event.Message,
			ToolResults:        trs,
			MessageEntryID:     event.MessageEntryID,
			ToolResultEntryIds: slices.Clone(event.ToolResultEntryIDs),
		})
	}
}

// emitMessageStart dispatches a message_start event.
// upstream: agent-session.ts:637-640: includes message.
func emitMessageStart(runner *inproc.Runner, message extension.AgentMessage) {
	emitMessageStartContext(context.Background(), runner, message)
}

func emitMessageStartContext(ctx context.Context, runner *inproc.Runner, message extension.AgentMessage) {
	if runner != nil && runner.HasHandlers(EventMessageStart) {
		_, _ = runner.Emit(ctx, extension.MessageStartEvent{
			Type:    EventMessageStart,
			Message: message,
		})
	}
}

// emitMessageEnd dispatches a message_end event.
// upstream: agent-session.ts:649-652: includes message.
func emitMessageEnd(runner *inproc.Runner, message extension.AgentMessage) {
	emitMessageEndContext(context.Background(), runner, message)
}

func emitMessageEndContext(ctx context.Context, runner *inproc.Runner, message extension.AgentMessage) {
	if runner != nil && runner.HasHandlers(EventMessageEnd) {
		_, _ = runner.Emit(ctx, extension.MessageEndEvent{
			Type:    EventMessageEnd,
			Message: message,
		})
	}
}

// emitMessageUpdate dispatches a message_update event.
// upstream: agent-session.ts:641-647: high-frequency per-delta event.
// Includes the current message state and the raw assistantMessageEvent.
func emitMessageUpdate(runner *inproc.Runner, message extension.AgentMessage, assistantMessageEvent any) {
	emitMessageUpdateContext(context.Background(), runner, message, assistantMessageEvent)
}

func emitMessageUpdateContext(ctx context.Context, runner *inproc.Runner, message extension.AgentMessage, assistantMessageEvent any) {
	if runner != nil && runner.HasHandlers(EventMessageUpdate) {
		_, _ = runner.Emit(ctx, extension.MessageUpdateEvent{
			Type:                  EventMessageUpdate,
			Message:               message,
			AssistantMessageEvent: assistantMessageEvent,
		})
	}
}

// toolExecutionWireArgs is the model's argument JSON as written. The event carries it for the extension wire so a Node handler sees the keys in the order the model wrote them, as the parsed `toolCall.arguments` of agent-loop.ts:541-547 keeps its insertion order; the decoded map toolExecutionArgs returns sorts them.
func toolExecutionWireArgs(args json.RawMessage) json.RawMessage {
	if len(args) == 0 || !json.Valid(args) {
		return nil
	}
	return args
}

func toolExecutionArgs(args json.RawMessage) any {
	if len(args) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(args, &value); err != nil {
		return args
	}
	return value
}

// toolResultWire is the AgentToolResult as an extension process receives it, with its members in the order the object holds them (AgentToolResult.MemberNames) and each content block in the order Pi's tools write it. JSON.stringify of Pi's object keeps that order; the map extensionToolResult returns for in-process handlers sorts it.
//
// upstream: agent-loop.ts:778-786, 912-919 (the events carry the tool's own object), packages/agent/src/types.ts AgentToolResult
type toolResultWire struct {
	result  agent.AgentToolResult
	content []any
}

// MarshalJSON writes the members with the extension codec, which keeps an unmatched UTF-16 unit as JSON.stringify writes it.
func (w toolResultWire) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	first := true
	for _, name := range w.result.MemberNames() {
		var value any
		switch name {
		case "content":
			value = w.content
		case "details":
			value = w.result.Details
		case "structuredContent":
			if !json.Valid(w.result.StructuredContent) {
				continue
			}
			value = w.result.StructuredContent
		case "isError":
			value = w.result.IsError
		case "usage":
			value = w.result.Usage
		case "terminate":
			value = w.result.Terminate
		}
		key, err := sdkjson.Marshal(name)
		if err != nil {
			return nil, err
		}
		encoded, err := sdkjson.Marshal(value)
		if err != nil {
			return nil, err
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		out.Write(key)
		out.WriteByte(':')
		out.Write(encoded)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// toolResultTextBlock and toolResultImageBlock are the blocks of toolResultWire. upstream: packages/ai/src/types.ts TextContent, ImageContent
type toolResultTextBlock struct {
	Type          string `json:"type"`
	Text          string `json:"text"`
	TextSignature string `json:"textSignature,omitempty"`
}

type toolResultImageBlock struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

func extensionToolResultWire(result agent.AgentToolResult) toolResultWire {
	content := make([]any, 0, len(result.Content))
	for _, block := range result.Content {
		switch value := block.(type) {
		case ai.TextContent:
			content = append(content, toolResultTextBlock{Type: "text", Text: value.Text, TextSignature: value.TextSignature})
		case ai.ImageContent:
			content = append(content, toolResultImageBlock{Type: "image", Data: value.Data, MimeType: value.MimeType})
		}
	}
	return toolResultWire{result: result, content: content}
}

// extensionToolResult is the finalized AgentToolResult a tool_execution_end handler sees: the keys the tool or a hook set (agent/src/types.ts:435-455).
func extensionToolResult(result agent.AgentToolResult) map[string]any {
	payload := map[string]any{"content": ToolResultEventContent(result)}
	if result.Details != nil {
		payload["details"] = result.Details
	}
	if len(result.StructuredContent) > 0 {
		var structured any
		if err := json.Unmarshal(result.StructuredContent, &structured); err == nil {
			payload["structuredContent"] = structured
		}
	}
	if result.IsError {
		payload["isError"] = true
	}
	if result.Usage != nil {
		payload["usage"] = result.Usage
	}
	if result.Terminate {
		payload["terminate"] = true
	}
	return payload
}

// emitToolExecutionStart dispatches a tool_execution_start event.
// upstream: agent-session.ts:654-661
func emitToolExecutionStart(runner *inproc.Runner, toolCallID, toolName string, args json.RawMessage, parentToolCallID string) {
	if runner != nil && runner.HasHandlers(EventToolExecutionStart) {
		_, _ = runner.Emit(context.Background(), extension.ToolExecutionStartEvent{
			Type:       EventToolExecutionStart,
			ToolCallID: toolCallID,
			ToolName:   toolName,
			Args:       toolExecutionArgs(args),
			WireArgs:   toolExecutionWireArgs(args),

			ParentToolCallID: parentToolCallID,
		})
	}
}

// emitToolExecutionUpdate dispatches a tool_execution_update event.
// upstream: agent-session.ts:663-670
// Args is decoded to the same structured value as tool_execution_start so
// in-process and subprocess handlers observe one payload shape. The partial result is the AgentToolResult the tool passed to onUpdate, sent as the same object shape tool_execution_end carries (agent-loop.ts:778-786).
func emitToolExecutionUpdate(runner *inproc.Runner, toolCallID, toolName string, partial agent.AgentToolResult, args json.RawMessage, parentToolCallID string) {
	if runner != nil && runner.HasHandlers(EventToolExecutionUpdate) {
		_, _ = runner.Emit(context.Background(), extension.ToolExecutionUpdateEvent{
			Type:              EventToolExecutionUpdate,
			ToolCallID:        toolCallID,
			ToolName:          toolName,
			PartialResult:     extensionToolResult(partial),
			WirePartialResult: extensionToolResultWire(partial),
			Args:              toolExecutionArgs(args),
			WireArgs:          toolExecutionWireArgs(args),

			ParentToolCallID: parentToolCallID,
		})
	}
}

// emitToolExecutionEnd dispatches a tool_execution_end event.
// upstream: agent-session.ts:671-678
func emitToolExecutionEnd(runner *inproc.Runner, event agent.ToolExecutionEndEvent) {
	if runner != nil && runner.HasHandlers(EventToolExecutionEnd) {
		_, _ = runner.Emit(context.Background(), extension.ToolExecutionEndEvent{
			Type:       EventToolExecutionEnd,
			ToolCallID: event.ToolCallID,
			ToolName:   event.ToolName,
			Result:     extensionToolResult(event.Result),
			WireResult: extensionToolResultWire(event.Result),
			IsError:    event.IsError,
			DurationMs: event.DurationMs,

			ParentToolCallID: event.ParentToolCallID,
		})
	}
}

// AgentEventContext returns the context an extension handler for ev runs under. It carries the event's stream scope, so a handler that waits for its extension process releases the stream's continuation queue instead of freezing the provider, as Pi's awaited handler does (agent-session.ts:894-919; runner.ts:emit).
func AgentEventContext(ev agent.AgentEvent) context.Context {
	return agent.EventObservation(ev).Context(context.Background())
}

// DispatchAgentLoopEvent maps one agent-loop event to the extension runner. Session awaits extension dispatch before notifying public listeners, as agent-session.ts::_handleAgentEvent does. Each message_update carries its own shallow message and full provider event; currentMessage retains the message_start value for callers that track it.
func DispatchAgentLoopEvent(runner *inproc.Runner, ev agent.AgentEvent, currentMessage *extension.AgentMessage) {
	if runner == nil {
		return
	}
	switch e := ev.(type) {
	case agent.AgentStartEvent:
		emitAgentStart(runner)
	case agent.AgentEndEvent:
		emitAgentEnd(runner, e.Messages, e.WillRetry)
	case agent.AgentSettledEvent:
		emitAgentSettled(runner, e.Aborted)
	case agent.TurnStartEvent:
		emitTurnStart(runner, e.TurnIndex)
	case agent.TurnEndEvent:
		emitTurnEnd(runner, e)
	case agent.MessageStartEvent:
		if currentMessage != nil {
			*currentMessage = e.Message
		}
		emitMessageStartContext(AgentEventContext(ev), runner, e.Message)
	case agent.MessageUpdateEvent:
		emitMessageUpdateContext(AgentEventContext(ev), runner, e.Message, e.AssistantMessageEvent)
	case agent.MessageEndEvent:
		emitMessageEndContext(AgentEventContext(ev), runner, e.Message)
	case agent.ToolExecutionStartEvent:
		emitToolExecutionStart(runner, e.ToolCallID, e.ToolName, e.Args, e.ParentToolCallID)
	case agent.ToolExecutionUpdateEvent:
		emitToolExecutionUpdate(runner, e.ToolCallID, e.ToolName, e.PartialResult, e.Args, e.ParentToolCallID)
	case agent.ToolExecutionEndEvent:
		emitToolExecutionEnd(runner, e)
	}
}

// AgentLoopEventType returns the extension event type DispatchAgentLoopEvent
// dispatches for ev, or "" for an event it does not dispatch.
func AgentLoopEventType(ev agent.AgentEvent) string {
	switch ev.(type) {
	case agent.AgentStartEvent:
		return EventAgentStart
	case agent.AgentEndEvent:
		return EventAgentEnd
	case agent.AgentSettledEvent:
		return EventAgentSettled
	case agent.TurnStartEvent:
		return EventTurnStart
	case agent.TurnEndEvent:
		return EventTurnEnd
	case agent.MessageStartEvent:
		return EventMessageStart
	case agent.MessageUpdateEvent:
		return EventMessageUpdate
	case agent.MessageEndEvent:
		return EventMessageEnd
	case agent.ToolExecutionStartEvent:
		return EventToolExecutionStart
	case agent.ToolExecutionUpdateEvent:
		return EventToolExecutionUpdate
	case agent.ToolExecutionEndEvent:
		return EventToolExecutionEnd
	}
	return ""
}

// emitUserBash dispatches a user_bash event. A non-nil error means a handler
// failed or returned an invalid result; the runner already reported it, and
// the caller must not run the command (upstream #9068 fails closed).
func emitUserBash(ctx context.Context, runner *inproc.Runner, command, cwd string, excludeFromContext bool) (*extension.UserBashEventResult, error) {
	if runner == nil || !runner.HasHandlers(EventUserBash) {
		return nil, nil
	}
	return runner.EmitUserBash(ctx, extension.UserBashEvent{
		Type:               EventUserBash,
		Command:            command,
		Cwd:                cwd,
		ExcludeFromContext: excludeFromContext,
	})
}

// The helpers below dispatch extension events from interactive runtime paths.

// RunInputHandlers runs the extension input handlers for user input and
// returns the text and images to use, or handled when an extension consumed
// it. A transform without images keeps the original images, and a handler
// failure is returned for the caller to report. Mirrors upstream
// agent-session.ts _runInputHandlers; coding.Session.RunInputHandlers is the
// Session entry point every mode uses.
func RunInputHandlers(ctx context.Context, runner *inproc.Runner, text string, images []ai.ImageContent, source extension.InputSource, streamingBehavior string) (string, []ai.ImageContent, bool, error) {
	if runner == nil || !runner.HasHandlers(EventInput) {
		return text, images, false, nil
	}
	result, err := runner.EmitInput(ctx, text, images, source, streamingBehavior)
	if err != nil {
		return text, images, false, err
	}
	switch result := result.(type) {
	case extension.InputEventResultHandled:
		return "", nil, true, nil
	case extension.InputEventResultTransform:
		if result.Images != nil {
			images = result.Images
		}
		return result.Text, images, false, nil
	default:
		return text, images, false, nil
	}
}
