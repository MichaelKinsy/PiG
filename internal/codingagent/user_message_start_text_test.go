package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// interactive-mode.ts message_start (user): textContent = getUserMessageText(message) joins every text block with "" and the chat shows the untrimmed text, including a message of only whitespace
// (the component renders no rows for it but the spacer and component are added). A trimmed or first-block-only text renders different rows: four leading spaces make an indented code block in Markdown.
func TestUserMessageStartShowsTheJoinedUntrimmedText(t *testing.T) {
	image := ai.ImageContent{Data: "AAAA", MimeType: "image/png"}
	for _, tc := range []struct {
		name    string
		content ai.UserContentBlocks
		text    string
	}{
		{"indented first line", ai.UserContentBlocks{ai.TextContent{Text: "    code line\nnext"}}, "    code line\nnext"},
		{"every text block is joined", ai.UserContentBlocks{ai.TextContent{Text: "head "}, image, ai.TextContent{Text: "tail"}}, "head tail"},
		{"trailing spaces", ai.UserContentBlocks{ai.TextContent{Text: "word  \n"}}, "word  \n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newPendingDisplayHarness(t)
			m.chatContainer = tui.NewContainer()
			m.handleAgentEvent(agent.MessageStartEvent{Message: agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: tc.content}}})
			want := tui.NewContainer()
			want.AddChild(m.newUserMessageBlock(tc.text))
			if got, wantRows := m.chatContainer.Render(60), want.Render(60); !reflect.DeepEqual(got, wantRows) {
				t.Fatalf("chat rows %q, want the rows of %q: %q", got, tc.text, wantRows)
			}
			// A resumed or rebuilt transcript reaches the same addMessageToChat (renderSessionContext), so it shows the same rows.
			var content []any
			for _, block := range tc.content {
				switch b := block.(type) {
				case ai.TextContent:
					content = append(content, map[string]any{"type": "text", "text": b.Text})
				case ai.ImageContent:
					content = append(content, map[string]any{"type": "image", "data": b.Data, "mimeType": b.MimeType})
				}
			}
			m.chatContainer = tui.NewContainer()
			m.renderSessionEntryList([]SessionEntry{contextFixtureEntry(t, "u", "", "message", map[string]any{"message": map[string]any{"role": "user", "timestamp": 1, "content": content}})}, false)
			if got, wantRows := m.chatContainer.Render(60), want.Render(60); !reflect.DeepEqual(got, wantRows) {
				t.Fatalf("rebuilt chat rows %q, want the rows of %q: %q", got, tc.text, wantRows)
			}
		})
	}
}
