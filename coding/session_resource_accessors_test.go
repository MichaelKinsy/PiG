package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// TestSessionPromptTemplatesAreTheResourceLoaderPrompts: Pi's `get promptTemplates()` returns the resource loader's prompts
// (agent-session.ts:1658-1660), and prompt text expands against them (agent-session.ts:1995).
func TestSessionPromptTemplatesAreTheResourceLoaderPrompts(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{}, fauxReply("done", ai.StopReasonStop, 0))
	h.session.SetPromptResources([]PromptTemplate{{Name: "greet", Content: "expanded:$1"}}, nil)
	got := h.session.PromptTemplates()
	if len(got) != 1 || got[0].Name != "greet" {
		t.Fatalf("PromptTemplates() = %+v, want the loader's greet template", got)
	}
	if text := h.session.expandPromptText("/greet Bob"); text != "expanded:Bob" {
		t.Fatalf("prompt text expands against PromptTemplates(): got %q", text)
	}
}

// TestSessionSettingsManagerIsTheManagerItPersistsThrough: Pi's `readonly settingsManager` (agent-session.ts:371) is the
// manager the session writes its preferences to, as setSteeringMode does (agent-session.ts setSteeringMode).
func TestSessionSettingsManagerIsTheManagerItPersistsThrough(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{}, fauxReply("done", ai.StopReasonStop, 0))
	manager := h.session.SettingsManager()
	if manager == nil || manager != h.session.Services().SettingsManager() {
		t.Fatalf("SettingsManager() = %p, want the Services' manager %p", manager, h.session.Services().SettingsManager())
	}
	if err := h.session.SetSteeringMode(agent.QueueModeAll); err != nil {
		t.Fatal(err)
	}
	if got := manager.GetSteeringMode(); got != "all" {
		t.Fatalf("the steering mode persists through SettingsManager(): got %q", got)
	}
}
