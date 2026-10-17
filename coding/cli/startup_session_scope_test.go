package cli

import (
	"path/filepath"
	"testing"
)

// Pi main.ts:822-828 passes buildSessionOptions hasExistingSession = sessionManager.buildSessionContext().messages.length > 0, and main.ts:500 applies the first scoped model unless that is true. A --session path that names no file starts an empty Session, so --models selects the startup model (Pi 1.2.0+b06c1f2c, same run against Pi's cli.js: kimi-coding/k3).
func TestRPCStartupScopeAppliesToSessionWithoutMessages(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	env := []string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_OFFLINE=1", "KIMI_API_KEY=x", "OPENAI_API_KEY=x"}
	p := startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--session", filepath.Join(home, "new-session.jsonl"), "--models", "kimi-coding/k3")
	p.send(`{"id":"state","type":"get_state"}`)
	p.await("startup model state", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "state" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		model, _ := data["model"].(map[string]any)
		if model["provider"] != "kimi-coding" || model["id"] != "k3" {
			t.Errorf("startup model = %v, want kimi-coding/k3", model)
		}
		return true
	})
	p.closeAndWait("after startup model state")
}
