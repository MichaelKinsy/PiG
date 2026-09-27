//go:build parity

package runner

import (
	"strings"
	"testing"
)

func TestTmuxPrefixDoesNotInheritAgentDirectories(t *testing.T) {
	prefix := tmuxEnvPrefix("")
	for _, key := range []string{"PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "PIG_HOME", "PI_HOME"} {
		if !strings.Contains(prefix, " "+key+" ") {
			t.Errorf("tmux server can retain ambient %s", key)
		}
	}
}

func TestHermeticEnvironmentDoesNotInheritAgentDirectories(t *testing.T) {
	keys := []string{"PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "PIG_HOME", "PI_HOME"}
	for _, key := range keys {
		t.Setenv(key, t.TempDir())
	}
	for _, entry := range hermeticEnviron() {
		key, _, _ := strings.Cut(entry, "=")
		for _, forbidden := range keys {
			if key == forbidden {
				t.Errorf("ambient %s can override a snapshotted fixture", key)
			}
		}
	}
}
