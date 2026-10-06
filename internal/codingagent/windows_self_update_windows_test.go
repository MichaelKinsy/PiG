//go:build windows

package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// PR #161 CI: `pig update` in an npm install failed with "rename ...\pig.exe"
// right after the installation was written and started, while anti-virus held
// the new image open. A handle opened without delete sharing, as Go's os.Open
// and scanners open files, makes Windows refuse the rename until it closes.
// The quarantine waits the hold out, then moves and copies back the image.
func TestQuarantineWaitsOutAHandleHeldOnTheRunningImage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "node_modules")
	packageDir := filepath.Join(root, "pig")
	image := filepath.Join(packageDir, "pig.exe")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(image, []byte("running pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(image)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	waits := 0
	previous := quarantineRenameWait
	quarantineRenameWait = func(context.Context, time.Duration) error {
		waits++
		_ = held.Close()
		return nil
	}
	t.Cleanup(func() { quarantineRenameWait = previous })

	if err := quarantineNativeDependencies(t.Context(), packageDir, []string{image}); err != nil {
		t.Fatalf("quarantine with a held image: %v", err)
	}
	if waits != 1 {
		t.Fatalf("waits = %d, want one: the held image must block the first rename", waits)
	}
	quarantined, err := filepath.Glob(filepath.Join(root, quarantineDirName, "*", "pig.exe"))
	if err != nil || len(quarantined) != 1 {
		t.Fatalf("quarantined = %v (err=%v), want pig.exe", quarantined, err)
	}
	for _, path := range []string{image, quarantined[0]} {
		if got, err := os.ReadFile(path); err != nil || string(got) != "running pig" {
			t.Fatalf("%s = %q (err=%v), want the loaded image", path, got, err)
		}
	}
}
