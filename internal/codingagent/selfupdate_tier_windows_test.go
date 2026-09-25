//go:build windows

package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveSelfUpdateTier_WindowsStandaloneIsUnsupported proves a writable,
// receipted standalone pig.exe, which is the standalone tier elsewhere,
// resolves to the unsupported tier on Windows: a standalone Windows binary is
// not replaced in place (D39).
func TestResolveSelfUpdateTier_WindowsStandaloneIsUnsupported(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pig.exe")
	if err := os.WriteFile(exe, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeStandaloneTestReceipt(t, exe)
	prov, err := resolveSelfUpdateTierForExe(exe, fakeCmdRunner{outputs: map[string]string{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.Tier != tierUnsupported {
		t.Fatalf("tier = %s, want unsupported on Windows", prov.Tier)
	}
}
