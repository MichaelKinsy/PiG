package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// interactive-mode.ts handleEvent message_start renders every displayed custom message, whichever path delivered it. A message a
// before_agent_start handler returns reaches the run only as an agent message, so the transcript shows it only through this event
// (end-to-end against Pi: an extension returning {message:{customType, content, display:true}} shows "[note]" under the prompt).
func TestMessageStartRendersADisplayedCustomMessage(t *testing.T) {
	m, _, _ := newLifecycleMode(t)
	for _, tc := range []struct {
		name    string
		display bool
		want    int
	}{{"displayed", true, 1}, {"hidden", false, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			m.chatContainer.Clear()
			m.customMessageOrder = nil
			m.handleAgentEvent(agent.MessageStartEvent{Message: agent.AgentMessage{Custom: map[string]any{
				"role": agent.RoleCustom, "customType": "note", "content": "injected by before_agent_start", "display": tc.display, "timestamp": int64(1),
			}}})
			text := widthx.StripAnsi(strings.Join(m.chatContainer.Render(80), "\n"))
			if got := len(m.customMessageOrder); got != tc.want || strings.Contains(text, "injected by before_agent_start") != (tc.want == 1) {
				t.Fatalf("components %d, want %d; chat %q", got, tc.want, text)
			}
		})
	}
}
