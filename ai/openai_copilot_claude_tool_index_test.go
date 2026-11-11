package ai

import (
	"os"
	"strings"
	"testing"
)

// Copilot's Claude Chat Completions stream numbers tool_calls[].index by
// content block (1 after a thinking block, 2 after thinking and text), so the
// first call never has index 0, and argument deltas carry only the index. The
// fixtures are hand-written synthetic streams with that structure; the
// descending one proves arrival order rather than index order.
func TestOpenAICompletionsCopilotClaudeContentBlockIndexes(t *testing.T) {
	type call struct{ id, name, args string }
	read := `{"path":"src/a.go"}`
	grep := `{"pattern":"main","path":"src"}`
	for _, tc := range []struct {
		name    string
		fixture string
		want    []call
	}{
		{"thinking then index 1", "copilot_claude_idx1.sse", []call{
			{"toolu_01AAAA", "read", read},
		}},
		{"thinking, text, then index 2", "copilot_claude_idx2_text.sse", []call{
			{"toolu_01BBBB", "read", read},
		}},
		{"two calls at indexes 1 and 2", "copilot_claude_idx12.sse", []call{
			{"toolu_01CCCC", "read", read},
			{"toolu_01DDDD", "grep", grep},
		}},
		{"text then two calls at indexes 2 and 3", "copilot_claude_idx23_text.sse", []call{
			{"toolu_01EEEE", "read", read},
			{"toolu_01FFFF", "grep", grep},
		}},
		{"indexes arrive descending, 2 then 1", "copilot_claude_idx21_descending.sse", []call{
			{"toolu_01GGGG", "read", read},
			{"toolu_01HHHH", "grep", grep},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + tc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			var chunks []string
			for line := range strings.SplitSeq(string(data), "\n") {
				if payload, ok := strings.CutPrefix(line, "data: "); ok && payload != "[DONE]" {
					chunks = append(chunks, payload)
				}
			}
			schema := JsonObject{"type": "object"}
			tools := []ToolSchema{{Name: "read", Parameters: schema}, {Name: "grep", Parameters: schema}}
			_, response, _ := captureToolChoiceRequest(t, localToolChoiceModel("claude-synthetic", "Claude Synthetic"),
				Context{Messages: []Message{UserMessage{Content: UserText("go")}}, Tools: tools}, StreamOptions{}, chunks)
			if response.StopReason != StopReasonToolUse {
				t.Fatalf("stop reason = %q, want %q", response.StopReason, StopReasonToolUse)
			}
			var got []ToolCall
			for _, block := range response.Content {
				if tcall, ok := block.(ToolCall); ok {
					got = append(got, tcall)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d tool calls %+v, want %d", len(got), got, len(tc.want))
			}
			for i, want := range tc.want {
				if got[i].ID != want.id || got[i].Name != want.name {
					t.Fatalf("call %d = %s/%s, want %s/%s", i, got[i].ID, got[i].Name, want.id, want.name)
				}
				assertCompletionsJSON(t, got[i].Arguments, want.args)
			}
		})
	}
}
