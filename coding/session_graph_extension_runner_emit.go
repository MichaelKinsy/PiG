package coding

// The emit family of the ExtensionRunner graph object: Pi's emit, emitToolCall, emitToolResult, emitInput, emitContext, emitBeforeAgentStart, emitBeforeProviderRequest, emitBeforeProviderHeaders, emitUserBash, emitMessageEnd, emitResourcesDiscover, emitCacheWarmingDecision and emitBoundary, with Pi's JSON event and result shapes.

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// decodeRunnerEvent is the typed event of a RunnerEmitEvent in Pi's JSON shape (runner.ts:176-190): the events the generic emit() takes.
func decodeRunnerEvent(raw json.RawMessage) (extension.ExtensionEvent, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("event: %w", err)
	}
	switch probe.Type {
	case "mcp_servers_change":
		return decodeEvent[extension.McpServersChangeEvent](raw)
	case "session_start":
		return decodeEvent[extension.SessionStartEvent](raw)
	case "session_info_changed":
		return decodeEvent[extension.SessionInfoChangedEvent](raw)
	case "session_before_switch":
		return decodeEvent[extension.SessionBeforeSwitchEvent](raw)
	case "session_before_fork":
		return decodeEvent[extension.SessionBeforeForkEvent](raw)
	case "session_before_compact":
		return decodeEvent[extension.SessionBeforeCompactEvent](raw)
	case "session_compact":
		return decodeEvent[extension.SessionCompactEvent](raw)
	case "session_compact_failed":
		return decodeEvent[extension.SessionCompactFailedEvent](raw)
	case "session_shutdown":
		return decodeEvent[extension.SessionShutdownEvent](raw)
	case "session_before_tree":
		return decodeEvent[extension.SessionBeforeTreeEvent](raw)
	case "session_tree":
		return decodeEvent[extension.SessionTreeEvent](raw)
	case "after_provider_response":
		return decodeEvent[extension.AfterProviderResponseEvent](raw)
	case "provider_stream_event":
		return decodeEvent[extension.ProviderStreamEvent](raw)
	case "agent_start":
		return decodeEvent[extension.AgentStartEvent](raw)
	case "agent_end":
		return decodeEvent[extension.AgentEndEvent](raw)
	case "agent_settled":
		return decodeEvent[extension.AgentSettledEvent](raw)
	case "ui_prompt_start":
		return decodeEvent[extension.UIPromptStartEvent](raw)
	case "ui_prompt_end":
		return decodeEvent[extension.UIPromptEndEvent](raw)
	case "turn_start":
		return decodeEvent[extension.TurnStartEvent](raw)
	case "message_start":
		return decodeEvent[extension.MessageStartEvent](raw)
	case "message_update":
		return decodeEvent[extension.MessageUpdateEvent](raw)
	case "tool_execution_start":
		return decodeEvent[extension.ToolExecutionStartEvent](raw)
	case "tool_execution_update":
		return decodeEvent[extension.ToolExecutionUpdateEvent](raw)
	case "tool_execution_end":
		return decodeEvent[extension.ToolExecutionEndEvent](raw)
	case "model_select":
		return decodeEvent[extension.ModelSelectEvent](raw)
	case "thinking_level_select":
		return decodeEvent[extension.ThinkingLevelSelectEvent](raw)
	}
	return nil, fmt.Errorf("the event type %q is not one the generic emit() takes: use its own emit method", probe.Type)
}

func decodeEvent[T extension.ExtensionEvent](raw json.RawMessage) (extension.ExtensionEvent, error) {
	var event T
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, fmt.Errorf("event: %w", err)
	}
	return event, nil
}

// callEmit runs the emit method named by method, and reports an unknown method as such.
func (g *GraphExtensionRunner) callEmit(ctx context.Context, method string, args icodingagent.GraphArgs) (any, error) {
	runner := g.runner
	switch method {
	case "emit":
		raw := args.Raw(0)
		if raw == nil {
			return nil, nil
		}
		event, err := decodeRunnerEvent(raw)
		if err != nil {
			return nil, err
		}
		return runner.Emit(ctx, event)
	case "emitToolCall":
		var event extension.CustomToolCallEvent
		err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &event) })
		if err != nil {
			return nil, err
		}
		result, err := runner.EmitToolCall(ctx, event)
		if err != nil || result == nil {
			return nil, err
		}
		return result, nil
	case "emitToolResult":
		raw := args.Raw(0)
		if raw == nil {
			return nil, nil
		}
		event, err := extension.UnmarshalToolResultEvent(raw)
		if err != nil {
			return nil, err
		}
		result, err := runner.EmitToolResult(ctx, event)
		if err != nil || result == nil {
			return nil, err
		}
		return result, nil
	case "emitInput":
		text, err := args.String(0)
		if err != nil {
			return nil, err
		}
		var images []extension.ImageContent
		err = args.Unmarshal(1, func(raw json.RawMessage) error { return json.Unmarshal(raw, &images) })
		if err != nil {
			return nil, err
		}
		source, err := args.String(2)
		if err != nil {
			return nil, err
		}
		streamingBehavior, err := args.String(3)
		if err != nil {
			return nil, err
		}
		result, err := runner.EmitInput(ctx, text, images, source, streamingBehavior)
		if err != nil {
			return nil, err
		}
		encoded, err := extension.MarshalInputEventResult(result)
		return json.RawMessage(encoded), err
	case "emitContext":
		var messages []extension.AgentMessage
		err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &messages) })
		if err != nil {
			return nil, err
		}
		return runner.EmitContext(ctx, messages)
	case "emitBeforeAgentStart":
		return g.emitBeforeAgentStart(ctx, args)
	case "emitBeforeProviderRequest":
		// The payload is the provider's request, opaque JSON the handlers read and replace: it is handed on as the bytes received.
		var payload any
		if raw := args.Raw(0); raw != nil && string(raw) != "null" {
			payload = json.RawMessage(raw)
		}
		return runner.EmitBeforeProviderRequest(ctx, payload)
	case "emitBeforeProviderHeaders":
		var headers extension.ProviderHeaders
		err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &headers) })
		if err != nil {
			return nil, err
		}
		return runner.EmitBeforeProviderHeaders(ctx, headers)
	case "emitUserBash":
		var event extension.UserBashEvent
		err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &event) })
		if err != nil {
			return nil, err
		}
		result, err := runner.EmitUserBash(ctx, event)
		if err != nil || result == nil {
			return nil, err
		}
		return result, nil
	case "emitMessageEnd":
		var event extension.MessageEndEvent
		err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &event) })
		if err != nil {
			return nil, err
		}
		result, err := runner.EmitMessageEnd(ctx, event)
		if err != nil || result == nil {
			return nil, err
		}
		return result, nil
	case "emitResourcesDiscover":
		cwd, err := args.String(0)
		if err != nil {
			return nil, err
		}
		reason, err := args.String(1)
		if err != nil {
			return nil, err
		}
		return runner.EmitResourcesDiscover(ctx, cwd, reason)
	case "emitCacheWarmingDecision":
		var event extension.CacheWarmingDecisionEvent
		err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &event) })
		if err != nil {
			return nil, err
		}
		return runner.EmitCacheWarmingDecision(ctx, event)
	case "emitBoundary":
		return g.emitBoundary(ctx, args)
	}
	return nil, fmt.Errorf("ExtensionRunner has no method %s", method)
}

// emitBoundary is `emitBoundary(baseEvent, buildContext)`: buildContext is the extension's function, called with the drafts and answering the context preview.
func (g *GraphExtensionRunner) emitBoundary(ctx context.Context, args icodingagent.GraphArgs) (any, error) {
	raw := args.Raw(0)
	if raw == nil {
		return nil, fmt.Errorf("emitBoundary takes the base event")
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	var base extension.BoundaryBaseEvent
	switch probe.Type {
	case "turn_end":
		var event extension.TurnEndEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		var outcome struct {
			Outcome extension.AgentActivityOutcome `json:"outcome"`
		}
		if err := json.Unmarshal(raw, &outcome); err != nil {
			return nil, err
		}
		event.BoundaryState = &extension.BoundaryState{Outcome: outcome.Outcome}
		base = event
	case "agent_before_settle":
		event := &extension.AgentBeforeSettleEvent{}
		if err := json.Unmarshal(raw, event); err != nil {
			return nil, err
		}
		var outcome struct {
			Outcome extension.AgentActivityOutcome `json:"outcome"`
		}
		if err := json.Unmarshal(raw, &outcome); err != nil {
			return nil, err
		}
		event.Outcome = outcome.Outcome
		base = event
	default:
		return nil, fmt.Errorf("emitBoundary takes a turn_end or agent_before_settle event, not %q", probe.Type)
	}
	buildID, err := args.String(1)
	if err != nil {
		return nil, err
	}
	result, err := g.runner.EmitBoundary(ctx, base, func(entries []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
		answer, err := g.callback(ctx, buildID, nonNilSlice(entries))
		if err != nil {
			return extension.BoundaryContextPreview{}, err
		}
		return decodeJSON[extension.BoundaryContextPreview](answer)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"entries": nonNilSlice(result.Entries), "continue": result.Continue, "context": result.Context, "valid": result.Valid}, nil
}

// emitBeforeAgentStart is `emitBeforeAgentStart(trigger, systemPromptOptions)`: trigger is `{messageType: "user", prompt, images?}` or `{messageType: "custom", message}`. The result is `{messages, systemPromptOptions}`, the options the handlers' edits left, with the prompt a handler set as forceSystemPrompt.
func (g *GraphExtensionRunner) emitBeforeAgentStart(ctx context.Context, args icodingagent.GraphArgs) (any, error) {
	var wire struct {
		MessageType string                   `json:"messageType"`
		Prompt      string                   `json:"prompt"`
		Images      []extension.ImageContent `json:"images"`
		Message     *struct {
			CustomType string `json:"customType"`
			Content    any    `json:"content"`
			Display    bool   `json:"display"`
			Details    any    `json:"details"`
			Timestamp  int64  `json:"timestamp"`
		} `json:"message"`
	}
	err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &wire) })
	if err != nil {
		return nil, err
	}
	var options extension.BuildSystemPromptOptions
	err = args.Unmarshal(1, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) })
	if err != nil {
		return nil, err
	}
	var trigger extension.BeforeAgentStartTrigger
	switch wire.MessageType {
	case "user", "":
		trigger = extension.UserPromptTrigger(wire.Prompt, wire.Images)
	case "custom":
		if wire.Message == nil {
			return nil, fmt.Errorf("a custom before_agent_start trigger carries its message")
		}
		trigger = extension.CustomMessageTrigger(extension.CustomMessage{CustomType: wire.Message.CustomType, Content: wire.Message.Content, Display: wire.Message.Display, Details: wire.Message.Details, Timestamp: wire.Message.Timestamp})
	default:
		return nil, fmt.Errorf("before_agent_start trigger messageType %q is user or custom", wire.MessageType)
	}
	result, err := g.runner.EmitBeforeAgentStart(ctx, trigger, options)
	if err != nil {
		return nil, err
	}
	combined := extension.NormalizeBuildSystemPromptOptions(options)
	if result.SystemPromptOptions != nil {
		combined = *result.SystemPromptOptions
	}
	if result.SystemPrompt != nil {
		combined.ForceSystemPrompt = result.SystemPrompt
	}
	return map[string]any{"messages": nonNilSlice(result.Messages), "systemPromptOptions": combined}, nil
}
