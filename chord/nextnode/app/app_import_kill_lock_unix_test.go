//go:build unix

package app

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode/internal/lockseam"
	"github.com/MichaelKinsy/PiG/chord/nextnode/workers"
)

// seamLockDir is a test-only env var that tells the test child (runSeamChild) to activate lockseam.ForceSocketFile
// so Linux tests exercise the macOS/BSD socket-file lock path across processes. Production code never reads it.
const seamLockDir = "CHORD_SEAM_LOCK_DIR"

func init() {
	if dir := os.Getenv(seamLockDir); dir != "" {
		lockseam.ForceSocketFile(dir)
	}
}

// shortTestLockDir creates a short temporary directory under /tmp so Unix socket paths fit within sun_path (103 bytes).
func shortTestLockDir(t *testing.T) string {
	t.Helper()
	//portlint:allow hardtmp the lock socket path must stay within sun_path (103 bytes), which a scoped TMPDIR exceeds
	dir, err := os.MkdirTemp("/tmp", "chord-app-lock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// forceSocketFileLocks turns on socket-file locks for both this process and any test child spawned from it until the
// test finishes.
func forceSocketFileLocks(t *testing.T) string {
	t.Helper()
	dir := shortTestLockDir(t)
	restore := lockseam.ForceSocketFile(dir)
	t.Cleanup(restore)
	t.Setenv(seamLockDir, dir)
	return dir
}

// A hard kill in the middle of an import on a platform whose process locks are socket files (macOS and the BSDs, forced
// here via lockseam on Linux) leaves two stale socket files: the server lock and the address's worker lock.
// First, the server lock stays fail-closed (Pi 1.5.2 parity): a restart before removing the server lock file is refused.
// Second, once the dead server's lock file is removed (as an operator must do on macOS/BSD), the next start resolves
// the interrupted intent by clearing the stale address worker lock under the held server lock and undoing the import.
func TestAKillLeavesAStaleAddressLockFileThatTheNextStartClears(t *testing.T) {
	forceSocketFileLocks(t)

	dataDir, root, archive := seamServer(t, nil)
	killedAtTheSeamRaw(t, dataDir, root, archive, "import", "p2", "")

	// Step 1: server lock stays fail-closed (Pi 1.5.2 parity).
	options, _ := testOptions(t, func(o *AppOptions) {
		projectOptions(t)(o)
		o.DataDir, o.Root = dataDir, root
	})
	if app, err := StartServer(options, background); err == nil {
		closeAndWait(t, app)
		t.Fatal("StartServer succeeded with a stale server lock file; want fail-closed refusal")
	} else if !strings.Contains(err.Error(), "is a stale lock file") {
		t.Fatalf("StartServer with stale server lock file = %v, want stale lock file error", err)
	}

	// Step 2: operator removes the dead server's lock file; the next start must clear the stale address lock file
	// and undo the interrupted import.
	cleared, err := workers.ClearStaleLock(serverLockName(dataDir), background)
	if err != nil || !cleared {
		t.Fatalf("ClearStaleLock(serverLock) = %v, %v, want true, nil", cleared, err)
	}
	s := restarted(t, dataDir, root)
	reported := s.errors.all()
	if len(reported) != 1 || reported[0].Kind != AppErrorInterruptedDataChange || !strings.Contains(fmt.Sprint(reported[0].Error), "project|p2") {
		t.Fatalf("the start reported %v, want one interrupted_data_change naming project|p2", reported)
	}
	if strings.Contains(fmt.Sprint(reported[0].Error), "cannot be resolved") {
		t.Fatalf("the start failed to resolve the interrupted import: %v", reported[0].Error)
	}
	wantData(t, s, "p2", none, "after restart with stale address lock file cleared,")
	if names := intentNames(t, dataDir); len(names) != 0 {
		t.Errorf("the start left %v in server/imports", names)
	}
}
