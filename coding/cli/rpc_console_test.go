package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// Pi modes/rpc/rpc-mode.ts starts its protocol loop without printing host tool inventories. Extension-authored console output remains visible on stderr; host setup is not extension console output.
func TestRPCDoesNotPrintHostToolInventories(t *testing.T) {
	home := t.TempDir()
	p := startRPCUIFixture(t, []string{
		"PIG_HOME=" + home,
		"PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"),
		"PIG_OFFLINE=1",
	}, "--no-extensions", "--no-session")
	p.closeAndWait("after command listing")
	if stderr := p.stderr.String(); strings.Contains(stderr, "pig --rpc: loaded") || strings.Contains(stderr, "pig --rpc: agent has") || strings.Contains(stderr, "pig --rpc: extension") {
		t.Fatalf("RPC setup leaked host debugging output: %s", stderr)
	}
}
