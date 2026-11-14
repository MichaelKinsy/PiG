package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RPC's prompt command runs AgentSession.prompt, so Pi's one auth rule (agent-session.ts:2247-2259) answers it: a models.json provider without
// an apiKey is refused with `No API key found for <provider>.` (auth-guidance.ts:22-25) whatever its name, with OLLAMA_HOST set or not, and no
// turn starts. Pi 1.5.2 gives this response for both names (RUN 2026-11-13).
func TestRPCPromptRejectsAKeylessProviderOfAnyName(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"ollama", "mylocal"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			dir := filepath.Join(home, "agent")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			models := `{"providers":{"` + provider + `":{"baseUrl":"http://127.0.0.1:9/v1","api":"openai-completions","models":[{"id":"m1"}]}}}`
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0o600); err != nil {
				t.Fatal(err)
			}
			env := []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + dir, "OLLAMA_HOST=http://127.0.0.1:9/v1"}
			f := &upstreamRPC{p: startRPCProcessAt(t, t.TempDir(), env, "--offline", "--no-extensions", "--model", provider+"/m1", "--no-session")}
			f.p.send(`{"id":"k1","type":"prompt","message":"Hello"}`)
			f.response("k1")
			f.finish()
			r := assertPromptFailure(t, f.seen, "k1")
			if !strings.HasPrefix(r["error"].(string), "No API key found for "+provider+".\n\nUse /login to log into a provider via OAuth or API key. See:") {
				t.Fatal(r)
			}
			for _, record := range f.seen {
				if record["type"] == "agent_start" {
					t.Fatalf("a turn started for a provider without credentials: %v", f.seen)
				}
			}
		})
	}
}
