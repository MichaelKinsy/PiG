// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package compaction

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestSerializeConversationUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/compaction-serialization.test.ts:6
	t.Run("should truncate long tool results", func(t *testing.T) {
		got := SerializeConversation([]ai.Message{ai.ToolResultMessage{ToolCallID: "tc1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat("x", 5000)}}}})
		for _, want := range []string{"[Tool result]:", "[... 3000 more characters truncated]", strings.Repeat("x", 2000)} {
			if !strings.Contains(got, want) {
				t.Fatalf("result missing %q", want)
			}
		}
		if strings.Contains(got, strings.Repeat("x", 3000)) {
			t.Fatal("tool result was not truncated")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction-serialization.test.ts:28
	t.Run("should not truncate short tool results", func(t *testing.T) {
		short := strings.Repeat("x", 1500)
		got := SerializeConversation([]ai.Message{ai.ToolResultMessage{ToolCallID: "tc1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: short}}}})
		if got != "[Tool result]: "+short || strings.Contains(got, "truncated") {
			t.Fatalf("short result = %q", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/compaction-serialization.test.ts:47
	t.Run("should not truncate assistant or user messages", func(t *testing.T) {
		long := strings.Repeat("y", 5000)
		got := SerializeConversation([]ai.Message{ai.UserMessage{Content: ai.UserText(long)}, ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: long}}, API: "anthropic", Provider: "anthropic", Model: "test", StopReason: ai.StopReasonStop}})
		if strings.Contains(got, "truncated") || !strings.Contains(got, long) {
			t.Fatalf("conversation was truncated: %q", got)
		}
		if want := "[User]: " + long + "\n\n[Assistant]: " + long; got != want {
			t.Fatal("user or assistant content was altered")
		}
	})
}
