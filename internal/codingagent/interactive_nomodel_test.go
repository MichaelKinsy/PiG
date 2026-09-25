package codingagent

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Submitting a message with no model selected (user not logged in / no model
// chosen) must not panic on a nil *ai.Model. The agent rejects the turn with
// agent.ErrNoModelSelected, and handleSubmit surfaces the upstream
// formatNoModelSelectedMessage() guidance in chat rather than a bare error or a
// crash. Caller-boundary companion to
// TestAgent_Send_NilModel_ReturnsErrorNotPanic.
func TestHandleSubmit_NoModel_ShowsGuidanceNoPanic(t *testing.T) {
	var out bytes.Buffer
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: nil})
	m.editor = tui.NewEditor()
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(&out, 100, 30)
	m.tuiInst.Add(m.chatContainer)
	m.statusLine = NewStatusLine(nil, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: nil})
	m.keybindings = DefaultKeybindingsManager()
	m.runCtx = context.Background()
	m.abortCtx = context.Background()
	m.abortFn = func() {}

	m.handleSubmit(context.Background(), "hello")

	// handleSubmit runs the turn on a goroutine that posts UI work to uiTaskCh.
	// Drain and render on this single goroutine (keeping all UI mutation
	// single-threaded) until the guidance appears or we time out.
	deadline := time.After(2 * time.Second)
	for {
		drained := false
		for {
			select {
			case fn := <-m.uiTaskCh:
				fn()
				drained = true
			default:
			}
			if len(m.uiTaskCh) == 0 {
				break
			}
		}
		m.tuiInst.Render()
		if strings.Contains(out.String(), "No model selected") {
			return // fixed: guidance surfaced, no panic
		}
		select {
		case <-deadline:
			t.Fatalf("expected 'No model selected' guidance in chat; got:\n%s", out.String())
		case <-time.After(5 * time.Millisecond):
		}
		_ = drained
	}
}
