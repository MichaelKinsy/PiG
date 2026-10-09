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
	"maps"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/compactiontypes"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// ─── File Operation Tracking ──────────────────────────────────────────────────

// FileOperations tracks files read/written/edited during a session segment. upstream: utils.ts FileOperations
type FileOperations = compactiontypes.FileOperations

// NewFileOps initializes the shared file-operation accumulator.
func NewFileOps() FileOperations {
	return FileOperations{Read: map[string]struct{}{}, Written: map[string]struct{}{}, Edited: map[string]struct{}{}}
}

// ExtractFileOpsFromMessage records read/write/edit tool calls in an assistant message, or the nested calls recorded on a tool result. Calls made from codemode scripts are recorded on the script's result.
//
// upstream: .upstream/current/packages/coding-agent/src/core/compaction/utils.ts (extractFileOpsFromMessage, addFileOp)
func ExtractFileOpsFromMessage(msg agent.AgentMessage, ops *FileOperations) {
	if result := msg.ToolResult; result != nil {
		if result.NestedCalls == nil {
			return
		}
		for _, call := range result.NestedCalls.Calls {
			addFileOp(call.Name, call.Arguments, ops)
		}
		return
	}
	if msg.Assistant == nil {
		return
	}
	for _, block := range msg.Assistant.Content {
		if call, ok := block.(ai.ToolCall); ok {
			addFileOp(call.Name, call.Arguments, ops)
		}
	}
}

func addFileOp(toolName string, arguments map[string]any, ops *FileOperations) {
	path, _ := arguments["path"].(string)
	if path == "" {
		return
	}
	switch toolName {
	case "read":
		ops.Read[path] = struct{}{}
	case "write":
		ops.Written[path] = struct{}{}
	case "edit":
		ops.Edited[path] = struct{}{}
	}
}

// ComputeFileLists returns read-only and modified paths in JavaScript sort order.
func ComputeFileLists(ops FileOperations) (readFiles, modifiedFiles []string) {
	modified := maps.Clone(ops.Edited)
	if modified == nil {
		modified = map[string]struct{}{}
	}
	maps.Copy(modified, ops.Written)
	readOnly := maps.Clone(ops.Read)
	for path := range modified {
		delete(readOnly, path)
	}
	return compactiontypes.SortedPaths(readOnly), compactiontypes.SortedPaths(modified)
}

// FormatFileOperations renders the shared summary metadata tags.
func FormatFileOperations(readFiles, modifiedFiles []string) string {
	var sections []string
	if len(readFiles) > 0 {
		sections = append(sections, "<read-files>\n"+strings.Join(readFiles, "\n")+"\n</read-files>")
	}
	if len(modifiedFiles) > 0 {
		sections = append(sections, "<modified-files>\n"+strings.Join(modifiedFiles, "\n")+"\n</modified-files>")
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(sections, "\n\n")
}

// ─── Message Serialization ────────────────────────────────────────────────────

// toolResultMaxChars is the maximum characters for a tool result in serialized
// summaries. Mirrors upstream TOOL_RESULT_MAX_CHARS = 2000 (utils.ts).
const toolResultMaxChars = 2000

// truncateForSummary truncates text to maxChars UTF-16 code units and appends a count marker, as truncateForSummary does
// with String.length and String.slice.
func truncateForSummary(text string, maxChars int) string {
	length := jsstring.Length(text)
	if length <= maxChars {
		return text
	}
	return jsstring.Slice(text, 0, maxChars) + "\n\n[... " + itoa(length-maxChars) + " more characters truncated]"
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
	var parts []string
	for _, message := range messages {
		switch message := message.(type) {
		case ai.UserMessage:
			var content string
			switch value := message.Content.(type) {
			case ai.UserText:
				content = string(value)
			case ai.UserContentBlocks:
				content = ai.ContentText(value, "")
			}
			if content != "" {
				parts = append(parts, "[User]: "+content)
			}
		case ai.AssistantMessage:
			var thinkingParts, toolCalls []string
			hasText := false
			for _, block := range message.Content {
				switch block := block.(type) {
				case ai.TextContent:
					hasText = true
				case ai.ThinkingContent:
					thinkingParts = append(thinkingParts, block.Thinking)
				case ai.ToolCall:
					members, _ := block.ArgumentMembers()
					arguments := make([]string, len(members))
					for i, member := range members {
						arguments[i] = member.Key + "=" + member.JSON
					}
					toolCalls = append(toolCalls, block.Name+"("+strings.Join(arguments, ", ")+")")
				}
			}
			if len(thinkingParts) > 0 {
				parts = append(parts, "[Assistant thinking]: "+strings.Join(thinkingParts, "\n"))
			}
			if hasText {
				parts = append(parts, "[Assistant]: "+ai.ContentText(message.Content))
			}
			if len(toolCalls) > 0 {
				parts = append(parts, "[Assistant tool calls]: "+strings.Join(toolCalls, "; "))
			}
		case ai.ToolResultMessage:
			if text := ai.ContentText(message.Content, ""); text != "" {
				parts = append(parts, "[Tool result]: "+truncateForSummary(text, toolResultMaxChars))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// ─── Summarization System Prompt ──────────────────────────────────────────────

// SummarizationSystemPrompt is the system prompt used when requesting a
// context summary from the LLM. Verbatim from upstream compaction.ts.
const SummarizationSystemPrompt = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`
