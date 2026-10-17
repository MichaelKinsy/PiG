package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// interactive-mode.ts renderInitialMessages renders with populateHistory: true, so addMessageToChat adds each user message's text to the editor
// history (newest first for Up): after a resume, a tree navigation or a Session replacement the previous prompts can be recalled. rebuildChatFromMessages
// (/model, /settings and the like) and the transcript after a compaction pass no option and leave the history alone. Consecutive duplicates collapse
// in addToHistory.
func TestInitialRenderPopulatesTheEditorHistory(t *testing.T) {
	session := func(sess *Session) {
		for _, message := range []string{"one", "two", "two", "  three  "} {
			if _, err := sess.AppendMessage(userMsg(message)); err != nil {
				t.Fatal(err)
			}
			if _, err := sess.AppendMessage(assistantMsg("", ai.TextContent{Text: "ok"})); err != nil {
				t.Fatal(err)
			}
		}
	}
	recall := func(m *InteractiveMode) []string {
		m.editor.Render(40)
		var got []string
		for range 4 {
			m.editor.HandleInput("\x1b[A")
			got = append(got, m.editor.Text())
		}
		return got
	}

	initial := resumedModeWithOutputPad(t, 1, session)
	initial.editor = tui.NewEditor()
	initial.repaintInitialMessages()
	if got, want := recall(initial), []string{"three", "two", "one", "one"}; !slices.Equal(got, want) {
		t.Errorf("history after the initial render, Up x4 = %q, want %q", got, want)
	}

	// The startup path (-c, -r, --session) paints through renderInitialMessages, not the repaint.
	startup := resumedModeWithOutputPad(t, 1, session)
	startup.editor = tui.NewEditor()
	startup.opts.ResumePath = "resumed"
	startup.chatContainer.Clear()
	startup.renderInitialMessages()
	if got, want := recall(startup), []string{"three", "two", "one", "one"}; !slices.Equal(got, want) {
		t.Errorf("history after renderInitialMessages, Up x4 = %q, want %q", got, want)
	}

	rebuilt := resumedModeWithOutputPad(t, 1, session)
	rebuilt.editor = tui.NewEditor()
	rebuilt.rebuildChatFromSession()
	if got := recall(rebuilt); got[0] != "" {
		t.Errorf("rebuilding the transcript filled the editor history: Up = %q", got)
	}
}
