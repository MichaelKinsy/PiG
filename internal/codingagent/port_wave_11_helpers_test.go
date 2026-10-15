package codingagent_test

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func configMigrationAgentDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	agentDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	return agentDir
}

func writeConfigMigrationFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readConfigMigrationFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func runConfigMigrationsCaptured(t *testing.T, agentDir string) string {
	t.Helper()
	capture, err := os.CreateTemp(t.TempDir(), "migration-stdout-")
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = capture
	defer func() {
		os.Stdout = previous
		if err := capture.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, _, err := icodingagent.RunMigrations(agentDir, agentDir); err != nil {
		t.Fatalf("runMigrations must not throw: %v", err)
	}
	return string(readConfigMigrationFile(t, capture.Name()))
}

// upstream: packages/coding-agent/test/model-runtime-test-utils.ts:15-24. The production Services-owned ModelRuntime and extension registry facade construct and refresh offline; all file-backed state is temporary.
func newMigrationModelRegistry(t *testing.T, agentDir string) *coding.ModelRegistry {
	t.Helper()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: agentDir, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	services.Registry().SetModelsStore(ai.NewInMemoryModelsStore())
	return services.Registry()
}
