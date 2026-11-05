//go:build !pig_strip_mcp

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi 4d72d05bc createProviderTokenReader: `pig mcp` reads a provider's token from the agent directory's auth.json; a provider without credentials gives none.
func TestMcpProviderTokenReaderReadsTheAgentDirectoryCredentials(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(`{"radius":{"type":"api_key","key":"radius-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	read, closeRuntime := newMcpProviderTokenReader(agentDir)
	defer closeRuntime()
	if got := read(t.Context(), "radius"); got != "radius-token" {
		t.Fatalf("radius token = %q, want radius-token", got)
	}
	if got := read(t.Context(), "missing-provider"); got != "" {
		t.Fatalf("missing provider token = %q, want none", got)
	}
}
