//go:build !windows

package codingagent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// trust-manager.ts:122-132 (writeTrustFile) rethrows Node's writeFileSync error, so a store whose trust.json cannot be written fails Set with the
// EACCES write error instead of reporting success, and the file keeps its earlier content.
func TestProjectTrustStoreSetReportsAWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through a read-only file")
	}
	agentDir := t.TempDir()
	trustPath := filepath.Join(agentDir, "trust.json")
	if err := os.WriteFile(trustPath, []byte("{}\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	yes := true
	err := NewProjectTrustStore(agentDir).Set(filepath.Join(t.TempDir(), "work"), &yes)
	var fsErr *nodeerrno.FSError
	if !errors.As(err, &fsErr) || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Set on a read-only trust.json returned %v, want the permission error of the write", err)
	}
	if got, readErr := os.ReadFile(trustPath); readErr != nil || string(got) != "{}\n" {
		t.Fatalf("trust.json after the failed write = %q, %v", got, readErr)
	}
}
