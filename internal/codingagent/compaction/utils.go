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
	"maps"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// ─── File Operation Tracking ──────────────────────────────────────────────────

// FileOperations tracks files read/written/edited during a session segment.
// Mirrors upstream FileOperations (utils.ts): three Sets of paths.
type FileOperations struct {
	Read    map[string]struct{}
	Written map[string]struct{}
	Edited  map[string]struct{}
}

// NewFileOps initializes the shared file-operation accumulator.
func NewFileOps() FileOperations {
	return FileOperations{Read: map[string]struct{}{}, Written: map[string]struct{}{}, Edited: map[string]struct{}{}}
}

// MarshalJSON carries each Set on the subprocess wire as a sorted string array.
func (ops FileOperations) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Read    []string `json:"read"`
		Written []string `json:"written"`
		Edited  []string `json:"edited"`
	}{sortedPaths(ops.Read), sortedPaths(ops.Written), sortedPaths(ops.Edited)})
}

// sortedPaths returns the Set's paths in JavaScript sort order (UTF-16 code units).
func sortedPaths(set map[string]struct{}) []string {
	paths := slices.Collect(maps.Keys(set))
	if paths == nil {
		paths = []string{}
	}
	slices.SortFunc(paths, func(a, b string) int { return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b))) })
	return paths
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
	return sortedPaths(readOnly), sortedPaths(modified)
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
