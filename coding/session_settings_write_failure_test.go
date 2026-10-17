package coding

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// agent-session.ts setModel, setThinkingLevel, setAutoCompactionEnabled and setAutoRetryEnabled persist through SettingsManager setters,
// which queue the write and record a failure for the settings diagnostics (settings-manager.ts enqueueWrite, recordError). A global
// settings.json that cannot be written therefore fails none of them, and the in-memory change still applies.
func TestSessionSettingsWriteFailureDoesNotFailTheChange(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", t.TempDir())
	settings := icodingagent.NewSettingsManager(cwd, agentDir)
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: cwd, SettingsManager: settings})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	model := fakeModel()
	model.Capabilities.MaxThinking = ai.ThinkingLevelHigh
	sess, err := NewSession(services, SessionOptions{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	next := *model
	next.ID = "next"
	if err := sess.SetModel(&next, ModelMutationOptions{Persist: true}); err != nil {
		t.Fatalf("SetModel(persist) = %v, want nil: Pi records the settings write failure", err)
	}
	if sess.Model().ID != "next" {
		t.Fatalf("model = %s, want next", sess.Model().ID)
	}
	if err := sess.SetThinkingLevel(ai.ThinkingHigh, ModelMutationOptions{Persist: true}); err != nil {
		t.Fatalf("SetThinkingLevel(persist) = %v, want nil", err)
	}
	if got := sess.ThinkingLevel(); got != ai.ThinkingHigh {
		t.Fatalf("thinking level = %s, want high", got)
	}
	sess.SetAutoCompactionEnabled(false)
	sess.SetAutoRetryEnabled(false)
	if sess.AutoCompactionEnabled() || sess.AutoRetryEnabled() {
		t.Fatal("the in-memory settings did not change")
	}
}
