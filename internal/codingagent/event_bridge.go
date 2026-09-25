// event_bridge.go dispatches typed extension events to the in-process Runner.
//
// Upstream reference: agent-session.ts::_emitExtensionEvent (lines 640–711)
// dispatches all agent-loop events to the extension runner. The helpers
// below mirror that function 1:1, and DispatchAgentLoopEvent composes them
// into the single per-event switch the session drives (see below).

package codingagent

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
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
	prompt, systemPrompt string,
	systemPromptOptions extension.BuildSystemPromptOptions,
) *extension.BeforeAgentStartCombinedResult {
	return emitBeforeAgentStartWithImages(context.Background(), runner, prompt, nil, systemPrompt, systemPromptOptions)
}

func emitBeforeAgentStartWithImages(ctx context.Context, runner *inproc.Runner, prompt string, images []ai.ImageContent, systemPrompt string, systemPromptOptions extension.BuildSystemPromptOptions) *extension.BeforeAgentStartCombinedResult {
	if runner == nil || !runner.HasHandlers(EventBeforeAgentStart) {
		return nil
	}
	result, err := runner.EmitBeforeAgentStart(
		ctx,
		prompt,
		extensionImages(images),
		systemPrompt,
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
		// Convert []agent.AgentMessage → []any for extension type alias.
		msgs := make([]extension.AgentMessage, len(messages))
		for i, m := range messages {
			msgs[i] = m
		}
		_, _ = runner.Emit(context.Background(), extension.AgentEndEvent{
			Type:     EventAgentEnd,
			Messages: msgs,
		})
	}
}

// emitAgentSettled dispatches an agent_settled event, fired after an agent run
// has fully settled (no automatic retry, compaction, or queued continuation
// will run).
// upstream: agent-session.ts:579 (_emitAgentSettled), called from the
// _runAgentPrompt finally at agent-session.ts:1069.
func emitAgentSettled(runner *inproc.Runner) {
	if runner != nil && runner.HasHandlers(EventAgentSettled) {
		_, _ = runner.Emit(context.Background(), extension.AgentSettledEvent{
			Type: EventAgentSettled,
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
		for i, tr := range event.ToolResults {
			trs[i] = tr
		}
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
	if runner != nil && runner.HasHandlers(EventMessageStart) {
		_, _ = runner.Emit(context.Background(), extension.MessageStartEvent{
			Type:    EventMessageStart,
			Message: message,
		})
	}
}

// emitMessageEnd dispatches a message_end event.
// upstream: agent-session.ts:649-652: includes message.
func emitMessageEnd(runner *inproc.Runner, message extension.AgentMessage) {
	if runner != nil && runner.HasHandlers(EventMessageEnd) {
		_, _ = runner.Emit(context.Background(), extension.MessageEndEvent{
			Type:    EventMessageEnd,
			Message: message,
		})
	}
}

// emitMessageUpdate dispatches a message_update event.
// upstream: agent-session.ts:641-647: high-frequency per-delta event.
// Includes the current message state and the raw assistantMessageEvent.
func emitMessageUpdate(runner *inproc.Runner, message extension.AgentMessage, assistantMessageEvent any) {
	if runner != nil && runner.HasHandlers(EventMessageUpdate) {
		_, _ = runner.Emit(context.Background(), extension.MessageUpdateEvent{
			Type:                  EventMessageUpdate,
			Message:               message,
			AssistantMessageEvent: assistantMessageEvent,
		})
	}
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

func extensionToolResult(result agent.AgentToolResult) map[string]any {
	content := make([]any, 0, 1+len(result.Images))
	content = append(content, map[string]any{"type": "text", "text": result.Content})
	for _, image := range result.Images {
		content = append(content, map[string]any{"type": "image", "data": image.Data, "mimeType": image.MimeType})
	}
	payload := map[string]any{"content": content}
	if result.Details != nil {
		payload["details"] = result.Details
	}
	return payload
}

// emitToolExecutionStart dispatches a tool_execution_start event.
// upstream: agent-session.ts:654-661
func emitToolExecutionStart(runner *inproc.Runner, toolCallID, toolName string, args json.RawMessage) {
	if runner != nil && runner.HasHandlers(EventToolExecutionStart) {
		_, _ = runner.Emit(context.Background(), extension.ToolExecutionStartEvent{
			Type:       EventToolExecutionStart,
			ToolCallID: toolCallID,
			ToolName:   toolName,
			Args:       toolExecutionArgs(args),
		})
	}
}

// emitToolExecutionUpdate dispatches a tool_execution_update event.
// upstream: agent-session.ts:663-670
// Args is decoded to the same structured value as tool_execution_start so
// in-process and subprocess handlers observe one payload shape.
func emitToolExecutionUpdate(runner *inproc.Runner, toolCallID, toolName, content string, details any, args json.RawMessage) {
	if runner != nil && runner.HasHandlers(EventToolExecutionUpdate) {
		partialResult := map[string]any{"content": content}
		if details != nil {
			partialResult["details"] = details
		}
		_, _ = runner.Emit(context.Background(), extension.ToolExecutionUpdateEvent{
			Type:          EventToolExecutionUpdate,
			ToolCallID:    toolCallID,
			ToolName:      toolName,
			PartialResult: partialResult,
			Args:          toolExecutionArgs(args),
		})
	}
}

// emitToolExecutionEnd dispatches a tool_execution_end event.
// upstream: agent-session.ts:671-678
func emitToolExecutionEnd(runner *inproc.Runner, toolCallID, toolName string, result agent.AgentToolResult) {
	if runner != nil && runner.HasHandlers(EventToolExecutionEnd) {
		_, _ = runner.Emit(context.Background(), extension.ToolExecutionEndEvent{
			Type:       EventToolExecutionEnd,
			ToolCallID: toolCallID,
			ToolName:   toolName,
			Result:     extensionToolResult(result),
			IsError:    result.IsError,
		})
	}
}

// DispatchAgentLoopEvent maps one agent-loop event to the extension runner,
// mirroring upstream agent-session.ts::_emitExtensionEvent (lines 640-711).
// It is called from coding.Session.forwardAgentEvents so that every driver -
// interactive, rpc, and print: dispatches agent-loop events from one place
// (the session), matching upstream where _handleAgentEvent awaits
// _emitExtensionEvent before notifying listeners. currentMessage carries the
// in-flight message across message_start → message_update, as upstream passes
// event.message on message_update; the caller owns the storage (the session
// funnel is single-goroutine, so no lock is needed).
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
		emitAgentSettled(runner)
	case agent.TurnStartEvent:
		emitTurnStart(runner, e.TurnIndex)
	case agent.TurnEndEvent:
		emitTurnEnd(runner, e)
	case agent.MessageStartEvent:
		if currentMessage != nil {
			*currentMessage = e.Message
		}
		emitMessageStart(runner, e.Message)
	case agent.MessageUpdateEvent:
		var msg extension.AgentMessage
		if currentMessage != nil {
			msg = *currentMessage
		}
		emitMessageUpdate(runner, msg, e.AssistantMessageEvent)
	case agent.MessageEndEvent:
		emitMessageEnd(runner, e.Message)
	case agent.ToolExecutionStartEvent:
		emitToolExecutionStart(runner, e.ToolCallID, e.ToolName, e.Args)
	case agent.ToolExecutionUpdateEvent:
		emitToolExecutionUpdate(runner, e.ToolCallID, e.ToolName, e.Content, e.Details, e.Args)
	case agent.ToolExecutionEndEvent:
		emitToolExecutionEnd(runner, e.ToolCallID, e.ToolName, e.Result)
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
func emitUserBash(runner *inproc.Runner, command, cwd string, excludeFromContext bool) (*extension.UserBashEventResult, error) {
	if runner == nil || !runner.HasHandlers(EventUserBash) {
		return nil, nil
	}
	return runner.EmitUserBash(context.Background(), extension.UserBashEvent{
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
	result, err := runner.EmitInput(ctx, text, extensionImages(images), source, streamingBehavior)
	if err != nil {
		return text, images, false, err
	}
	switch result := result.(type) {
	case extension.InputEventResultHandled:
		return "", nil, true, nil
	case extension.InputEventResultTransform:
		if result.Images != nil {
			images = imagesFromExtension(result.Images)
		}
		return result.Text, images, false, nil
	default:
		return text, images, false, nil
	}
}

// emitModelSelect dispatches a model_select event when the user switches models.
// Mirrors upstream interactive-mode.ts model selection emit.
func emitModelSelect(runner *inproc.Runner, newModel, prevModel extension.Model, source extension.ModelSelectSource) {
	if runner != nil && runner.HasHandlers(EventModelSelect) {
		_, _ = runner.Emit(context.Background(), extension.ModelSelectEvent{
			Type:          EventModelSelect,
			Model:         newModel,
			PreviousModel: prevModel,
			Source:        source,
		})
	}
}

// emitThinkingLevelSelect dispatches a thinking_level_select event when the
// user changes the active thinking level. Mirrors upstream thinking-level
// selection events.
func emitThinkingLevelSelect(runner *inproc.Runner, level, previousLevel string) {
	if runner != nil && runner.HasHandlers(EventThinkingLevelSelect) {
		_, _ = runner.Emit(context.Background(), extension.ThinkingLevelSelectEvent{
			Type:          EventThinkingLevelSelect,
			Level:         level,
			PreviousLevel: previousLevel,
		})
	}
}

// emitSessionBeforeSwitch dispatches session_before_switch.
// Extensions can cancel the switch. Mirrors upstream interactive-mode.ts emitBeforeSwitch.
func emitSessionBeforeSwitch(runner *inproc.Runner, reason string, targetFile string) bool {
	if runner == nil || !runner.HasHandlers(EventSessionBeforeSwitch) {
		return false
	}
	result, _ := runner.Emit(context.Background(), extension.SessionBeforeSwitchEvent{
		Type:              EventSessionBeforeSwitch,
		Reason:            reason,
		TargetSessionFile: targetFile,
	})
	if m, ok := result.(map[string]any); ok {
		if cancel, ok := m["cancel"].(bool); ok {
			return cancel
		}
	}
	return false
}

// emitSessionBeforeFork dispatches session_before_fork.
// Extensions can cancel the fork. Mirrors upstream interactive-mode.ts emitBeforeFork.
func emitSessionBeforeFork(runner *inproc.Runner, entryID string) bool {
	if runner == nil || !runner.HasHandlers(EventSessionBeforeFork) {
		return false
	}
	result, _ := runner.Emit(context.Background(), extension.SessionBeforeForkEvent{
		Type:    EventSessionBeforeFork,
		EntryID: entryID,
	})
	if m, ok := result.(map[string]any); ok {
		if cancel, ok := m["cancel"].(bool); ok {
			return cancel
		}
	}
	return false
}

// modelToExtModel converts an ai.Model to extension.Model (map[string]any).
// Matches upstream's model payload shape in model_select events.
func modelToExtModel(m *ai.Model) extension.Model {
	if m == nil {
		return nil
	}
	return map[string]any{
		"id":          m.ID,
		"displayName": m.DisplayName,
	}
}
