package codingagent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// modelSwitchThinkingHandle applies a thinking level on SetModel, as the
// Session does for upstream _getThinkingLevelForModelSwitch.
type modelSwitchThinkingHandle struct {
	*recordingCompactHandle
	level ai.ThinkingLevel
}

func (h *modelSwitchThinkingHandle) SetModel(model *ai.Model, _ ...ModelMutationOptions) error {
	h.agent.SetModel(model)
	h.agent.SetThinkingLevel(h.level)
	return nil
}

// Upstream's footer and editor border read session.thinkingLevel after a
// model switch, so they show the level the switch applied. Interactive mode
// kept showing the previous level (CH-020 follow-up).
func TestModelSwitchShowsTheThinkingLevelTheSwitchApplied(t *testing.T) {
	current := &ai.Model{ID: "current", DisplayName: "current", Capabilities: ai.ModelCapabilities{ContextWindow: 8000, MaxThinking: ai.ThinkingHigh}}
	target := &ai.Model{ID: "target", DisplayName: "target", Provider: captureStreamOptionsProvider{}, Capabilities: ai.ModelCapabilities{ContextWindow: 8000, MaxThinking: ai.ThinkingHigh}}
	handle := &modelSwitchThinkingHandle{recordingCompactHandle: &recordingCompactHandle{}, level: ai.ThinkingHigh}
	dir := t.TempDir()
	sm := NewSettingsManager(t.TempDir(), dir)
	if err := sm.SetDefaultModelAndProvider("saved", "saved-model"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewInteractiveMode(InteractiveOptions{
		SettingsManager: sm,
		CWD:             t.TempDir(), Model: current, SessionHandle: handle,
		ModelBuilder: func(string) (*ai.Model, error) { return target, nil },
	})
	m.chatContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.editor = tui.NewEditor()
	m.statusLine = NewStatusLine(current, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: current, ThinkingLevel: ai.ThinkingLow})
	handle.agent = m.agent
	m.thinkingLevel = "low"
	m.editor.ThinkingLevel = "low"

	if err := m.buildSlashContext(context.Background()).SwitchModel("capture/target"); err != nil {
		t.Fatal(err)
	}
	if m.thinkingLevel != "high" || m.editor.ThinkingLevel != "high" {
		t.Fatalf("after the switch the UI shows thinking %q (editor %q), want the applied level high", m.thinkingLevel, m.editor.ThinkingLevel)
	}
	after, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("interactive model switch rewrote global defaults: %s", after)
	}
}
