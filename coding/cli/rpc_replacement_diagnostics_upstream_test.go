package cli

// Ports packages/coding-agent/test/rpc-replacement-diagnostics.test.ts (Pi c47ad0432bb8, #12115, related to #11264).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeReplacementSettings(t *testing.T, agentDir string, enabledModels []string) {
	t.Helper()
	settings, err := json.Marshal(map[string]any{"defaultProvider": "fake", "defaultModel": "fake-model", "enabledModels": enabledModels})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The runtime factory runs again on every new_session, but only the startup diagnostics were reported, so a problem that first appeared on replacement went nowhere. Found while fixing #11264.
func TestRPCReportsADiagnosticThatFirstAppearsOnNewSessionOnStderrOnce(t *testing.T) {
	tempRoot := t.TempDir()
	agentDir := filepath.Join(tempRoot, "agent")
	projectDir := filepath.Join(tempRoot, "project")
	for _, dir := range []string{agentDir, projectDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":{"fake":{"baseUrl":"http://127.0.0.1:9/v1","api":"openai-completions","apiKey":"test","models":[{"id":"fake-model"}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeReplacementSettings(t, agentDir, []string{"fake/fake-model"})

	env := []string{"HOME=" + tempRoot, "PIG_HOME=" + filepath.Join(tempRoot, "pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_OFFLINE=1"}
	p := startRPCProcessAt(t, projectDir, env, "--no-session")
	var stdout strings.Builder
	newSession := func(id string) {
		t.Helper()
		p.send(`{"id":"` + id + `","type":"new_session"}`)
		p.await("the new_session response "+id, func(record rpcRecord) bool {
			encoded, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			stdout.Write(encoded)
			stdout.WriteByte('\n')
			return record["command"] == "new_session" && record["id"] == id
		})
	}

	newSession("1")
	writeReplacementSettings(t, agentDir, []string{"fake/fake-model", "github-copilot/missing-model"})
	newSession("2")
	newSession("3")
	p.closeAndWait("after the third new_session")

	stderr := p.stderr.String()
	if got := len(regexp.MustCompile(`Warning: No models match pattern "github-copilot/missing-model"`).FindAllString(stderr, -1)); got != 1 {
		t.Fatalf("the warning is on stderr %d times, want 1:\n%s", got, stderr)
	}
	if strings.Contains(stdout.String(), "No models match pattern") {
		t.Fatalf("stdout, which carries the RPC protocol, holds the diagnostic:\n%s", stdout.String())
	}
}
