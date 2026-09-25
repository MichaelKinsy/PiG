package codingagent

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestAssistantThinkingUsesThinkingTransformContext(t *testing.T) {
	m := resumeThinkingMode(t, false, userMsg("question"), assistantMsg(""))
	block := m.newAssistantMessageBlock()
	const diagram = "```mermaid\nsequenceDiagram\n  A->>B: seq [1;1:1A]\n```"
	block.SetContent([]tui.AssistantSegment{{Thinking: true, Text: diagram}, {Text: diagram}})
	got := stripANSITest(strings.Join(block.Render(80), "\n"))
	if strings.Count(got, "Mermaid diagram not rendered") != 1 || !strings.Contains(got, "sequenceDiagram") {
		t.Fatalf("thinking must retain the code block while only text gets the Mermaid transform: %q", got)
	}
}

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
	want := []string{"", " Resumed bold and code", "", " - first", " - second", "", " answer"}
	if got := assistantLines(m.assistantBlocks[0]); !slices.Equal(got, want) {
		t.Fatalf("RRT-002: %q, want %q", got, want)
	}
}
