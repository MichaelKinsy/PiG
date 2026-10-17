package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Queueing a steering or follow-up message while the agent streams only updates the pending-messages area
// (interactive-mode.ts handleSubmit/handleFollowUp: session.prompt(text, { streamingBehavior }) then
// updatePendingMessagesDisplay); Pi prints no status line. Only a message queued during compaction shows
// "Queued message for after compaction" (queueCompactionMessage). PiG also showed "Queued steering message"
// and "Queued follow-up message". Found by the queue journey (frame diff in the chat area).
func TestQueueingWhileStreamingShowsNoStatusLine(t *testing.T) {
	m, term := newTickRenderProbe(t, "regular")
	m.steerMessageWithImages("steer me", nil)
	m.followUpMessageWithImages("then this", nil)
	m.tuiInst.Render()
	screen := widthx.StripAnsi(term.take())
	for _, unwanted := range []string{"Queued steering message", "Queued follow-up message"} {
		if strings.Contains(screen, unwanted) {
			t.Fatalf("queueing printed the status line %q; Pi prints none:\n%s", unwanted, screen)
		}
	}
}
