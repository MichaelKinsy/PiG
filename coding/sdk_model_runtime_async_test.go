package coding

// pi: packages/coding-agent/src/core/sdk.ts

import (
	"os"
	"path/filepath"
	"testing"
)

// sdk.ts:198 awaits ModelRuntime.create({ authPath, modelsPath }) before createAgentSession goes on, so the runtime that selects the initial model (:207-257) has read the agent directory's models.json: a default model that only that file defines is the session's model, and nothing falls back.
func TestCreateAgentSessionSelectsItsModelFromTheRuntimeItCreated(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	agentDir := filepath.Join(root, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"models.json":   `{"providers":{"own-provider":{"baseUrl":"http://127.0.0.1:1/v1","apiKey":"fixture","api":"openai-completions","models":[{"id":"own-model","name":"Own model"}]}}}`,
		"settings.json": `{"defaultProvider":"own-provider","defaultModel":"own-model"}`,
	} {
		if err := os.WriteFile(filepath.Join(agentDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := CreateAgentSession(CreateAgentSessionOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.Session.Close() })
	if model := result.Session.Model(); model == nil || model.ID != "own-model" || result.ModelFallbackMessage != "" {
		t.Fatalf("model = %+v, fallback message %q; want own-model of the agent directory's models.json and no fallback", model, result.ModelFallbackMessage)
	}
}
