package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi builds the footer with the Session (interactive-mode.ts:659) and footer.ts:236 reads session.state.thinkingLevel on every render, so the first frame, drawn
// by ui.start() before any extension binds, already shows the Session's level. A footer that is told the level only after the TUI started painted
// "thinking off" until the bind (journey extension-completion-fast-enter, frame launch/startup).
func TestNewFooterShowsTheSessionThinkingLevelBeforeTheStartupBind(t *testing.T) {
	model := &ai.Model{ID: "reasoner", DisplayName: "reasoner", Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxThinking: ai.ThinkingLevelHigh}}
	handle := &recordingCompactHandle{agent: mustNewAgent(agent.AgentOptions{Model: model, ThinkingLevel: ai.ThinkingMedium})}
	m := &InteractiveMode{opts: InteractiveModeOptions{Model: model, SessionHandle: handle}}

	footer := m.newFooter()
	var line string
	for _, row := range footer.Render(120) {
		if strings.Contains(row, "reasoner") {
			line = row
		}
	}
	if !strings.Contains(line, "reasoner • medium") {
		t.Fatalf("footer model row of a new footer = %q, want the Session's thinking level medium", line)
	}
}
