package coding

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// TestNewSessionUsesPerModelThinkingLevel mirrors upstream createAgentSession:
// a new session reads settings.modelThinkingLevels["provider/id"] ahead of
// defaultThinkingLevel (CFG-08).
func TestNewSessionUsesPerModelThinkingLevel(t *testing.T) {
	agentDir := t.TempDir()
	settings := `{"defaultThinkingLevel":"low","modelThinkingLevels":{"anthropic/claude-sonnet-4-5":"high"}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	svcs, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntime(RuntimeOptions{Services: svcs})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	for _, tc := range []struct {
		modelID string
		want    ai.ThinkingLevel
	}{
		{"claude-sonnet-4-5", ai.ThinkingHigh},
		{"claude-opus-4-8", ai.ThinkingLow},
	} {
		model := svcs.ModelRuntime().GetModel("anthropic", tc.modelID)
		if model == nil {
			t.Fatalf("no model anthropic/%s", tc.modelID)
		}
		sess, err := rt.New(SessionStartOptions{Model: model, NoSession: true})
		if err != nil {
			t.Fatal(err)
		}
		if got := sess.ThinkingLevel(); got != tc.want {
			t.Errorf("%s thinking = %q, want %q", tc.modelID, got, tc.want)
		}
		_ = sess.Close()
	}
}

// TestSetModelAppliesPerModelThinkingLevel mirrors upstream
// _getThinkingLevelForModelSwitch (CH-020): switching models uses the target
// model's modelThinkingLevels entry, else defaultThinkingLevel, else the
// current level.
func TestSetModelAppliesPerModelThinkingLevel(t *testing.T) {
	agentDir := t.TempDir()
	settings := `{"modelThinkingLevels":{"anthropic/claude-sonnet-4-5":"high"}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	svcs, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntime(RuntimeOptions{Services: svcs})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	opus := svcs.ModelRuntime().GetModel("anthropic", "claude-opus-4-8")
	sonnet := svcs.ModelRuntime().GetModel("anthropic", "claude-sonnet-4-5")
	sess, err := rt.New(SessionStartOptions{Model: opus, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if err := sess.SetThinkingLevel(ai.ThinkingLow); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetModel(sonnet); err != nil {
		t.Fatal(err)
	}
	if got := sess.ThinkingLevel(); got != ai.ThinkingHigh {
		t.Fatalf("thinking after switching to sonnet = %q, want the per-model high", got)
	}
	if err := svcs.SettingsManager().SetDefaultThinkingLevel("minimal"); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetModel(opus); err != nil {
		t.Fatal(err)
	}
	if got := sess.ThinkingLevel(); got != ai.ThinkingMinimal {
		t.Fatalf("thinking after switching to opus = %q, want defaultThinkingLevel minimal", got)
	}
}
