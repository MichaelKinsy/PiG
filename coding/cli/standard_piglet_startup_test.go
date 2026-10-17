package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// PiG Standard selects its own piglogin Resource, which registers /sprite. Stock PiG's replaceable built-in pig-login
// registers /sprite too, and resource-loader.ts omitReplacedExtensions warns on every start when a selected extension
// replaces a built-in. Standard strips the built-in (D92 strip.extensions), so a Standard print run starts with its
// own piglogin loaded and writes no warning.
func TestStandardPigletStartsWithoutReplacedBuiltinWarning(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	standard := filepath.Join(testenv.ModuleRoot(t), "piglets", "standard", "pig-standard.yaml")
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	models := `{"providers":{"test-faux":{"baseUrl":"http://localhost:0","api":"test-faux","authHeader":false,"models":[{"id":"faux-1","name":"Test Faux","api":"test-faux","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(testbudget.Context(t), binary, "--piglet", standard, "--model", "test-faux/faux-1", "--no-session", "--print", "What is 20+22?")
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_STARTUP_TRACE=1")
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("pig --piglet standard: %v\nstderr:\n%s", err, stderr.String())
	}
	// Positive control: Standard's own /sprite owner loaded, so the run did reach the replaced-extension check.
	if !strings.Contains(stderr.String(), "extension.piglogin.handshake-done") {
		t.Fatalf("PiG Standard's piglogin extension did not load:\n%s", stderr.String())
	}
	if strings.TrimSpace(stdout.String()) == "" {
		t.Fatalf("print run wrote no answer; stderr:\n%s", stderr.String())
	}
	for line := range strings.SplitSeq(stderr.String(), "\n") {
		if strings.Contains(line, "Warning") || strings.Contains(line, "was not loaded") || strings.Contains(line, "builtin:pig-login") {
			t.Errorf("PiG Standard startup wrote %q", line)
		}
	}
}
