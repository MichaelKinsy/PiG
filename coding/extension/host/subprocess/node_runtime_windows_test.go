//go:build windows

package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

// A running Node cell reads its runtime files through hard links into the
// shared runtime entry. A handle held on one link must not stop another cell's
// scratch tree, which links the same files, from being published by a directory
// rename, so hard links stay safe on Windows. The control proves the handle
// really blocks a directory rename when it is inside the renamed tree.
func TestWindowsHardLinkedRuntimeTreeRenamesWhileAnotherLinkIsHeldOpen(t *testing.T) {
	cacheRoot := t.TempDir()
	running := filepath.Join(t.TempDir(), "running")
	if err := materializeNodeRuntime(t.Context(), cacheRoot, running); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(t.TempDir(), ".build-next")
	if err := materializeNodeRuntime(t.Context(), cacheRoot, scratch); err != nil {
		t.Fatal(err)
	}
	runningFile := filepath.Join(running, "cell.mjs")
	scratchFile := filepath.Join(scratch, "cell.mjs")
	if !sameFile(t, runningFile, scratchFile) {
		t.Skip("filesystem copied the runtime instead of hard-linking it")
	}

	held, err := os.Open(runningFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	published := filepath.Join(filepath.Dir(scratch), "published")
	if err := os.Rename(scratch, published); err != nil {
		t.Fatalf("renaming a tree whose files are held open through another hard link: %v", err)
	}

	heldInside, err := os.Open(filepath.Join(published, "cell.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = heldInside.Close() })
	if err := os.Rename(published, filepath.Join(filepath.Dir(scratch), "again")); err == nil {
		t.Fatal("control: renaming a tree with a file held open inside it succeeded, so the hold does not model a running cell")
	}
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	infoA, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	infoB, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(infoA, infoB)
}
