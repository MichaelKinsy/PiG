package cli

// Ports packages/coding-agent/test/model-scope-session-replacement.test.ts (Pi bacd45b9c, #11973).

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// runNewSessionOverRPC starts RPC mode, replaces the session once, and returns everything the process wrote to stderr.
func runNewSessionOverRPC(t *testing.T, tempRoot, agentDir, projectDir string) string {
	t.Helper()
	env := []string{"HOME=" + tempRoot, "PIG_HOME=" + filepath.Join(tempRoot, "pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_OFFLINE=1"}
	p := startRPCProcessAt(t, projectDir, env, "--no-session")
	p.send(`{"id":"1","type":"new_session"}`)
	p.await("the new_session response", func(record rpcRecord) bool { return record["command"] == "new_session" })
	p.closeAndWait("after new_session")
	return p.stderr.String()
}

// #11264: the runtime factory runs again on /new, /resume and /fork. Pi printed the model scope warnings there with console.warn, which drew over the interactive TUI. They are startup diagnostics, reported once.
func TestModelScopeWarningsOnSessionReplacement(t *testing.T) {
	t.Run("reports unmatched enabledModels patterns once at startup, not again on new_session", func(t *testing.T) {
		tempRoot := t.TempDir()
		agentDir := filepath.Join(tempRoot, "agent")
		projectDir := filepath.Join(tempRoot, "project")
		for _, dir := range []string{agentDir, projectDir} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for name, content := range map[string]string{
			"models.json":   `{"providers":{"fake":{"baseUrl":"http://127.0.0.1:9/v1","api":"openai-completions","apiKey":"test","models":[{"id":"fake-model"}]}}}`,
			"settings.json": `{"defaultProvider":"fake","defaultModel":"fake-model","enabledModels":["fake/fake-model","github-copilot/missing-model"]}`,
		} {
			if err := os.WriteFile(filepath.Join(agentDir, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		stderr := runNewSessionOverRPC(t, tempRoot, agentDir, projectDir)

		if got := len(regexp.MustCompile(`No models match pattern "github-copilot/missing-model"`).FindAllString(stderr, -1)); got != 1 {
			t.Fatalf("the warning is on stderr %d times, want 1:\n%s", got, stderr)
		}
	})
}
