package codingagent

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// readOnlySession returns a persisted session whose file no longer accepts
// appends, so every later Append* call fails.
func readOnlySession(t *testing.T) *Session {
	t.Helper()
	session, err := tempSessionMgr(t).Create("read-only", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []agent.AgentMessage{mkUserMsg("hi"), mkAssistantMsg("hello")} {
		if _, err := session.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(session.Path(), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(session.Path(), 0o600) })
	return session
}

// Upstream setThinkingLevel appends the thinking_level_change, which throws on
// a write failure, and selectThinkingLevel shows it with showError instead of
// the status. Interactive mode discarded the error (GUARD-17).
func TestSelectThinkingLevelShowsAFailedSessionAppend(t *testing.T) {
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model, SessionHandle: &recordingCompactHandle{inner: readOnlySession(t)}})
	m.chatContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.editor = tui.NewEditor()
	m.statusLine = NewStatusLine(model, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})
	m.thinkingLevel = "off"

	m.selectThinkingLevel("high", false)

	chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(100), "\n"))
	if !strings.Contains(chat, "Error: ") {
		t.Fatalf("a failed thinking-level append was not shown:\n%s", chat)
	}
}

// Upstream's setLabel action calls appendLabelChange, which throws, so the
// extension's call fails; interactive mode returned success (GUARD-17).
func TestSetLabelHostActionReportsAFailedAppend(t *testing.T) {
	session, err := tempSessionMgr(t).Create("labels", "")
	if err != nil {
		t.Fatal(err)
	}
	bridge := subprocess.NewUIBridge(func() {})
	m := &InteractiveMode{opts: InteractiveOptions{SessionHandle: &recordingCompactHandle{inner: session}, SubprocessUIBridge: bridge}}
	detach := m.wireSubprocessHostCallbacks()
	defer detach()

	args, _ := json.Marshal(map[string]string{"entryId": "missing-entry", "label": "keep"})
	if _, err := bridge.HandleCall("labeler", &subprocess.CallPayload{Method: "setLabel", Args: args}); err == nil {
		t.Fatal("setLabel on an unknown entry succeeded; upstream appendLabelChange throws")
	}
}
