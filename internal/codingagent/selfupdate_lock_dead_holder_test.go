package codingagent

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gofrs/flock"
)

// pig divergence (D39): "A crash leaves an unlocked sidecar that the next update reuses and removes." The OS lock is
// released when its holder dies, the sidecar file stays; the next update acquires it, is the only holder, and removes
// the sidecar on release. Unlike Pi's proper-lockfile directory, a leftover file is never contention.
func TestStandaloneUpdateLockReusesAndRemovesASidecarLeftByADeadHolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("standalone in-place update is unsupported on Windows")
	}
	for _, tc := range []struct {
		name     string
		contents string
	}{
		{"empty", ""},
		{"with bytes", "12345\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pig.update.lock")
			// The dead holder: it locked the sidecar and ended without releasing, so the file stays and the kernel
			// dropped its lock.
			dead := flock.New(path)
			if locked, err := dead.TryLock(); err != nil || !locked {
				t.Fatalf("the dead holder's lock: locked=%t err=%v", locked, err)
			}
			if err := os.WriteFile(path, []byte(tc.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := dead.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("the leftover sidecar: %v", err)
			}

			release, err := acquireStandaloneUpdateLock(path)
			if err != nil {
				t.Fatalf("acquiring over the dead holder's sidecar: %v", err)
			}
			if _, err := acquireStandaloneUpdateLock(path); err == nil || err.Error() != lockBusyMessage {
				t.Fatalf("while held, a second updater got %v, want %q", err, lockBusyMessage)
			}
			if err := release(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("sidecar after release: %v, want not exist", err)
			}
		})
	}
}
