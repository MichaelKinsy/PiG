//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestResolvePigBinClearsAmbientAgentDirectory(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pig")
	if err := os.WriteFile(binary, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_PARITY_PIG_BIN", binary)
	ambient := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", ambient)
	resolved := ResolvePigBin(t)
	var home string
	for _, entry := range resolved.Env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("invalid env %q", entry)
		}
		t.Setenv(key, value)
		if key == "PIG_HOME" {
			home = value
		}
	}
	if home == "" {
		t.Fatal("missing isolated home")
	}
	if got := codingagent.AgentDir(); got != filepath.Join(home, "agent") {
		t.Fatalf("agent directory = %q, want isolated home %q instead of ambient %q", got, home, ambient)
	}
	// Scenario overrides are appended after BinaryRef.Env and remain authoritative.
	fixture := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", fixture)
	if got := codingagent.AgentDir(); got != fixture {
		t.Fatalf("explicit scenario directory = %q, want %q", got, fixture)
	}
}
