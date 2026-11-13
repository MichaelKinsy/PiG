package app

import (
	"os"
	"path/filepath"
	"testing"
)

// Ports the contract of packages/chord/src/node/app/layout.ts: the layout's directories are made in turn, and the first that
// cannot be made is the error of the same call (the ones before it are made, the ones after it are not tried).

func TestTheLayoutOfADataDirectoryReturnsTheErrorOfTheFirstDirectoryItCannotMake(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A file where `plugins` belongs: server and workers come before it, logs after it.
	if err := os.WriteFile(filepath.Join(dataDir, "plugins"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateLayout(dataDir); err == nil {
		t.Fatal("CreateLayout made a layout over a file named plugins")
	}
	for _, made := range []string{"server", "workers"} {
		if _, err := os.Stat(filepath.Join(dataDir, made)); err != nil {
			t.Fatalf("%s before the failing step was not made: %v", made, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "logs")); err == nil {
		t.Fatal("logs after the failing step was made: the steps did not stop at the error")
	}
}
