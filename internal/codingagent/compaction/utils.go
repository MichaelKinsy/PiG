// Package compaction provides shared utilities for context compaction and
// branch summarization.
//
// Mirrors upstream:
//
//	.upstream/current/packages/coding-agent/src/core/compaction/utils.ts
//
// All functions are pure: no LLM calls, no I/O, no side effects.
package compaction

import (
	"encoding/json"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	harnesscompaction "github.com/MichaelKinsy/PiG/agent/harness/compaction"
	"github.com/MichaelKinsy/PiG/ai"
)

// ─── File Operation Tracking ──────────────────────────────────────────────────

// FileOperations tracks files read/written/edited during a session segment.
// Mirrors upstream FileOperations (utils.ts).
type FileOperations = harnesscompaction.FileOperations

// NewFileOps initializes the shared file-operation accumulator.
func NewFileOps() FileOperations { return harnesscompaction.CreateFileOps() }

// ExtractFileOpsFromMessage records read/write/edit tool calls.
func ExtractFileOpsFromMessage(msg agent.AgentMessage, ops *FileOperations) {
	harnesscompaction.ExtractFileOpsFromMessage(msg, ops)
}

// ComputeFileLists returns read-only and modified paths in JavaScript sort order.
func ComputeFileLists(ops FileOperations) (readFiles, modifiedFiles []string) {
	return harnesscompaction.ComputeFileLists(ops)
}

// FormatFileOperations renders the shared summary metadata tags.
func FormatFileOperations(readFiles, modifiedFiles []string) string {
	return harnesscompaction.FormatFileOperations(readFiles, modifiedFiles)
}

// ─── Message Serialization ────────────────────────────────────────────────────

// toolResultMaxChars is the maximum characters for a tool result in serialized
// summaries. Mirrors upstream TOOL_RESULT_MAX_CHARS = 2000 (utils.ts).
const toolResultMaxChars = 2000

// truncateForSummary truncates text to maxChars and appends a count marker.
// Mirrors upstream truncateForSummary (utils.ts).
func truncateForSummary(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	truncated := len(text) - maxChars
	return text[:maxChars] + "\n\n[... " + itoa(truncated) + " more characters truncated]"
}

// itoa converts a non-negative integer to its decimal string representation
// without importing strconv or fmt.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	// reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

// SerializeConversation converts wire-format LLM messages to a plain-text
// representation suitable for summarization. Call convertToLLM first to
// normalise custom message types (bashExecution, compactionSummary, etc.)
// before passing the slice here.
//
// Roles handled:
//   - "user"      → [User]: <text>
//   - "assistant" → [Assistant thinking]: …  /  [Assistant]: …  /  [Assistant tool calls]: …
//   - "tool"      → [Tool result]: <text> (truncated to 2000 chars)
//
// Mirrors upstream serializeConversation (utils.ts).
func SerializeConversation(messages []ai.Message) string {
	var sb strings.Builder
	first := true
	push := func(text string) {
		if text == "" {
			return
		}
		if !first {
			sb.WriteString("\n\n")
		}
		sb.WriteString(text)
		first = false
	}

	for _, message := range messages {
		switch message := message.(type) {
		case ai.UserMessage:
			var content string
			switch value := message.Content.(type) {
			case ai.UserText:
				content = string(value)
			case ai.UserContentBlocks:
				var text strings.Builder
				for _, block := range value {
					if block, ok := block.(ai.TextContent); ok {
						text.WriteString(block.Text)
					}
				}
				content = text.String()
			}
			if content != "" {
				push("[User]: " + content)
			}
		case ai.AssistantMessage:
			var textParts, thinkingParts, toolCalls []string
			for _, block := range message.Content {
				switch block := block.(type) {
				case ai.TextContent:
					if block.Text != "" {
						textParts = append(textParts, block.Text)
					}
				case ai.ThinkingContent:
					if block.Thinking != "" {
						thinkingParts = append(thinkingParts, block.Thinking)
					}
				case ai.ToolCall:
					var call strings.Builder
					call.WriteString(block.Name)
					call.WriteByte('(')
					i := 0
					for key, value := range block.Arguments {
						if i > 0 {
							call.WriteString(", ")
						}
						encoded, _ := json.Marshal(value)
						call.WriteString(key)
						call.WriteByte('=')
						call.Write(encoded)
						i++
					}
					call.WriteByte(')')
					toolCalls = append(toolCalls, call.String())
				}
			}
			if len(thinkingParts) > 0 {
				push("[Assistant thinking]: " + strings.Join(thinkingParts, "\n"))
			}
			if len(textParts) > 0 {
				push("[Assistant]: " + strings.Join(textParts, "\n"))
			}
			if len(toolCalls) > 0 {
				push("[Assistant tool calls]: " + strings.Join(toolCalls, "; "))
			}
		case ai.ToolResultMessage:
			var text strings.Builder
			for _, block := range message.Content {
				if block, ok := block.(ai.TextContent); ok {
					text.WriteString(block.Text)
				}
			}
			if text.Len() > 0 {
				push("[Tool result]: " + truncateForSummary(text.String(), toolResultMaxChars))
			}
		}
	}
	return sb.String()
}

// ─── Summarization System Prompt ──────────────────────────────────────────────

// SummarizationSystemPrompt is the system prompt used when requesting a
// context summary from the LLM. Verbatim from upstream compaction.ts.
const SummarizationSystemPrompt = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`
