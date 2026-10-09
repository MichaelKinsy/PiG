package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// RRT-001: reuse the persisted-session vector and exact line oracle from
// review c22f27062. A toolCall terminates a thinking run even though the tool
// card itself is rendered separately after the assistant component.
func TestReviewResumeThinkingToolBoundary(t *testing.T) {
	m := resumeThinkingMode(t, true, userMsg("question"), assistantMsg("",
		ai.ThinkingContent{Thinking: "before tool"},
		ai.ToolCall{ID: "review-call", Name: "read", Arguments: ai.JsonObject{"path": "unused"}},
		ai.ThinkingContent{Thinking: "after tool"},
		ai.TextContent{Text: "answer"},
	))
	m.renderSessionEntries()
	want := []string{"", " Thinking...", "", " Thinking...", "", " answer"}
	if got := assistantLines(m.assistantBlocks[0]); !slices.Equal(got, want) {
		t.Fatalf("RRT-001: %q, want %q", got, want)
	}
}

// RRT-002: reuse the review's bold/code vector and include its two-item list.
// Visible thinking goes through Markdown with thinking-specific default style.
func TestReviewResumeThinkingMarkdown(t *testing.T) {
	m := resumeThinkingMode(t, false, userMsg("question"), assistantMsg("",
		ai.ThinkingContent{Thinking: "**Resumed bold** and `code`\n\n- first\n- second"},
		ai.TextContent{Text: "answer"},
	))
	m.renderSessionEntries()
	want := []string{"\x1b]133;A\x07", " Resumed bold and code", "", " - first", " - second", "", "\x1b]133;B\x07\x1b]133;C\x07 answer"}
	if got := assistantLines(m.assistantBlocks[0]); !slices.Equal(got, want) {
		t.Fatalf("RRT-002: %q, want %q", got, want)
	}
}
