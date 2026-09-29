// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package compaction

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/agent/src/harness/compaction/utils.ts.
type FileOperations struct {
	Read                                 map[string]struct{}
	Written                              map[string]struct{}
	Edited                               map[string]struct{}
	readOrder, writtenOrder, editedOrder []string
}

func CreateFileOps() FileOperations {
	return FileOperations{Read: map[string]struct{}{}, Written: map[string]struct{}{}, Edited: map[string]struct{}{}}
}
func (ops FileOperations) MarshalJSON() ([]byte, error) {
	sorted := func(set map[string]struct{}) []string {
		result := make([]string, 0, len(set))
		for key := range set {
			result = append(result, key)
		}
		slices.Sort(result)
		return result
	}
	return json.Marshal(struct {
		Read    []string `json:"read"`
		Written []string `json:"written"`
		Edited  []string `json:"edited"`
	}{sorted(ops.Read), sorted(ops.Written), sorted(ops.Edited)})
}
func ExtractFileOpsFromMessage(message agent.AgentMessage, ops *FileOperations) {
	if message.Assistant == nil {
		return
	}
	for _, block := range message.Assistant.Content {
		call, ok := block.(ai.ToolCall)
		if !ok {
			continue
		}
		path, _ := call.Arguments["path"].(string)
		if path == "" {
			continue
		}
		switch call.Name {
		case "read":
			ops.AddRead(path)
		case "write":
			ops.AddWritten(path)
		case "edit":
			ops.AddEdited(path)
		}
	}
}
func ComputeFileLists(ops FileOperations) (readFiles, modifiedFiles []string) {
	modified := maps.Clone(ops.Edited)
	if modified == nil {
		modified = map[string]struct{}{}
	}
	maps.Copy(modified, ops.Written)
	readFiles, modifiedFiles = []string{}, []string{}
	for path := range ops.Read {
		if _, ok := modified[path]; !ok {
			readFiles = append(readFiles, path)
		}
	}
	for path := range modified {
		modifiedFiles = append(modifiedFiles, path)
	}
	compare := func(a, b string) int { return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b))) }
	slices.SortFunc(readFiles, compare)
	slices.SortFunc(modifiedFiles, compare)
	return readFiles, modifiedFiles
}
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
func safeJSONStringify(value any) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "[unserializable]"
	}
	return strings.TrimSuffix(out.String(), "\n")
}
func truncateForSummary(text string, limit int) string {
	if stringLength(text) <= limit {
		return text
	}
	units := utf16.Encode([]rune(text))
	return string(utf16.Decode(units[:limit])) + fmt.Sprintf("\n\n[... %d more characters truncated]", len(units)-limit)
}

// SerializeConversation renders message text rather than continuing the conversation. Tool result text is limited to 2,000 UTF-16 units.
func SerializeConversation(messages []ai.Message) string {
	var parts []string
	for _, message := range messages {
		switch message := message.(type) {
		case ai.UserMessage:
			text := ""
			switch content := message.Content.(type) {
			case ai.UserText:
				text = string(content)
			case ai.UserContentBlocks:
				for _, block := range content {
					if block, ok := block.(ai.TextContent); ok {
						text += block.Text
					}
				}
			}
			if text != "" {
				parts = append(parts, "[User]: "+text)
			}
		case ai.AssistantMessage:
			var texts, thinking, calls []string
			hasText := false
			for _, block := range message.Content {
				switch block := block.(type) {
				case ai.TextContent:
					hasText = true
					texts = append(texts, block.Text)
				case ai.ThinkingContent:
					thinking = append(thinking, block.Thinking)
				case ai.ToolCall:
					var args []string
					for _, key := range slices.Sorted(maps.Keys(block.Arguments)) {
						args = append(args, key+"="+safeJSONStringify(block.Arguments[key]))
					}
					calls = append(calls, block.Name+"("+strings.Join(args, ", ")+")")
				}
			}
			if len(thinking) > 0 {
				parts = append(parts, "[Assistant thinking]: "+strings.Join(thinking, "\n"))
			}
			if hasText {
				parts = append(parts, "[Assistant]: "+strings.Join(texts, "\n"))
			}
			if len(calls) > 0 {
				parts = append(parts, "[Assistant tool calls]: "+strings.Join(calls, "; "))
			}
		case ai.ToolResultMessage:
			var text strings.Builder
			for _, block := range message.Content {
				if block, ok := block.(ai.TextContent); ok {
					text.WriteString(block.Text)
				}
			}
			if text.String() != "" {
				parts = append(parts, "[Tool result]: "+truncateForSummary(text.String(), 2000))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}
