package main

import (
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// rpcAgentEvent converts an internal agent event to Pi 0.86.1 JSON/RPC event
// shapes. Conversion errors are returned so writer paths can terminate loudly.
func rpcAgentEvent(event agent.AgentEvent) ([]any, error) {
	switch event := event.(type) {
	case agent.AgentStartEvent:
		return []any{map[string]any{"type": "agent_start"}}, nil
	case agent.AgentEndEvent:
		messages, err := rpcAgentMessages(event.Messages)
		if err != nil {
			return nil, err
		}
		return []any{map[string]any{"type": "agent_end", "messages": messages, "willRetry": event.WillRetry}}, nil
	case agent.AgentSettledEvent:
		return []any{map[string]any{"type": "agent_settled"}}, nil
	case agent.QueueUpdateEvent:
		return []any{RPCQueueUpdateEvent{Type: "queue_update", Steering: event.Steering, FollowUp: event.FollowUp}}, nil
	case agent.ThinkingLevelChangedEvent:
		return []any{RPCThinkingLevelChangedEvent{Type: "thinking_level_changed", Level: event.Level}}, nil
	case agent.CompactionStartEvent:
		return []any{map[string]any{"type": "compaction_start", "reason": event.Reason}}, nil
	case agent.CompactionEndEvent:
		out := map[string]any{"type": "compaction_end", "reason": event.Reason, "aborted": event.Aborted, "willRetry": event.WillRetry}
		if event.Summary != "" {
			result := map[string]any{
				"summary": event.Summary, "firstKeptEntryId": event.FirstKeptEntryID,
				"tokensBefore": event.TokensBefore, "estimatedTokensAfter": event.EstimatedTokensAfter,
			}
			if details := rpcOptionalCompactionDetails(event.Details); details != nil {
				result["details"] = details
			}
			if event.Usage != nil {
				result["usage"] = rpcUsage(event.Usage)
			}
			out["result"] = result
		}
		if event.ErrorMessage != "" {
			out["errorMessage"] = event.ErrorMessage
		}
		return []any{out}, nil
	case agent.AutoRetryStartEvent:
		return []any{map[string]any{"type": "auto_retry_start", "attempt": event.Attempt, "maxAttempts": event.MaxAttempts, "delayMs": event.DelayMs, "errorMessage": event.ErrorMessage}}, nil
	case agent.AutoRetryEndEvent:
		out := map[string]any{"type": "auto_retry_end", "success": event.Success, "attempt": event.Attempt}
		if event.FinalError != "" {
			out["finalError"] = event.FinalError
		}
		return []any{out}, nil
	case agent.SummarizationRetryScheduledEvent:
		return []any{map[string]any{"type": "summarization_retry_scheduled", "attempt": event.Attempt, "maxAttempts": event.MaxAttempts, "delayMs": event.DelayMs, "errorMessage": event.ErrorMessage}}, nil
	case agent.SummarizationRetryAttemptStartEvent:
		out := map[string]any{"type": "summarization_retry_attempt_start", "source": event.Source}
		if event.Source == "compaction" {
			out["reason"] = event.Reason
		}
		return []any{out}, nil
	case agent.SummarizationRetryFinishedEvent:
		return []any{map[string]any{"type": "summarization_retry_finished"}}, nil
	case agent.TurnStartEvent:
		return []any{map[string]any{"type": "turn_start"}}, nil
	case agent.TurnEndEvent:
		message, err := rpcAgentMessage(event.Message)
		if err != nil {
			return nil, err
		}
		toolResults, err := rpcToolResultMessages(event.ToolResults)
		if err != nil {
			return nil, err
		}
		return []any{map[string]any{"type": "turn_end", "message": message, "toolResults": toolResults}}, nil
	case agent.MessageStartEvent:
		message, err := rpcAgentMessage(event.Message)
		if err != nil {
			return nil, err
		}
		return []any{map[string]any{"type": "message_start", "message": message}}, nil
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
		return []any{map[string]any{"type": "message_end", "message": message}}, nil
	case agent.TimingEvent:
		return nil, nil
	case agent.EntryAppendedEvent:
		return []any{rpcSessionEntryAppendedEvent{Type: "entry_appended", Entry: event.Entry}}, nil
	case agent.ToolExecutionStartEvent:
		args := any(json.RawMessage(event.Args))
		if decoded, ok := tryDecodeJSON(string(event.Args)); ok {
			args = decoded
		}
		return []any{map[string]any{"type": "tool_execution_start", "toolCallId": event.ToolCallID, "toolName": event.ToolName, "args": args}}, nil
	case agent.ToolExecutionUpdateEvent:
		args := any(json.RawMessage(event.Args))
		if decoded, ok := tryDecodeJSON(string(event.Args)); ok {
			args = decoded
		}
		return []any{map[string]any{
			"type": "tool_execution_update", "toolCallId": event.ToolCallID,
			"toolName": event.ToolName, "args": args,
			"partialResult": rpcToolResultPayload(event.Content, nil, false, event.Details),
		}}, nil
	case agent.ToolExecutionEndEvent:
		return []any{map[string]any{
			"type": "tool_execution_end", "toolCallId": event.ToolCallID, "toolName": event.ToolName,
			"result":  rpcToolResultPayload(event.Result.Content, event.Result.Images, event.Result.IsError, event.Result.Details),
			"isError": event.Result.IsError,
		}}, nil
	default:
		return nil, fmt.Errorf("unsupported agent event %T", event)
	}
}

// rpcSessionEntryAppendedEvent carries a Session entry appended outside the
// agent loop, such as cache-warming usage, exactly as it was persisted.
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
	encoded, err := json.Marshal(event.AssistantMessageEvent)
	if err != nil {
		return nil, fmt.Errorf("marshal message_update assistant event: %w", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		return nil, fmt.Errorf("decode message_update assistant event: %w", err)
	}
	delete(wire, "partial")
	if start, ok := event.AssistantMessageEvent.(ai.ToolCallStartEvent); ok {
		if start.Partial == nil || start.ContentIndex < 0 || start.ContentIndex >= len(start.Partial.Content) {
			return nil, fmt.Errorf("toolcall_start content index %d is invalid", start.ContentIndex)
		}
		call, ok := start.Partial.Content[start.ContentIndex].(ai.ToolCall)
		if !ok {
			return nil, fmt.Errorf("toolcall_start content at index %d is not a tool call", start.ContentIndex)
		}
		wire["id"] = call.ID
		wire["toolName"] = call.Name
	}
	return map[string]any{
		"type": "message_update", "usage": rpcUsage(event.Message.Assistant.Usage),
		"assistantMessageEvent": wire,
	}, nil
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
		return map[string]any{"role": "user", "content": content, "timestamp": message.User.Timestamp}, nil
	case message.Assistant != nil:
		content, err := rpcAssistantContent(message.Assistant.Content)
		if err != nil {
			return nil, err
		}
		assistant := message.Assistant
		out := map[string]any{
			"role": "assistant", "content": content, "api": assistant.API,
			"provider": assistant.Provider, "model": assistant.ModelID,
			"usage": rpcUsage(assistant.Usage), "timestamp": assistant.Timestamp,
		}
		// A missing stop reason stays missing, as JSON.stringify drops an
		// undefined stopReason upstream.
		if assistant.StopReason != "" {
			out["stopReason"] = assistant.StopReason
		}
		if assistant.ResponseModel != "" {
			out["responseModel"] = assistant.ResponseModel
		}
		if assistant.ResponseID != "" {
			out["responseId"] = assistant.ResponseID
		}
		if assistant.ProviderThinkingLevel != "" {
			out["providerThinkingLevel"] = assistant.ProviderThinkingLevel
		}
		if len(assistant.Diagnostics) > 0 {
			out["diagnostics"] = assistant.Diagnostics
		}
		if assistant.Deferred != nil {
			out["deferred"] = assistant.Deferred
		}
		if assistant.ErrorMessage != "" {
			out["errorMessage"] = assistant.ErrorMessage
		}
		if assistant.RawStopReason != "" {
			out["rawStopReason"] = assistant.RawStopReason
		}
		if assistant.EndTurn != nil {
			out["endTurn"] = *assistant.EndTurn
		}
		return out, nil
	case message.ToolResult != nil:
		return rpcToolResultMessage(*message.ToolResult)
	case message.Custom != nil:
		return message.Custom, nil
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
	out := map[string]any{
		"role": "toolResult", "toolCallId": message.ToolCallID, "toolName": message.ToolName,
		"content": content, "isError": message.IsError, "timestamp": message.Timestamp,
	}
	if message.Details != nil {
		out["details"] = message.Details
	}
	if message.Usage != nil {
		out["usage"] = rpcUsage(message.Usage)
	}
	return out, nil
}

func rpcToolResultPayload(text string, images []ai.ImageContent, isError bool, details any) any {
	content := make([]any, 0, 1+len(images))
	content = append(content, map[string]any{"type": "text", "text": text})
	for _, image := range images {
		content = append(content, map[string]any{"type": "image", "data": image.Data, "mimeType": image.MimeType})
	}
	out := map[string]any{"content": content, "isError": isError}
	if details != nil {
		out["details"] = details
	}
	return out
}

func rpcUserContent(blocks []ai.UserContentBlock) ([]any, error) {
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
		out = append(out, wire)
	}
	return out, nil
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
	case ai.TextContent:
		out := map[string]any{"type": "text", "text": block.Text}
		if block.TextSignature != "" {
			out["textSignature"] = block.TextSignature
		}
		return out, nil
	case ai.ImageContent:
		return map[string]any{"type": "image", "data": block.Data, "mimeType": block.MimeType}, nil
	case ai.ToolCall:
		out := map[string]any{"type": "toolCall", "id": block.ID, "name": block.Name, "arguments": block.Arguments}
		if block.ThoughtSignature != "" {
			out["thoughtSignature"] = block.ThoughtSignature
		}
		if block.Namespace != "" {
			out["namespace"] = block.Namespace
		}
		return out, nil
	case ai.ThinkingContent:
		out := map[string]any{"type": "thinking", "thinking": block.Thinking}
		if block.ThinkingSignature != "" {
			out["thinkingSignature"] = block.ThinkingSignature
		}
		if block.Redacted {
			out["redacted"] = true
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported content block %T", block)
	}
}

func rpcUsage(usage *ai.Usage) map[string]any {
	if usage == nil {
		usage = &ai.Usage{}
	}
	out := map[string]any{
		"input": usage.Input, "output": usage.Output, "cacheRead": usage.CacheRead,
		"cacheWrite": usage.CacheWrite, "totalTokens": usage.TotalTokens,
		"cost": map[string]any{
			"input": usage.Cost.Input, "output": usage.Cost.Output,
			"cacheRead": usage.Cost.CacheRead, "cacheWrite": usage.Cost.CacheWrite,
			"total": usage.Cost.Total,
		},
	}
	if usage.CacheWrite1h != nil {
		out["cacheWrite1h"] = *usage.CacheWrite1h
	}
	if usage.Reasoning != nil {
		out["reasoning"] = *usage.Reasoning
	}
	return out
}

func tryDecodeJSON(text string) (any, bool) {
	if text == "" {
		return nil, false
	}
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return nil, false
	}
	return value, true
}
