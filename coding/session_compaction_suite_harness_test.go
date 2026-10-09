// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/ai"
)

// The suite's upstream faux provider estimates usage from each actual request (faux.ts:158-266). A zero-usage scripted response does not represent that fixture.
func newCompactionSuiteHarness(t *testing.T, options harnessOptions, responses ...scriptedResponse) *recoveryHarness {
	t.Helper()
	if options.contextWindow == 0 {
		options.contextWindow = 200000
	}
	h := newRecoveryHarness(t, options, responses...)
	cache := map[string]string{}
	h.provider.estimateUsage = func(request ai.TranscriptContext, options ai.StreamOptions, message *ai.AssistantMessage) {
		var parts []string
		for _, message := range request.Messages() {
			switch value := message.(type) {
			case ai.SystemMessage:
				fields := []string{}
				if text := ai.GetCurrentSystemPrompt([]ai.Message{value}); text != "" {
					fields = append(fields, text)
				}
				for _, tool := range value.ToolsRemoved {
					fields = append(fields, "tool-:"+suiteJSON(t, tool))
				}
				for _, tool := range value.ToolsAdded {
					fields = append(fields, "tool+:"+suiteJSON(t, tool))
				}
				parts = append(parts, "system:"+strings.Join(fields, "\n"))
			case ai.UserMessage:
				text := ""
				switch content := value.Content.(type) {
				case ai.UserText:
					text = string(content)
				case ai.UserContentBlocks:
					var blocks []string
					for _, block := range content {
						switch block := block.(type) {
						case ai.TextContent:
							blocks = append(blocks, block.Text)
						case ai.ImageContent:
							blocks = append(blocks, fmt.Sprintf("[image:%s:%d]", block.MimeType, len(block.Data)))
						}
					}
					text = strings.Join(blocks, "\n")
				}
				parts = append(parts, "user:"+text)
			case ai.AssistantMessage:
				parts = append(parts, "assistant:"+suiteAssistantText(t, value.Content))
			case ai.ToolResultMessage:
				fields := []string{value.ToolName}
				for _, block := range value.Content {
					switch block := block.(type) {
					case ai.TextContent:
						fields = append(fields, block.Text)
					case ai.ImageContent:
						fields = append(fields, fmt.Sprintf("[image:%s:%d]", block.MimeType, len(block.Data)))
					}
				}
				parts = append(parts, "toolResult:"+strings.Join(fields, "\n"))
			}
		}
		prompt := strings.Join(parts, "\n\n")
		tokens := func(text string) int { return (len(utf16.Encode([]rune(text))) + 3) / 4 }
		input := tokens(prompt)
		output := tokens(suiteAssistantText(t, message.Content))
		read, write := 0, 0
		if options.SessionID != "" && options.CacheRetention != "none" {
			if previous := cache[options.SessionID]; previous != "" {
				a, b := utf16.Encode([]rune(previous)), utf16.Encode([]rune(prompt))
				prefix := 0
				for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
					prefix++
				}
				read = (prefix + 3) / 4
				write = (len(b) - prefix + 3) / 4
				input = max(0, input-read)
			} else {
				write = input
			}
			cache[options.SessionID] = prompt
		}
		message.Usage = ai.Usage{Input: input, Output: output, CacheRead: read, CacheWrite: write, TotalTokens: input + output + read + write}
	}
	return h
}
func suiteJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func suiteAssistantText(t *testing.T, content []ai.AssistantContentBlock) string {
	t.Helper()
	var fields []string
	for _, block := range content {
		switch block := block.(type) {
		case ai.TextContent:
			fields = append(fields, block.Text)
		case ai.ThinkingContent:
			fields = append(fields, block.Thinking)
		case ai.ToolCall:
			fields = append(fields, block.Name+":"+suiteJSON(t, block.Arguments))
		}
	}
	return strings.Join(fields, "\n")
}
