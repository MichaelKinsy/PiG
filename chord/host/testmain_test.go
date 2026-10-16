package host

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain removes the Node loader's child entry when this run created it. The loader keeps
// chord-node-loader-<content hash>.mjs in the OS temporary directory for every later load, so these tests would otherwise
// leave it in the shared one (CI test-fast assert-clean-tmp). A scoped temporary directory does not fit: TestProcessLock
// requires its lock socket directly in the temporary directory, and a longer one exceeds the socket path limit. An entry that
// existed before the run belongs to someone else and stays.
func TestMain(m *testing.M) {
	sum := sha256.Sum256(nodeLoaderChild)
	entry := filepath.Join(os.TempDir(), "chord-node-loader-"+hex.EncodeToString(sum[:8])+".mjs")
	_, statErr := os.Stat(entry)
	existed := statErr == nil
	code := m.Run()
	if !existed {
		if err := os.Remove(entry); err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "remove "+entry+":", err)
			code = max(code, 2)
		}
	}
	os.Exit(code)
}
