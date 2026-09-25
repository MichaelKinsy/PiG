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
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// ─── File Operation Tracking ──────────────────────────────────────────────────

// FileOperations tracks files read/written/edited during a session segment.
// Mirrors upstream FileOperations (utils.ts).
type FileOperations struct {
	Read    map[string]struct{}
	Written map[string]struct{}
	Edited  map[string]struct{}
}

// MarshalJSON writes upstream's three Sets as sorted string arrays, the
// subprocess wire form of session_before_compact preparation.fileOps.
func (ops FileOperations) MarshalJSON() ([]byte, error) {
	sorted := func(set map[string]struct{}) []string {
		out := make([]string, 0, len(set))
		for path := range set {
			out = append(out, path)
		}
		slices.Sort(out)
		return out
	}
	return json.Marshal(struct {
		Read    []string `json:"read"`
		Written []string `json:"written"`
		Edited  []string `json:"edited"`
	}{sorted(ops.Read), sorted(ops.Written), sorted(ops.Edited)})
}

// NewFileOps returns a zero-valued FileOperations with all maps initialised.
func NewFileOps() FileOperations {
	return FileOperations{
		Read:    make(map[string]struct{}),
		Written: make(map[string]struct{}),
		Edited:  make(map[string]struct{}),
	}
}

// ExtractFileOpsFromMessage inspects assistant tool calls in msg and records
// file paths into ops. Non-assistant messages are a no-op.
//
// Mirrors upstream extractFileOpsFromMessage (utils.ts).
func ExtractFileOpsFromMessage(msg agent.AgentMessage, ops *FileOperations) {
	if msg.Assistant == nil {
		return
	}
	for _, blk := range msg.Assistant.Content {
		call, ok := blk.(ai.ToolCall)
		if !ok {
			continue
		}
		path, _ := call.Arguments["path"].(string)
		if path == "" {
			continue
		}
		switch call.Name {
		case "read":
			ops.Read[path] = struct{}{}
		case "write":
			ops.Written[path] = struct{}{}
		case "edit":
			ops.Edited[path] = struct{}{}
		}
	}
}

// ComputeFileLists derives two sorted lists from ops:
//   - modifiedFiles = union of Written ∪ Edited, sorted
//   - readFiles = Read ∖ modifiedFiles, sorted
//
// Mirrors upstream computeFileLists (utils.ts).
func ComputeFileLists(ops FileOperations) (readFiles, modifiedFiles []string) {
	modified := make(map[string]struct{}, len(ops.Written)+len(ops.Edited))
	for p := range ops.Written {
		modified[p] = struct{}{}
	}
	for p := range ops.Edited {
		modified[p] = struct{}{}
	}

	modifiedFiles = make([]string, 0, len(modified))
	for p := range modified {
		modifiedFiles = append(modifiedFiles, p)
	}
	slices.Sort(modifiedFiles)

	readFiles = make([]string, 0, len(ops.Read))
	for p := range ops.Read {
		if _, inMod := modified[p]; !inMod {
			readFiles = append(readFiles, p)
		}
	}
	slices.Sort(readFiles)

	return readFiles, modifiedFiles
}

// FormatFileOperations renders file lists as XML tags for inclusion in
// summarization prompts. Returns "" when both slices are empty.
//
// Output format (mirrors upstream formatFileOperations in utils.ts):
//
//	\n\n<read-files>\npath1\npath2\n</read-files>\n\n<modified-files>\npath3\n</modified-files>
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
