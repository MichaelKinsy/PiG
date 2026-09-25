package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ctrl+X (app.message.copy) confirms a copy differently by mode: fullscreen
// flashes "Copied!" on the alt-screen renderer, while inline appends the status
// line. Ports handleCopyCommand({flashConfirmation: true}): interactive-mode.ts
// only flashes when `this.ui instanceof TuiAltScreen`, otherwise showStatus.
func TestConfirmMessageCopied_FullscreenFlashesInsteadOfStatusLine(t *testing.T) {
	t.Run("inline appends the status line", func(t *testing.T) {
		m := &InteractiveMode{chatContainer: tui.NewContainer()}
		m.confirmMessageCopied(true)
		if m.chatContainer.ChildCount() == 0 {
			t.Fatal("inline copy confirmation should append a status line")
		}
		if m.lastStatusText == nil || !strings.Contains(m.lastStatusText.Content, "Copied last agent message to clipboard") {
			t.Fatalf("inline status = %v, want the copy-confirmation status line", m.lastStatusText)
		}
	})

	t.Run("fullscreen flashes and appends no status line", func(t *testing.T) {
		var buf synchronizedOutput
		m := &InteractiveMode{chatContainer: tui.NewContainer()}
		m.altScreen = tui.NewTuiAltScreenWithOutput(&buf, 30, 6, tui.TuiAltScreenOptions{})
		t.Cleanup(m.altScreen.Stop)
		m.altScreen.Add(tui.NewText("body"))
		m.altScreen.Start()

		m.confirmMessageCopied(true)

		if m.chatContainer.ChildCount() != 0 {
			t.Fatalf("fullscreen copy must not append a status line; children=%d", m.chatContainer.ChildCount())
		}
		buf.Reset()
		m.altScreen.Render()
		if !strings.Contains(buf.String(), "Copied!") {
			t.Fatalf("fullscreen copy should flash \"Copied!\" into the frame; frame=%q", buf.String())
		}
	})
}
