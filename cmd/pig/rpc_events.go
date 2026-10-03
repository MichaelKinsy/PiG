// Ports packages/coding-agent/src/modes/json-event.ts.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// subscribeRPCEvents observes each mode event after awaited extension handling and before Session persistence, as rpc-mode.ts:354-363 subscribes to AgentSession. The separate Events consumer only drains and acknowledges delivery barriers.
func subscribeRPCEvents(session *coding.Session, write func(any), fail func(error)) func() {
	failed := false
	return session.Subscribe(func(event agent.AgentEvent) {
		if failed {
			return
		}
		if _, alreadyPublished := event.(agent.SessionInfoChangedEvent); alreadyPublished {
			return
		}
		frames, err := rpcAgentEvent(event)
		if err != nil {
			failed = true
			fail(err)
			return
		}
		for _, frame := range frames {
			write(frame)
		}
	})
}

// subscribeStdoutBackpressure registers Pi's Agent listener `async () => { await waitForRawStdoutBackpressure(); }` (rpc-mode.ts:361-363, print-mode.ts:113-118). It is subscribed after the Session's own Agent listener, so it runs after that event's subscribers have serialized and written it. Every write here completes before it returns, so the wait is the barrier against a write in flight on another goroutine; the observable effect is that the awaited listener resumes from the write callback, which Node runs after every promise reaction (output-guard.ts:95-101). A listener without a stream scope runs outside the continuation queue and waits directly.
func subscribeStdoutBackpressure(a *agent.Agent, wait func()) func() {
	return a.Subscribe(func(_ context.Context, event agent.AgentEvent) error {
		if observation := agent.EventObservation(event); observation != nil {
			return observation.AwaitTick(func() error { wait(); return nil })
		}
		wait()
		return nil
	})
}

// rpcAgentEvent observes retained messages and converts events to Pi JSON/RPC shapes in source insertion order. Conversion errors are returned so writer paths can terminate loudly.
func rpcAgentEvent(event agent.AgentEvent) ([]any, error) {
	switch event := event.(type) {
	case agent.AgentStartEvent:
		return []any{rpcObject{{"type", "agent_start"}}}, nil
	case agent.AgentEndEvent:
		messages, err := rpcAgentMessages(event.Messages)
		if err != nil {
			return nil, err
		}
		return []any{rpcObject{{"type", "agent_end"}, {"messages", messages}, {"willRetry", event.WillRetry}}}, nil
	case agent.AgentSettledEvent:
		return []any{rpcObject{{"type", "agent_settled"}}}, nil
	case agent.BashExecutionUpdateEvent:
		var id rpcRequestID
		if event.ID != nil {
			id = rpcRequestID(*event.ID)
		}
		return []any{RPCBashExecutionUpdate{Type: "bash_execution_update", ID: id, Delta: event.Delta}}, nil
	case agent.QueueUpdateEvent:
		return []any{RPCQueueUpdateEvent{Type: "queue_update", Steering: event.Steering, FollowUp: event.FollowUp}}, nil
	case agent.SessionInfoChangedEvent:
		return []any{rpcSessionInfoChanged(event.Name)}, nil
	case agent.ThinkingLevelChangedEvent:
		return []any{RPCThinkingLevelChangedEvent{Type: "thinking_level_changed", Level: event.Level}}, nil
	case agent.CompactionStartEvent:
		return []any{rpcObject{{"type", "compaction_start"}, {"reason", event.Reason}}}, nil
	case agent.CompactionEndEvent:
		out := rpcObject{{"type", "compaction_end"}, {"reason", event.Reason}}
		if event.Summary != "" {
			result := rpcObject{{"summary", event.Summary}, {"firstKeptEntryId", event.FirstKeptEntryID}, {"tokensBefore", event.TokensBefore}, {"estimatedTokensAfter", event.EstimatedTokensAfter}}
			if event.Usage != nil {
				result = append(result, rpcField{"usage", rpcUsage(event.Usage)})
			}
			if details := rpcOptionalCompactionDetails(event.Details); details != nil {
				result = append(result, rpcField{"details", details})
			}
			out = append(out, rpcField{"result", result})
		}
		out = append(out, rpcField{"aborted", event.Aborted}, rpcField{"willRetry", event.WillRetry})
		if event.ErrorMessage != "" {
			out = append(out, rpcField{"errorMessage", event.ErrorMessage})
		}
		return []any{out}, nil
	case agent.AutoRetryStartEvent:
		return []any{rpcObject{{"type", "auto_retry_start"}, {"attempt", event.Attempt}, {"maxAttempts", event.MaxAttempts}, {"delayMs", event.DelayMs}, {"errorMessage", event.ErrorMessage}}}, nil
	case agent.AutoRetryEndEvent:
		out := rpcObject{{"type", "auto_retry_end"}, {"success", event.Success}, {"attempt", event.Attempt}}
		if event.FinalError != "" {
			out = append(out, rpcField{"finalError", event.FinalError})
		}
		return []any{out}, nil
	case agent.SummarizationRetryScheduledEvent:
		return []any{rpcObject{{"type", "summarization_retry_scheduled"}, {"attempt", event.Attempt}, {"maxAttempts", event.MaxAttempts}, {"delayMs", event.DelayMs}, {"errorMessage", event.ErrorMessage}}}, nil
	case agent.SummarizationRetryAttemptStartEvent:
		out := rpcObject{{"type", "summarization_retry_attempt_start"}, {"source", event.Source}}
		if event.Source == "compaction" {
			out = append(out, rpcField{"reason", event.Reason})
		}
		return []any{out}, nil
	case agent.SummarizationRetryFinishedEvent:
		return []any{rpcObject{{"type", "summarization_retry_finished"}}}, nil
	case agent.TurnStartEvent:
		return []any{rpcObject{{"type", "turn_start"}}}, nil
	case agent.TurnEndEvent:
		message, err := rpcAgentMessage(event.Message)
		if err != nil {
			return nil, err
		}
		toolResults, err := rpcToolResultMessages(event.ToolResults)
		if err != nil {
			return nil, err
		}
		return []any{rpcObject{{"type", "turn_end"}, {"message", message}, {"toolResults", toolResults}}}, nil
	case agent.MessageStartEvent:
		message, err := rpcAgentMessage(event.Message)
		if err != nil {
			return nil, err
		}
		return []any{rpcObject{{"type", "message_start"}, {"message", message}}}, nil
	case agent.MessageUpdateEvent:
		update, err := rpcMessageUpdate(event)
		if err != nil {
			return nil, err
		}
		return []any{update}, nil
	case agent.MessageEndEvent:
		message, err := rpcAgentMessage(event.Message)
		if err != nil {
			return nil, err
		}
		return []any{rpcObject{{"type", "message_end"}, {"message", message}}}, nil
	case agent.EntryAppendedEvent:
		return []any{rpcSessionEntryAppendedEvent{Type: "entry_appended", Entry: event.Entry}}, nil
	case agent.ToolExecutionStartEvent:
		return []any{rpcObject{{"type", "tool_execution_start"}, {"toolCallId", event.ToolCallID}, {"toolName", event.ToolName}, {"args", json.RawMessage(event.Args)}}}, nil
	case agent.ToolExecutionUpdateEvent:
		// The partial result is the object the tool passed to onUpdate (agent-loop.ts:778-786): shell startup has no content blocks and no details (bash.ts:321-323), output snapshots a text block and details (bash.ts:270-276), codemode no blocks and details (codemode/execute.ts:235).
		partial, err := rpcToolResult(event.PartialResult)
		if err != nil {
			return nil, fmt.Errorf("tool_execution_update partialResult: %w", err)
		}
		return []any{rpcObject{{"type", "tool_execution_update"}, {"toolCallId", event.ToolCallID}, {"toolName", event.ToolName}, {"args", json.RawMessage(event.Args)}, {"partialResult", partial}}}, nil
	case agent.ToolExecutionEndEvent:
		result, err := rpcToolResult(event.Result)
		if err != nil {
			return nil, err
		}
		return []any{rpcObject{{"type", "tool_execution_end"}, {"toolCallId", event.ToolCallID}, {"toolName", event.ToolName}, {"result", result}, {"isError", event.IsError}}}, nil
	default:
		return nil, fmt.Errorf("unsupported agent event %T", event)
	}
}

// rpcToolResult writes the AgentToolResult of tool_execution_update and tool_execution_end as Pi does, key by key in the order the object holds them.
// upstream: packages/agent/src/agent-loop.ts:778-786, 912-919 emit the object the tool built, whose keys are the ones the tool or a hook set (agent/src/types.ts:435-455): a built-in tool builds content, details, structuredContent, isError (coding-agent/src/core/tools/bash.ts:403-409), then usage and terminate, and a tool's own object keeps its own order (AgentToolResult.MemberNames).
// afterToolCall's result is spread over the tool's (agent-loop.ts:877-889): keys keep their positions, and a structuredContent the hook added to a result that had none comes last.
// Only the errors a tool returns are written as isError: a thrown error, an unknown tool, invalid arguments and a blocked call give createErrorToolResult, which has none (agent-loop.ts:906-910).
func rpcToolResult(result agent.AgentToolResult) (rpcObject, error) {
	var out rpcObject
	for _, name := range result.MemberNames() {
		switch name {
		case "content":
			content := result.Content
			if content == nil {
				content = []ai.ToolResultMessageContent{}
			}
			out = append(out, rpcField{"content", content})
		case "details":
			out = append(out, rpcField{"details", result.Details})
		case "structuredContent":
			// Pi serializes the value with JSON.stringify, so the raw text a tool or SDK produced is written as JSON.stringify(JSON.parse(raw)).
			canonical, err := jsonstringify.Canonicalize(result.StructuredContent)
			if err != nil {
				return nil, fmt.Errorf("tool_execution_end structuredContent: %w", err)
			}
			out = append(out, rpcField{"structuredContent", json.RawMessage(canonical)})
		case "isError":
			out = append(out, rpcField{"isError", result.IsError})
		case "usage":
			if result.Usage == nil {
				// The tool wrote usage: null.
				out = append(out, rpcField{"usage", nil})
				continue
			}
			out = append(out, rpcField{"usage", rpcUsage(result.Usage)})
		case "terminate":
			out = append(out, rpcField{"terminate", result.Terminate})
		}
	}
	return out, nil
}

// rpcSessionEntryAppendedEvent carries a Session entry appended outside the agent loop exactly as it was persisted.
type rpcSessionEntryAppendedEvent struct {
	Type  string          `json:"type"`
	Entry json.RawMessage `json:"entry"`
}

func rpcMessageUpdate(event agent.MessageUpdateEvent) (any, error) {
	if event.Message.Assistant == nil {
		return nil, fmt.Errorf("message_update message is not an assistant message")
	}
	if event.AssistantMessageEvent == nil {
		return nil, fmt.Errorf("message_update assistant event is nil")
	}
	encoded, err := json.Marshal(rpcAssistantEventFields(event.AssistantMessageEvent))
	if err != nil {
		return nil, fmt.Errorf("marshal message_update assistant event: %w", err)
	}
	var wire rpcObject
	if err := json.Unmarshal(encoded, &wire); err != nil {
		return nil, fmt.Errorf("decode message_update assistant event: %w", err)
	}
	wire = slices.DeleteFunc(wire, func(field rpcField) bool { return field.name == "partial" })
	if start, ok := event.AssistantMessageEvent.(ai.ToolCallStartEvent); ok {
		id, name, toolCall, indexValid := start.Partial.ObserveToolCallIdentity(start.ContentIndex)
		if !indexValid {
			return nil, fmt.Errorf("toolcall_start content index %d is invalid", start.ContentIndex)
		}
		if !toolCall {
			return nil, fmt.Errorf("toolcall_start content at index %d is not a tool call", start.ContentIndex)
		}
		wire = append(wire, rpcField{"id", id}, rpcField{"toolName", name})
	}
	return rpcObject{{"type", "message_update"}, {"usage", rpcUsage(event.Message.Assistant.ObserveUsage())}, {"assistantMessageEvent", wire}}, nil
}

// rpcAssistantEventFields excludes cumulative partials before invoking any JSON marshaler. Retained fields keep their existing serialization, order and error handling.
func rpcAssistantEventFields(event ai.AssistantMessageEvent) any {
	switch event := event.(type) {
	case ai.StartEvent:
		return rpcObject{{"type", ai.EventStart}}
	case ai.TextStartEvent:
		return rpcObject{{"type", ai.EventTextStart}, {"contentIndex", event.ContentIndex}}
	case ai.TextDeltaEvent:
		return rpcObject{{"type", ai.EventTextDelta}, {"contentIndex", event.ContentIndex}, {"delta", event.Delta}}
	case ai.TextEndEvent:
		return rpcObject{{"type", ai.EventTextEnd}, {"contentIndex", event.ContentIndex}, {"content", event.Content}}
	case ai.ThinkingStartEvent:
		return rpcObject{{"type", ai.EventThinkingStart}, {"contentIndex", event.ContentIndex}}
	case ai.ThinkingDeltaEvent:
		return rpcObject{{"type", ai.EventThinkingDelta}, {"contentIndex", event.ContentIndex}, {"delta", event.Delta}}
	case ai.ThinkingEndEvent:
		return rpcObject{{"type", ai.EventThinkingEnd}, {"contentIndex", event.ContentIndex}, {"content", event.Content}}
	case ai.ToolCallStartEvent:
		return rpcObject{{"type", ai.EventToolCallStart}, {"contentIndex", event.ContentIndex}}
	case ai.ToolCallDeltaEvent:
		return rpcObject{{"type", ai.EventToolCallDelta}, {"contentIndex", event.ContentIndex}, {"delta", event.Delta}}
	case ai.ToolCallEndEvent:
		return rpcObject{{"type", ai.EventToolCallEnd}, {"contentIndex", event.ContentIndex}, {"toolCall", event.ToolCall}}
	default:
		return event
	}
}

func rpcAgentMessages(messages []agent.AgentMessage) ([]any, error) {
	out := make([]any, 0, len(messages))
	for i, message := range messages {
		wire, err := rpcAgentMessage(message)
		if err != nil {
			return nil, fmt.Errorf("agent messages[%d]: %w", i, err)
		}
		out = append(out, wire)
	}
	return out, nil
}

func rpcAgentMessage(message agent.AgentMessage) (any, error) {
	switch {
	case message.System != nil:
		return message.System, nil
	case message.User != nil:
		content, err := rpcUserContent(message.User.Content)
		if err != nil {
			return nil, err
		}
		return rpcObject{{"role", "user"}, {"content", content}, {"timestamp", message.User.Timestamp}}, nil
	case message.Assistant != nil:
		assistant := message.Assistant.Observe()
		content, err := rpcAssistantContent(assistant.Content)
		if err != nil {
			return nil, err
		}
		out := rpcObject{{"role", "assistant"}, {"content", content}, {"api", assistant.API}, {"provider", assistant.Provider}, {"model", assistant.ModelID}, {"usage", rpcUsage(assistant.Usage)}}
		// JSON.stringify omits an undefined stopReason; timestamp is part of the initial provider message, before subsequently appended metadata.
		if assistant.StopReason != "" {
			out = append(out, rpcField{"stopReason", assistant.StopReason})
		}
		out = append(out, rpcField{"timestamp", assistant.Timestamp})
		if assistant.ResponseModel != "" {
			out = append(out, rpcField{"responseModel", assistant.ResponseModel})
		}
		if assistant.ResponseID != "" {
			out = append(out, rpcField{"responseId", assistant.ResponseID})
		}
		if assistant.ProviderThinkingLevel != "" {
			out = append(out, rpcField{"providerThinkingLevel", assistant.ProviderThinkingLevel})
		}
		if len(assistant.Diagnostics) > 0 {
			out = append(out, rpcField{"diagnostics", assistant.Diagnostics})
		}
		if assistant.Deferred != nil {
			out = append(out, rpcField{"deferred", assistant.Deferred})
		}
		if assistant.ErrorMessage != "" {
			out = append(out, rpcField{"errorMessage", assistant.ErrorMessage})
		}
		if assistant.RawStopReason != "" {
			out = append(out, rpcField{"rawStopReason", assistant.RawStopReason})
		}
		if assistant.EndTurn != nil {
			out = append(out, rpcField{"endTurn", *assistant.EndTurn})
		}
		// upstream: agent-loop.ts:409 assigns thinkingLevel to the finished response, after the provider's own keys.
		if assistant.ThinkingLevel != "" {
			out = append(out, rpcField{"thinkingLevel", assistant.ThinkingLevel})
		}
		return out, nil
	case message.ToolResult != nil:
		return rpcToolResultMessage(*message.ToolResult)
	case message.Custom != nil:
		// The message writes its own members in Pi's order (messages.ts:123-137) and keeps an extension's `details` as written.
		return message, nil
	default:
		return nil, fmt.Errorf("agent message has no variant")
	}
}

func rpcToolResultMessages(messages []agent.ToolResultMessage) ([]any, error) {
	out := make([]any, 0, len(messages))
	for i, message := range messages {
		wire, err := rpcToolResultMessage(message)
		if err != nil {
			return nil, fmt.Errorf("tool results[%d]: %w", i, err)
		}
		out = append(out, wire)
	}
	return out, nil
}

func rpcToolResultMessage(message agent.ToolResultMessage) (any, error) {
	content, err := rpcToolResultContent(message.Content)
	if err != nil {
		return nil, err
	}
	out := rpcObject{{"role", "toolResult"}, {"toolCallId", message.ToolCallID}, {"toolName", message.ToolName}, {"content", content}}
	if message.Details != nil || message.DetailsNull {
		out = append(out, rpcField{"details", message.Details})
	}
	if message.Usage != nil {
		out = append(out, rpcField{"usage", rpcUsage(message.Usage)})
	}
	out = append(out, rpcField{"isError", message.IsError}, rpcField{"timestamp", message.Timestamp})
	return out, nil
}

// rpcToolResultPayload preserves absent text separately from an explicit empty text block.
func rpcToolResultPayload(text *string, images []ai.ImageContent, details any) any {
	content := make([]any, 0, 1+len(images))
	if text != nil {
		content = append(content, rpcObject{{"type", "text"}, {"text", *text}})
	}
	for _, image := range images {
		content = append(content, rpcObject{{"type", "image"}, {"data", image.Data}, {"mimeType", image.MimeType}})
	}
	out := rpcObject{{"content", content}}
	if details != nil {
		out = append(out, rpcField{"details", details})
	}
	return out
}

func rpcUserContent(content ai.UserContent) (any, error) {
	if text, ok := content.(ai.UserText); ok {
		return string(text), nil
	}
	blocks, _ := content.(ai.UserContentBlocks)
	out := make([]any, 0, len(blocks))
	for i, block := range blocks {
		wire, err := rpcContentBlock(block)
		if err != nil {
			return nil, fmt.Errorf("user content[%d]: %w", i, err)
		}
		out = append(out, wire)
	}
	return out, nil
}

func rpcAssistantContent(blocks []ai.AssistantContentBlock) ([]any, error) {
	out := make([]any, 0, len(blocks))
	for i, block := range blocks {
		wire, err := rpcContentBlock(block)
		if err != nil {
			return nil, fmt.Errorf("assistant content[%d]: %w", i, err)
		}
		if object, ok := wire.(rpcObject); ok {
			scratch, err := rpcStreamingBlockScratch(block)
			if err != nil {
				return nil, fmt.Errorf("assistant content[%d]: %w", i, err)
			}
			wire = scratch.apply(object)
		}
		out = append(out, wire)
	}
	return out, nil
}

// rpcBlockScratch is what a streamed text or thinking block carries beyond its stable fields: the provider event index while its content block is open, and a thinking signature that is present but empty.
type rpcBlockScratch struct {
	index          *int
	emptySignature bool
}

// rpcStreamingBlockScratch reads the block's own JSON encoding, which owns those fields and their order; the wire object built from the stable fields holds neither.
func rpcStreamingBlockScratch(block ai.AssistantContentBlock) (rpcBlockScratch, error) {
	encoded, err := json.Marshal(block)
	if err != nil {
		return rpcBlockScratch{}, err
	}
	var fields struct {
		Index     *int    `json:"index"`
		Signature *string `json:"thinkingSignature"`
	}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return rpcBlockScratch{}, err
	}
	return rpcBlockScratch{index: fields.Index, emptySignature: fields.Signature != nil && *fields.Signature == ""}, nil
}

// apply adds the scratch fields in Pi's property order: signature after the thinking text, index last.
func (scratch rpcBlockScratch) apply(object rpcObject) rpcObject {
	if scratch.emptySignature {
		fields := make(rpcObject, 0, len(object)+2)
		fields = append(fields, object[:2]...)
		fields = append(fields, rpcField{"thinkingSignature", ""})
		object = append(fields, object[2:]...)
	}
	if scratch.index != nil {
		object = append(object, rpcField{"index", *scratch.index})
	}
	return object
}

func rpcToolResultContent(blocks []ai.ToolResultMessageContent) ([]any, error) {
	out := make([]any, 0, len(blocks))
	for i, block := range blocks {
		wire, err := rpcContentBlock(block)
		if err != nil {
			return nil, fmt.Errorf("tool result content[%d]: %w", i, err)
		}
		out = append(out, wire)
	}
	return out, nil
}

func rpcContentBlock(block ai.ContentBlock) (any, error) {
	switch block := block.(type) {
	case ai.TextContent, ai.ToolCall, ai.ThinkingContent:
		// These blocks own the provider's optional streaming scratch fields (`index`, `partialJson`, ...), the presence of an empty thinking signature, and their JSON order.
		return block, nil
	case ai.ImageContent:
		return rpcObject{{"type", "image"}, {"data", block.Data}, {"mimeType", block.MimeType}}, nil
	default:
		return nil, fmt.Errorf("unsupported content block %T", block)
	}
}

// RPCUsage preserves reported totals and the insertion order of streaming usage counters.
type RPCUsage struct {
	Input        int          `json:"input"`
	Output       int          `json:"output"`
	CacheRead    int          `json:"cacheRead"`
	CacheWrite   int          `json:"cacheWrite"`
	TotalTokens  int          `json:"totalTokens"`
	Cost         ai.UsageCost `json:"cost"`
	CacheWrite1h *int         `json:"cacheWrite1h,omitempty"`
	Reasoning    *int         `json:"reasoning,omitempty"`
}

func rpcUsage(usage *ai.Usage) *RPCUsage {
	if usage == nil {
		usage = &ai.Usage{}
	}
	value := &RPCUsage{
		Input: usage.Input, Output: usage.Output, CacheRead: usage.CacheRead,
		CacheWrite: usage.CacheWrite, TotalTokens: usage.TotalTokens, Cost: usage.Cost,
	}
	if usage.CacheWrite1h != nil {
		value.CacheWrite1h = new(*usage.CacheWrite1h)
	}
	if usage.Reasoning != nil {
		value.Reasoning = new(*usage.Reasoning)
	}
	return value
}
