package agent

import (
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
)

// ConvertToLLM converts AgentMessages to the wire format expected by the AI provider.
// It calls NormalizeMessages first to filter errored/aborted assistant messages and
// insert synthetic tool results for orphaned tool calls.
func ConvertToLLM(msgs []AgentMessage, model *ai.Model) []ai.Message {
	msgs = NormalizeMessages(msgs, model)
	out := make([]ai.Message, 0, len(msgs))
	for _, m := range msgs {
		switch {
		case m.System != nil:
			out = append(out, *m.System)
		case m.User != nil:
			out = append(out, m.User.LLMMessage())
		case m.Assistant != nil:
			message := m.Assistant.LLMMessage()
			if len(message.Content) == 0 {
				message.Content = []ai.AssistantContentBlock{ai.TextContent{Text: ""}}
			}
			out = append(out, message)
		case m.ToolResult != nil:
			out = append(out, ai.ToolResultMessage{
				ToolCallID: m.ToolResult.ToolCallID, ToolName: m.ToolResult.ToolName,
				Content: m.ToolResult.Content, Details: m.ToolResult.Details,
				Usage: m.ToolResult.Usage, IsError: m.ToolResult.IsError,
				Timestamp: m.ToolResult.Timestamp,
			})
		case m.Custom != nil:
			// Mirrors upstream convertToLlm (coding-agent core/messages.ts):
			// every known custom role becomes a user message whose content is a
			// text-block array; "custom" block-array content passes through.
			role, _ := m.Custom["role"].(string)
			switch role {
			case RoleBashExecution, RoleBranchSummary, RoleCompactionSummary:
				if role == RoleBashExecution {
					if excluded, _ := m.Custom["excludeFromContext"].(bool); excluded {
						continue
					}
				}
				out = append(out, ai.UserMessage{
					Content:   ai.UserContentBlocks{ai.TextContent{Text: customMessageText(m.Custom)}},
					Timestamp: customTimestamp(m.Custom),
				})
			case RoleCustom:
				if content, ok := customMessageContent(m.Custom["content"]); ok {
					out = append(out, ai.UserMessage{Content: content, Timestamp: customTimestamp(m.Custom)})
				}
			}
		}
	}
	return out
}

// customMessageContent converts a "custom" message's content to user content
// blocks: a string becomes one text block and a block array passes through,
// whether built in memory or decoded from JSON. Missing content is an empty
// array, as session projection stores it.
func customMessageContent(content any) (ai.UserContentBlocks, bool) {
	switch content := content.(type) {
	case string:
		return ai.UserContentBlocks{ai.TextContent{Text: content}}, true
	case nil:
		return ai.UserContentBlocks{}, true
	case ai.UserContentBlocks:
		return content, true
	case []ai.UserContentBlock:
		return ai.UserContentBlocks(content), true
	}
	raw, err := json.Marshal(map[string]any{"role": RoleUser, "content": content})
	if err != nil {
		return nil, false
	}
	var decoded AgentMessage
	if json.Unmarshal(raw, &decoded) != nil || decoded.User == nil {
		return nil, false
	}
	switch content := decoded.User.Content.(type) {
	case ai.UserText:
		return ai.UserContentBlocks{ai.TextContent{Text: string(content)}}, true
	case ai.UserContentBlocks:
		return content, true
	default:
		return ai.UserContentBlocks{}, true
	}
}

// customTimestamp returns a custom message's timestamp in Unix milliseconds,
// as built in memory (int64) or decoded from JSON (float64), or 0.
func customTimestamp(m map[string]any) int64 {
	switch timestamp := m["timestamp"].(type) {
	case int64:
		return timestamp
	case int:
		return int64(timestamp)
	case float64:
		return int64(timestamp)
	case json.Number:
		value, _ := timestamp.Int64()
		return value
	}
	return 0
}

// LLMMessage retains the user content variant and normalizes an omitted Go content value to an empty array.
func (m *UserMessage) LLMMessage() ai.UserMessage {
	content := m.Content
	if content == nil {
		content = ai.UserContentBlocks{}
	}
	return ai.UserMessage{Content: content, Timestamp: m.Timestamp}
}

// Observe returns an independently owned observation of the message. A streaming shallow view keeps its copied top-level values while observing its retained nested objects.
func (m *AssistantMessage) Observe() *AssistantMessage {
	if m == nil {
		return nil
	}
	if m.streamView != nil {
		return agentAssistantMessage(m.streamView.Observe())
	}
	message := m.LLMMessage()
	owned := message.Observe()
	out := *m
	out.Content = owned.Content
	out.Diagnostics = owned.Diagnostics
	out.Deferred = owned.Deferred
	out.EndTurn = owned.EndTurn
	if m.Usage != nil {
		out.Usage = &owned.Usage
	}
	return &out
}

// refreshed returns a copy whose exported fields equal the retained stream state at this call and that keeps its stream link. Messages without a stream view are returned unchanged.
func (m *AssistantMessage) refreshed() *AssistantMessage {
	if m == nil || m.streamView == nil {
		return m
	}
	out := m.Observe()
	out.streamView = m.streamView
	return out
}

// ObserveUsage returns an owned usage snapshot without traversing message content or metadata. Streaming views observe their retained usage reference; ordinary absent usage stays absent.
func (m *AssistantMessage) ObserveUsage() *ai.Usage {
	if m == nil {
		return nil
	}
	if m.streamView != nil {
		usage := m.streamView.ObserveUsage()
		return &usage
	}
	if m.Usage == nil {
		return nil
	}
	usage := (&ai.AssistantMessage{Usage: *m.Usage}).ObserveUsage()
	return &usage
}

// LLMMessage returns the provider-facing form of an assistant message. Plain messages share content and diagnostics slices with m; retained streaming views produce an owned observation.
func (m *AssistantMessage) LLMMessage() ai.AssistantMessage {
	if m.streamView != nil {
		return *m.streamView.Observe()
	}
	usage := ai.Usage{}
	if m.Usage != nil {
		usage = *m.Usage
	}
	return ai.AssistantMessage{
		Content: m.Content, API: m.API, Provider: m.Provider,
		Model: m.ModelID, ResponseModel: m.ResponseModel,
		ResponseID: m.ResponseID, ProviderThinkingLevel: m.ProviderThinkingLevel,
		Diagnostics: m.Diagnostics,
		Usage:       usage, StopReason: m.StopReason, Deferred: m.Deferred,
		ErrorMessage: m.ErrorMessage, RawStopReason: m.RawStopReason,
		EndTurn: m.EndTurn, Timestamp: m.Timestamp,
	}
}

func convertToLLM(msgs []AgentMessage, model *ai.Model) []ai.Message {
	return ConvertToLLM(msgs, model)
}

// BranchSummaryContextText wraps a branch summary for LLM context.
// Mirrors upstream BRANCH_SUMMARY_PREFIX + summary + BRANCH_SUMMARY_SUFFIX
// from packages/coding-agent/src/core/messages.ts.
func BranchSummaryContextText(summary string) string {
	return "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n" + summary + "</summary>"
}

// CompactionSummaryContextText wraps a compaction summary for LLM context.
// Mirrors upstream COMPACTION_SUMMARY_PREFIX + summary + COMPACTION_SUMMARY_SUFFIX
// from packages/coding-agent/src/core/messages.ts.
func CompactionSummaryContextText(summary string) string {
	return "The conversation history before this point was compacted into the following summary:\n\n<summary>\n" + summary + "\n</summary>"
}

// customMessageText renders a custom message as a plain text string for LLM context.
// Mirrors upstream convertToLlm (messages.ts:143-188).
func customMessageText(m map[string]any) string {
	role, _ := m["role"].(string)
	switch role {
	case RoleBashExecution:
		return bashExecutionToText(m)
	case RoleBranchSummary:
		summary, _ := m["summary"].(string)
		return BranchSummaryContextText(summary)
	case RoleCompactionSummary:
		summary, _ := m["summary"].(string)
		return CompactionSummaryContextText(summary)
	}
	// Fallback: marshal to JSON
	b, _ := json.Marshal(m)
	return string(b)
}

// bashExecutionToText converts a BashExecutionMessage to user message text for LLM context.
// Mirrors upstream bashExecutionToText (messages.ts:90-108).
func bashExecutionToText(m map[string]any) string {
	cmd, _ := m["command"].(string)
	output, _ := m["output"].(string)
	cancelled, _ := m["cancelled"].(bool)
	truncated, _ := m["truncated"].(bool)
	fullOutputPath, _ := m["fullOutputPath"].(string)
	excludeFromContext, _ := m["excludeFromContext"].(bool)

	if excludeFromContext {
		return ""
	}

	text := "Ran `" + cmd + "`\n"
	if output != "" {
		text += "```\n" + output + "\n```"
	} else {
		text += "(no output)"
	}
	if cancelled {
		text += "\n\n(command cancelled)"
	} else if exitCode, ok := m["exitCode"].(float64); ok && exitCode != 0 {
		text += fmt.Sprintf("\n\nCommand exited with code %d", int(exitCode))
	}
	if truncated && fullOutputPath != "" {
		text += fmt.Sprintf("\n\n[Output truncated. Full output: %s]", fullOutputPath)
	}
	return text
}
