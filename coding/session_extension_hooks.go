package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// installExtensionHooks installs the Session's extension hooks on its agent,
// once, for every mode. Mirrors upstream AgentSession._installAgentToolHooks
// (agent-session.ts:529-590): each hook reads the current runner and checks
// hasHandlers at call time, so a runner swapped by reload, or a handler added
// after the Session started, is honored without reinstalling anything.
func (s *Session) installExtensionHooks() {
	s.agent.AddBeforeToolCallHook(s.extensionToolCallHook)
	s.agent.AddAfterToolCallHook(s.extensionToolResultHook)
	// sdk.ts installs transformContext and onPayload on the Agent for every mode.
	s.agent.SetTransformContextWithContext(s.extensionContextHook)
	s.agent.SetBeforeProviderHook(s.extensionProviderRequestHook)
	s.agent.SetTransformHeaders(s.extensionProviderHeadersHook)
}

// extensionToolCallHook emits tool_call. A handler error blocks the call with
// the error message, as upstream rethrows it and the agent loop turns it into
// an error tool result. A handler may block (with terminate) or mutate
// event.input in place; mutated input runs without revalidation.
func (s *Session) extensionToolCallHook(ctx context.Context, toolCallID, toolName string, args json.RawMessage) agent.ToolCallHookResult {
	runner := s.currentRunner()
	if runner == nil || !runner.HasHandlers(icodingagent.EventToolCall) {
		return agent.ToolCallHookResult{}
	}
	var input map[string]any
	if len(args) > 0 {
		_ = json.Unmarshal(args, &input) // invalid JSON yields a nil input, as validation already ran
	}
	before, _ := json.Marshal(input)
	result, err := runner.EmitToolCall(ctx, extension.CustomToolCallEvent{
		ToolCallEventBase: extension.ToolCallEventBase{Type: icodingagent.EventToolCall, ToolCallID: toolCallID},
		ToolName:          toolName,
		Input:             input,
	})
	if err != nil {
		return agent.ToolCallHookResult{Block: true, Reason: err.Error()}
	}
	var hook agent.ToolCallHookResult
	if after, err := json.Marshal(input); err == nil && !bytes.Equal(before, after) {
		hook.Args = after
	}
	if result != nil {
		hook.Block = result.Block
		hook.Reason = result.Reason
		hook.Terminate = result.Terminate
	}
	return hook
}

// extensionToolResultHook emits tool_result and applies the chained override.
func (s *Session) extensionToolResultHook(ctx context.Context, toolCallID, toolName string, args json.RawMessage, result agent.AgentToolResult) agent.AfterToolCallResult {
	runner := s.currentRunner()
	if runner == nil || !runner.HasHandlers(icodingagent.EventToolResult) {
		return agent.AfterToolCallResult{}
	}
	var input map[string]any
	if len(args) > 0 {
		_ = json.Unmarshal(args, &input)
	}
	hookResult, err := runner.EmitToolResult(ctx, extension.CustomToolResultEvent{
		ToolResultEventBase: extension.ToolResultEventBase{
			Type:       icodingagent.EventToolResult,
			ToolCallID: toolCallID,
			Input:      input,
			Content:    icodingagent.ToolResultEventContent(result),
			IsError:    result.IsError,
			Usage:      toolResultEventUsage(result.Usage),
		},
		ToolName: toolName,
		Details:  extension.ToolResultDetailsFor(result.Details),
	})
	if err != nil || hookResult == nil {
		return agent.AfterToolCallResult{}
	}
	return icodingagent.ToolResultEventOverride(hookResult)
}

// extensionContextHook emits context before each provider request and uses
// the returned conversation. Mirrors upstream runner.emitContext's context
// phase (runner.ts:1190-1223): handlers see only the non-system messages, an
// unchanged conversation keeps every system message in place, and a changed
// one gets the current prompt and tool state as one leading system message.
func (s *Session) extensionContextHook(ctx context.Context, messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
	runner := s.currentRunner()
	if runner == nil {
		return messages, nil
	}
	transformed, err := s.contextPhase(ctx, runner, messages)
	if err != nil {
		return nil, err
	}
	return s.contextWithSystemPhase(ctx, runner, transformed)
}

// contextWithSystemPhase runs context_with_system handlers on the full
// transcript and uses their result as returned.
func (s *Session) contextWithSystemPhase(ctx context.Context, runner *inproc.Runner, messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
	if !runner.HasHandlers(icodingagent.EventContextWithSystem) {
		return messages, nil
	}
	all := make([]extension.AgentMessage, len(messages))
	for i, message := range messages {
		all[i] = message
	}
	returned, err := runner.EmitContextWithSystem(ctx, all)
	if err != nil {
		return nil, err
	}
	converted, err := agentMessagesFromExtension(returned)
	if err != nil {
		return messages, nil
	}
	return converted, nil
}

func (s *Session) contextPhase(ctx context.Context, runner *inproc.Runner, messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
	if !runner.HasHandlers(icodingagent.EventContext) {
		return messages, nil
	}
	visible := make([]extension.AgentMessage, 0, len(messages))
	var systems []ai.Message
	for _, message := range messages {
		if message.System != nil {
			systems = append(systems, *message.System)
			continue
		}
		visible = append(visible, message)
	}
	returned, replaced, err := runner.EmitContextTracked(ctx, visible)
	if err != nil {
		return nil, err
	}
	// Identity, not value, decides replacement (upstream sameMessages): a
	// fresh message with equal fields is still a replacement, so the value
	// shortcut applies only when no handler replaced the list.
	if !replaced && sameContextMessages(returned, visible) {
		return messages, nil
	}
	converted, err := agentMessagesFromExtension(returned)
	if err != nil {
		return messages, nil
	}
	if !replaced && len(converted) == len(visible) {
		// upstream restoreSystemMessages: an unchanged conversation (edited in
		// place) keeps every system message where it was.
		out := make([]agent.AgentMessage, 0, len(messages))
		next := 0
		for _, message := range messages {
			if message.System != nil {
				out = append(out, message)
				continue
			}
			if sameContextMessages([]extension.AgentMessage{returned[next]}, []extension.AgentMessage{visible[next]}) {
				out = append(out, message)
			} else {
				out = append(out, converted[next])
			}
			next++
		}
		return out, nil
	}
	if head := ai.GetCurrentSystemMessage(systems); head != nil {
		return append([]agent.AgentMessage{{System: head}}, converted...), nil
	}
	return converted, nil
}

// sameContextMessages reports whether handlers left the conversation
// unchanged. The runner hands handlers a clone, and a subprocess handler
// returns decoded JSON, so equality is by value rather than identity.
func sameContextMessages(returned, visible []extension.AgentMessage) bool {
	if len(returned) != len(visible) {
		return false
	}
	for i := range returned {
		if reflect.DeepEqual(returned[i], visible[i]) {
			continue
		}
		left, errLeft := json.Marshal(returned[i])
		right, errRight := json.Marshal(visible[i])
		if errLeft != nil || errRight != nil {
			return false
		}
		var leftValue, rightValue any
		if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil || !reflect.DeepEqual(leftValue, rightValue) {
			return false
		}
	}
	return true
}

// agentMessagesFromExtension converts handler-returned messages back to agent
// messages: in-process handlers return agent messages, subprocess handlers
// return the upstream JSON shape.
func agentMessagesFromExtension(messages []extension.AgentMessage) ([]agent.AgentMessage, error) {
	out := make([]agent.AgentMessage, 0, len(messages))
	for _, message := range messages {
		switch value := message.(type) {
		case agent.AgentMessage:
			out = append(out, value)
		case *agent.AgentMessage:
			if value == nil {
				return nil, errors.New("context handler returned a nil message")
			}
			out = append(out, *value)
		default:
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			var decoded agent.AgentMessage
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return nil, err
			}
			out = append(out, decoded)
		}
	}
	return out, nil
}

// extensionProviderRequestHook emits before_provider_request with the
// provider's final wire payload; a handler may return a replacement.
func (s *Session) extensionProviderRequestHook(payload any, _ *ai.Model) (any, error) {
	runner := s.currentRunner()
	if runner == nil || !runner.HasHandlers(icodingagent.EventBeforeProviderRequest) {
		return payload, nil
	}
	result, err := runner.EmitBeforeProviderRequest(context.Background(), payload)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return payload, nil
	}
	return result, nil
}

// extensionProviderHeadersHook emits before_provider_headers on the merged
// request headers (sdk.ts buildRequestOptions.transformHeaders).
func (s *Session) extensionProviderHeadersHook(ctx context.Context, headers ai.ProviderHeaders) (ai.ProviderHeaders, error) {
	runner := s.currentRunner()
	if runner == nil || !runner.HasHandlers(icodingagent.EventBeforeProviderHeaders) {
		return headers, nil
	}
	return runner.EmitBeforeProviderHeaders(ctx, headers)
}

// extensionEventHook runs on the agent goroutine for every agent event,
// before persistence and delivery, as upstream _handleAgentEvent awaits
// _emitExtensionEvent before it persists or notifies listeners. A
// message_end replacement from a handler is applied in place. A panic during
// dispatch is reported and the event continues.
func (s *Session) extensionEventHook(ev agent.AgentEvent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.reportAgentEventPanic(ev, recovered)
		}
	}()
	runner := s.currentRunner()
	if runner == nil {
		return
	}
	end, ok := ev.(agent.MessageEndEvent)
	if !ok {
		icodingagent.DispatchAgentLoopEvent(runner, ev, &s.extCurrentMessage)
		return
	}
	if !runner.HasHandlers(icodingagent.EventMessageEnd) {
		return
	}
	replacement, err := runner.EmitMessageEnd(context.Background(), end.Message)
	if err != nil || replacement == nil {
		return
	}
	converted, err := agentMessagesFromExtension([]extension.AgentMessage{replacement})
	if err != nil {
		return
	}
	replaceMessageInPlace(end.Message, normalizeReplacementContent(converted[0]))
}

// normalizeReplacementContent gives a replacement with missing content an
// empty content list, as upstream normalizes untyped handler results before
// they enter agent state or session history.
func normalizeReplacementContent(message agent.AgentMessage) agent.AgentMessage {
	switch {
	case message.User != nil && message.User.Content == nil:
		message.User.Content = []ai.UserContentBlock{}
	case message.Assistant != nil && message.Assistant.Content == nil:
		message.Assistant.Content = []ai.AssistantContentBlock{}
	case message.ToolResult != nil && message.ToolResult.Content == nil:
		message.ToolResult.Content = []ai.ToolResultMessageContent{}
	case message.Custom != nil && message.Custom["role"] == agent.RoleCustom && message.Custom["content"] == nil:
		message.Custom["content"] = []any{}
	}
	return message
}

// replaceMessageInPlace overwrites target with replacement through the
// pointers the agent's transcript, the event and persistence share
// (agent-session.ts _replaceMessageInPlace). A replacement of another shape is
// ignored.
func replaceMessageInPlace(target, replacement agent.AgentMessage) {
	switch {
	case target.System != nil && replacement.System != nil:
		*target.System = *replacement.System
	case target.User != nil && replacement.User != nil:
		*target.User = *replacement.User
	case target.Assistant != nil && replacement.Assistant != nil:
		*target.Assistant = *replacement.Assistant
	case target.ToolResult != nil && replacement.ToolResult != nil:
		*target.ToolResult = *replacement.ToolResult
	case target.Custom != nil && replacement.Custom != nil:
		// A handler may return the event's own message: snapshot it first,
		// as upstream returns early when target === replacement.
		fields := maps.Clone(replacement.Custom)
		clear(target.Custom)
		maps.Copy(target.Custom, fields)
	}
}

// QueueAgentStartMessages holds the messages a before_agent_start dispatch
// returned; the next prompt carries them as custom messages after the user
// message and any next-turn messages (agent-session.ts prompt()). Every mode
// that emits before_agent_start hands its result here.
func (s *Session) QueueAgentStartMessages(messages []extension.CustomMessageRef) {
	if len(messages) == 0 {
		return
	}
	timestamp := time.Now().UnixMilli()
	custom := make([]agent.AgentMessage, 0, len(messages))
	for _, message := range messages {
		content := message.Content
		if content == nil {
			// Untyped extensions can pass null or missing content.
			content = []any{}
		}
		fields := map[string]any{
			"role":       agent.RoleCustom,
			"customType": message.CustomType,
			"content":    content,
			"timestamp":  timestamp,
		}
		if message.Display != nil {
			fields["display"] = message.Display
		}
		if message.Details != nil {
			fields["details"] = message.Details
		}
		custom = append(custom, agent.AgentMessage{Custom: fields})
	}
	s.agentStartMu.Lock()
	s.agentStartMessages = append(s.agentStartMessages, custom...)
	s.agentStartMu.Unlock()
}

func (s *Session) takeAgentStartMessages() []agent.AgentMessage {
	s.agentStartMu.Lock()
	defer s.agentStartMu.Unlock()
	messages := s.agentStartMessages
	s.agentStartMessages = nil
	return messages
}

// ExtensionCommandActions returns the command-context actions the Session
// backs in every mode. Modes may bind their own versions over them (for
// example to refresh a UI after navigating).
func (s *Session) ExtensionCommandActions() extension.CommandActions {
	return extension.CommandActions{
		WaitForIdle: func() error { return s.WaitForIdle(context.Background()) },
		// upstream print-mode.ts/rpc-mode.ts bind navigateTree to
		// session.navigateTree and return only whether it was cancelled.
		NavigateTree: func(targetID string, opts *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
			var options NavigateTreeOptions
			if opts != nil {
				options.Summarize = opts.Summarize
				options.CustomInstructions = opts.CustomInstructions
			}
			result, err := s.NavigateTree(context.Background(), targetID, options)
			if err != nil {
				return extension.CancelledResult{}, err
			}
			return extension.CancelledResult{Cancelled: result.Cancelled}, nil
		},
	}
}

// bindExtensionCommandActions binds the Session's command actions on runner.
func (s *Session) bindExtensionCommandActions(runner *inproc.Runner) {
	if runner != nil {
		runner.BindCommandActions(s.ExtensionCommandActions())
	}
}

// toolResultEventUsage exposes the tool's own usage on the event, or nothing.
func toolResultEventUsage(usage *ai.Usage) any {
	if usage == nil {
		return nil
	}
	return usage
}
