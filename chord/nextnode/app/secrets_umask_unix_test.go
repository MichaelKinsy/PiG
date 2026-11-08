//go:build unix

package app

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Ports packages/chord/test/node/app/secrets.test.ts, "keeps the mode 0600 under a permissive umask". The umask is the
// process's: the test sets it and restores it, and runs on its own (no t.Parallel).
func TestSecretFilesKeepTheirModeUnderAPermissiveUmask(t *testing.T) {
	dir := t.TempDir()
	previous := syscall.Umask(0)
	_, err := WriteSecret(filepath.Join(dir, "s"), "x", SecretCreate)
	syscall.Umask(previous)
	if err != nil {
		t.Fatalf("WriteSecret: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %#o, want 0600", mode)
	}
}
