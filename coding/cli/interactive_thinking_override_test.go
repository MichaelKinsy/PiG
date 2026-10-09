//go:build !windows

package cli

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi main.ts forwards resolved CLI thinking into the Session before InteractiveMode starts. Exercise --thinking and :thinking on both HTTP APIs, including persisted resume, rather than only testing the resolver or footer leaf.
func TestInteractiveCLIThinkingOverrideReachesSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PIG_PARITY_PIG_BIN", buildPigBinaryForSignalTest(t))
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "python3", filepath.Join(root, "test/parity/testdata/interactive-thinking-wire.py"), "pig")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("interactive thinking provider/session probe: %v\n%s", err, output)
	}
}
