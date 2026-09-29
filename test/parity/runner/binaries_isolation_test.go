//go:build parity

package runner

import (
	"os"
	"strings"
	"testing"
)

// PIG_CODING_AGENT_DIR overrides PIG_HOME in the CLI. A parity process started
// by PiG must not inherit that agent's settings, credentials, or extensions.
func TestResolvePigBinIsolatesInheritedAgentDirectory(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_PARITY_PIG_BIN", executable)
	inherited := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", inherited)
	ref := ResolvePigBin(t)
	values := make(map[string]string)
	for _, entry := range ref.Env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	if got, exists := values["PIG_CODING_AGENT_DIR"]; !exists || got != "" {
		t.Fatalf("agent directory override = %q (present=%v), must clear the inherited value so PIG_HOME fixtures remain effective", got, exists)
	}
}
