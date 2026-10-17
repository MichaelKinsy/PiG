package coding

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// agent-session-services.ts:147 preserves a supplied SettingsManager; sdk.ts:237 uses its defaults for the Session.
func TestServicesUsesInMemorySettingsThroughSession(t *testing.T) {
	sm := NewInMemorySettingsManager(Settings{DefaultThinkingLevel: "high"})
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), SettingsManager: sm})
	if err != nil {
		t.Fatal(err)
	}
	if services.SettingsManager() != sm {
		t.Fatal("Services replaced the supplied settings manager")
	}
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ok")}, StopReason: "stop"})})
	model := &ai.Model{ID: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingLevelHigh, ContextWindow: 128000}}
	session, err := NewSession(services, SessionOptions{NoSession: true, Model: model, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, session)
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	if session.ThinkingLevel() != "high" {
		t.Fatalf("thinking level=%q", session.ThinkingLevel())
	}
	if _, err := session.Send(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	if err := session.SetThinkingLevel("low", ModelMutationOptions{Persist: true}); err != nil {
		t.Fatal(err)
	}
	sm.Reload()
	if sm.GetDefaultThinkingLevel() != "low" {
		t.Fatalf("reload lost Session setter: %q", sm.GetDefaultThinkingLevel())
	}
	if _, err := os.Stat(filepath.Join(services.AgentDir(), "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("Session wrote a memory setting to disk: %v", err)
	}
}
