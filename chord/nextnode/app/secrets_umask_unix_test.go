//go:build unix

package app

import (
	"path/filepath"
	"syscall"
	"testing"
)

// Ports the "keeps the mode 0600 under a permissive umask" test of packages/chord/test/node/app/secrets.test.ts (Pi main c23b9ec1).
// The umask is the process's; no test of this package runs in parallel with it.
func TestSecretFileKeepsTheMode0600UnderAPermissiveUmask(t *testing.T) {
	dir := t.TempDir()
	previous := syscall.Umask(0)
	_, err := WriteSecret(filepath.Join(dir, "s"), "x", SecretCreate)
	syscall.Umask(previous)
	if err != nil {
		t.Fatal(err)
	}
	if mode := modeOf(t, filepath.Join(dir, "s")); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
}
