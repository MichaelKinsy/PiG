package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// interactive-mode.ts addMessageToChat (user) shows getUserMessageText(message) without trimming when a session is rebuilt, as the live message_start does: an indented first line stays an indented code block and a message of
// only whitespace still adds its component (it renders no rows).
func TestResumedUserMessageShowsUntrimmedText(t *testing.T) {
	for _, text := range []string{"    code line\nnext", "word  \n", "  padded  "} {
		t.Run(text, func(t *testing.T) {
			dir := t.TempDir()
			sess, err := NewSessionManagerWithDir(dir, dir).Create("resume-user", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sess.AppendMessage(userMsg(text)); err != nil {
				t.Fatal(err)
			}
			m := resumeBashModeFromFile(t, dir, sess.Path())
			m.rebuildChatFromSession()
			want := tui.NewContainer()
			want.AddChild(m.newUserMessageBlock(text))
			if got, wantRows := m.chatContainer.Render(60), want.Render(60); !reflect.DeepEqual(got, wantRows) {
				t.Fatalf("resumed rows %q, want the rows of %q: %q", got, text, wantRows)
			}
		})
	}
}

// A whitespace-only user message is truthy in Pi's `if (textContent)` (interactive-mode.ts:3929), so rebuilding adds its Spacer and component: between two
// messages it leaves one more blank row than the two messages alone.
func TestResumedWhitespaceOnlyUserMessageKeepsItsSpacer(t *testing.T) {
	rows := func(texts ...string) []string {
		dir := t.TempDir()
		sess, err := NewSessionManagerWithDir(dir, dir).Create("resume-user", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range texts {
			if _, err := sess.AppendMessage(userMsg(text)); err != nil {
				t.Fatal(err)
			}
		}
		m := resumeBashModeFromFile(t, dir, sess.Path())
		m.rebuildChatFromSession()
		return m.chatContainer.Render(60)
	}
	with, without := rows("first", "   ", "last"), rows("first", "last")
	if len(with) != len(without)+1 {
		t.Fatalf("with a whitespace-only message between: %d rows %q; without it: %d rows %q; want one more (its Spacer)", len(with), with, len(without), without)
	}
}
