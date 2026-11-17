package cli

// pi: packages/coding-agent/src/cli/list-models.ts

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// list-models.ts:39 awaits modelRuntime.getAvailable(undefined, { signal }), a fresh availability pass (model-runtime.ts:490-506), and main.ts:951 does
// not catch its rejection: with a credential store that cannot be read the run warns of modelRuntime.getError() (list-models.ts:34-37), which holds the
// failure of the earlier passes, then ends with the pass's error and status 1, and prints no table. Pi 1.5.2 prints for this auth.json the warning
// below, the uncaught SyntaxError with the same message, and exits 1.
func TestListModelsEndsWithTheAvailabilityFailure(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), binary, "--offline", "--no-extensions", "--list-models", "gpt-4o-mini")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "HOME="+root, "PIG_HOME="+filepath.Join(root, "home"), "PIG_CODING_AGENT_DIR="+agentDir, "FORCE_COLOR=0", "OPENAI_API_KEY=fixture")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	exit, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exit.ExitCode() != 1 {
		t.Fatalf("run ended with %v, want exit status 1; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no table", stdout.String())
	}
	const message = "Expected property name or '}' in JSON at position 2 (line 1 column 3)"
	warning := "Warning: errors loading models.json:\nAvailability refresh: " + message + "\n"
	// Pi c8c456c85c6e (main.ts:917-918): the diagnostics listed before the models end with the no-models warning, then listModels reports the availability failure (probed on Pi, documentation roots cut).
	rest, found := strings.CutPrefix(cutDocsRoot(stderr.String()), noModelsWarningCut+warning)
	if !found {
		t.Fatalf("stderr = %q, want it to start with %q", cutDocsRoot(stderr.String()), noModelsWarningCut+warning)
	}
	if !strings.Contains(rest, message) {
		t.Fatalf("stderr after the warning = %q, want the pass's error %q", rest, message)
	}
}
