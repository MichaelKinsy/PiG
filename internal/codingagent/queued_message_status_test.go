package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// interactive-mode.ts:3378-3383 (steer) and :4493-4498 (follow-up): a message submitted while the agent streams is shown in the pending
// area only. Pi shows no status line for it, so the chat does not gain "Queued steering message" or "Queued follow-up message".
func TestQueuedMessagesShowNoStatusLine(t *testing.T) {
	m, term := newTickRenderProbe(t, "regular")
	m.handleAgentEvent(agent.AgentStartEvent{})
	m.steerMessageWithImages("check the logs", nil)
	m.followUpMessageWithImages("then summarize", nil)
	m.tuiInst.Render()
	screen := widthx.StripAnsi(term.take())
	for _, want := range []string{"Steering: check the logs", "Follow-up: then summarize"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("pending area lacks %q:\n%s", want, screen)
		}
	}
	for _, unwanted := range []string{"Queued steering message", "Queued follow-up message"} {
		if strings.Contains(screen, unwanted) {
			t.Errorf("the chat shows %q, which Pi never shows:\n%s", unwanted, screen)
		}
	}
}
