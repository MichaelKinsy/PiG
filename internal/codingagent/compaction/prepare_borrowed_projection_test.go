package compaction

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func borrowSystemEntry(id string, tools, properties int) map[string]any {
	members := map[string]any{}
	for i := range properties {
		members[fmt.Sprintf("field%d", i)] = map[string]any{"type": "string", "description": "an argument with a description of realistic length"}
	}
	declared := make([]any, tools)
	for i := range tools {
		declared[i] = map[string]any{"name": fmt.Sprintf("tool_%d", i), "description": "a tool", "parameters": map[string]any{"type": "object", "properties": members, "required": []any{"field0"}}}
	}
	return map[string]any{
		"type": "message", "id": id, "parentId": nil, "timestamp": time.Now().UTC().Format(time.RFC3339),
		"message": map[string]any{"role": "system", "content": "You are a coding agent.", "toolsAdded": declared, "timestamp": 1},
	}
}

func borrowToolCallEntry(id, text string) map[string]any {
	return map[string]any{
		"type": "message", "id": id, "parentId": nil, "timestamp": time.Now().UTC().Format(time.RFC3339),
		"message": map[string]any{
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "text", "text": text},
				map[string]any{"type": "toolCall", "id": "call_" + id, "name": "tool_0", "arguments": map[string]any{"path": "/tmp/" + id, "nested": map[string]any{"list": []any{"a", "b"}}}},
			},
		},
	}
}

// borrowSession is a long enough branch that PrepareCompaction summarizes its start: a system message declaring tools, then
// turns of a user message and an assistant message with a tool call.
func borrowSession(t *testing.T, tools, properties int) []codingagent.SessionEntry {
	t.Helper()
	entries := []codingagent.SessionEntry{mustSessionEntry(t, borrowSystemEntry("sys", tools, properties))}
	for i := range 8 {
		entries = append(entries,
			mustSessionEntry(t, userEntry(fmt.Sprintf("u%d", i), strings.Repeat("w", 300))),
			mustSessionEntry(t, borrowToolCallEntry(fmt.Sprintf("a%d", i), strings.Repeat("r", 300))),
		)
	}
	return entries
}

var borrowSettings = CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 400}

// TestPrepareCompactionHandsOutOwnedMessages: the projection PrepareCompaction reads holds the session entries' own messages, so
// every message it returns must be a copy. A receiver (an extension hook, the summarizer, the session file writer) that edits a
// text block, a tool call's arguments or a user message it was given leaves the entries, and so the next preparation, unchanged.
func TestPrepareCompactionHandsOutOwnedMessages(t *testing.T) {
	entries := borrowSession(t, 3, 4)
	want := PrepareCompaction(entries, borrowSettings)
	if want == nil || len(want.MessagesToSummarize) == 0 || len(want.TurnPrefixMessages) == 0 {
		t.Fatalf("fixture prepares nothing to summarize and no split turn: %#v", want)
	}
	for _, list := range [][]agent.AgentMessage{want.MessagesToSummarize, want.TurnPrefixMessages} {
		for _, message := range list {
			if message.System != nil {
				t.Fatal("a system message was handed out: its tool declarations would reach the receiver")
			}
		}
	}
	got := PrepareCompaction(entries, borrowSettings)
	for _, list := range [][]agent.AgentMessage{got.MessagesToSummarize, got.TurnPrefixMessages} {
		for i := range list {
			switch {
			case list[i].User != nil:
				blocks, _ := list[i].User.Content.(ai.UserContentBlocks)
				blocks[0] = ai.TextContent{Text: "edited by a receiver"}
			case list[i].Assistant != nil:
				for _, block := range list[i].Assistant.Content {
					switch block := block.(type) {
					case ai.TextContent:
						_ = block
					case *ai.ToolCall:
						block.Arguments["path"] = "edited"
						block.Arguments["nested"].(map[string]any)["list"].([]any)[0] = "edited"
					}
				}
				list[i].Assistant.Content[0] = ai.TextContent{Text: "edited by a receiver"}
			}
		}
	}
	again := PrepareCompaction(entries, borrowSettings)
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("a receiver's edits of the prepared messages reached the entries:\n got  %#v\n want %#v", again, want)
	}
}
