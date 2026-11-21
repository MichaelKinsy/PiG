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

	// Re-rendering a branch of prompts already in history moves them to the front in re-add order without duplicating them (Pi 06a20368a7, #12370).
	// History before re-render: p5, p4, p3, p2, p1. Re-rendering branch p1..p3 yields: p3, p2, p1, p5, p4 (not p3, p2, p1, p5, p4, p3, p2, p1).
	branch := resumedModeWithOutputPad(t, 1, func(sess *Session) {
		for _, message := range []string{"p1", "p2", "p3"} {
			if _, err := sess.AppendMessage(userMsg(message)); err != nil {
				t.Fatal(err)
			}
			if _, err := sess.AppendMessage(assistantMsg("", ai.TextContent{Text: "ok"})); err != nil {
				t.Fatal(err)
			}
		}
	})
	branch.editor = tui.NewEditor()
	for _, prompt := range []string{"p1", "p2", "p3", "p4", "p5"} {
		branch.editor.AddToHistory(prompt)
	}
	branch.repaintInitialMessages()
	branch.editor.Render(40)
	var gotBranch []string
	for range 6 {
		branch.editor.HandleInput("\x1b[A")
		gotBranch = append(gotBranch, branch.editor.Text())
	}
	if wantBranch := []string{"p3", "p2", "p1", "p5", "p4", "p4"}; !slices.Equal(gotBranch, wantBranch) {
		t.Errorf("history after branch re-render, Up x6 = %q, want %q", gotBranch, wantBranch)
	}
}
