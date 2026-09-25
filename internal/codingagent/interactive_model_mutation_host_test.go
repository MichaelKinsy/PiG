package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// AgentSession.bindExtensions calls setModel/setThinkingLevel without Persist.
func TestExtensionHostModelMutationsPreserveDefaults(t *testing.T) {
	t.Run("agent", func(t *testing.T) { testExtensionHostModelMutations(t, false) })
	t.Run("session", func(t *testing.T) { testExtensionHostModelMutations(t, true) })
}

func testExtensionHostModelMutations(t *testing.T, boundSession bool) {
	t.Helper()
	dir := t.TempDir()
	sm := NewSettingsManager(t.TempDir(), dir)
	if err := sm.SetDefaultModelAndProvider("saved", "original"); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetDefaultThinkingLevel("low"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	model := &ai.Model{ID: "current", Provider: cycleTestProvider{id: "fixture"}, Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingHigh}}
	next := *model
	next.ID = "next"
	bridge := &captureUIBridge{}
	m := &InteractiveMode{
		opts:   InteractiveOptions{Model: model, SettingsManager: sm, SubprocessUIBridge: bridge, ModelBuilder: func(string) (*ai.Model, error) { return &next, nil }},
		agent:  agent.NewAgent(agent.AgentOptions{Model: model, ThinkingLevel: ai.ThinkingLow}),
		editor: tui.NewEditor(), thinkingLevel: "low", uiTaskCh: make(chan func(), 4),
	}
	if boundSession {
		m.opts.SessionHandle = &modelSwitchThinkingHandle{recordingCompactHandle: &recordingCompactHandle{agent: m.agent, inner: NewSession("host-mutations", dir)}, level: ai.ThinkingHigh}
	}
	detach := m.wireSubprocessHostCallbacks()
	defer detach()
	bridge.actions["setThinkingLevel"].(func(string))("max")
	if boundSession {
		entries := m.currentSession().Entries()
		if len(entries) != 1 || entries[0].Base.Type != "thinking_level_change" {
			t.Fatalf("host bypassed Session reasoning audit: %v", entries)
		}
	}
	if got := bridge.actions["getThinkingLevel"].(func() string)(); got != "high" {
		t.Errorf("host thinking getter = %s, want clamped high", got)
	}
	if got := m.agent.ThinkingLevel(); got != ai.ThinkingHigh {
		t.Errorf("agent thinking = %s, want high", got)
	}
	if ok, err := bridge.actions["setModel"].(func(context.Context, string) (bool, error))(t.Context(), "fixture/next"); err != nil || !ok {
		t.Fatalf("model switch = %v, %v", ok, err)
	}
	for len(m.uiTaskCh) > 0 {
		(<-m.uiTaskCh)()
	}
	after, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("extension host rewrote global defaults:\n%s", after)
	}
}
