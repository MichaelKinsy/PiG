package host

// pig divergence (D178): the loader forks Node on a copy of loader_child.mjs. These tests pin where that copy lives:
// a directory of its own per load, so no other process's cleanup, and nothing else in the shared temporary
// directory, can take it from a load that is starting.

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// scopedTempDir points os.TempDir at a fresh directory for the test, so the test sees every entry the loader makes in
// it and removes none that belongs to another process.
func scopedTempDir(t *testing.T) string {
	t.Helper()
	scoped := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, scoped)
	}
	return scoped
}

func loaderEntries(t *testing.T, tmp string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(tmp, "chord-node-loader-*"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// loadGreeter loads one facet module from its own directory under dir through loader.
func loadGreeter(t *testing.T, env nodeLoaderEnv, loader *NodeLoaderImpl, dir string) {
	t.Helper()
	env.write(t, filepath.Join(dir, "greeting.ts"), "export const greeting = \"v1\";\n")
	env.write(t, filepath.Join(dir, "facet.ts"), env.facetSource())
	facets, err := loader.Load([]string{filepath.Join(dir, "facet.ts")})
	if err != nil {
		t.Error(err)
		return
	}
	if len(facets) != 1 {
		t.Errorf("facets = %d, want 1", len(facets))
	}
}

// The entry directory is private (no other user can swap the code Node runs) and holds only the child's source.
func TestNodeLoaderEntryIsPrivateAndRemovable(t *testing.T) {
	tmp := scopedTempDir(t)
	path, remove, err := nodeLoaderEntry()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(path)) != filepath.Clean(tmp) {
		t.Fatalf("entry %s is not in a directory of its own under %s", path, tmp)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("entry directory mode = %v, want 0700", info.Mode().Perm())
		}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, nodeLoaderChild) {
		t.Fatal("entry is not loader_child.mjs")
	}
	second, removeSecond, err := nodeLoaderEntry()
	if err != nil {
		t.Fatal(err)
	}
	if second == path {
		t.Fatal("two loads share one entry")
	}
	remove()
	removeSecond()
	if left := loaderEntries(t, tmp); len(left) != 0 {
		t.Fatalf("entries left after removal: %v", left)
	}
}

// A load leaves nothing behind in the shared temporary directory, however many run at once.
func TestNodeLoaderLeavesNothingInTheTempDirectory(t *testing.T) {
	env := requireNodeLoaderEnv(t)
	roots := make([]string, 4)
	for index := range roots {
		roots[index] = t.TempDir()
	}
	tmp := scopedTempDir(t)
	loader := env.loader()
	t.Cleanup(func() { _ = loader.Close() })
	var group sync.WaitGroup
	for _, root := range roots {
		group.Go(func() { loadGreeter(t, env, loader, root) })
	}
	group.Wait()
	if left := loaderEntries(t, tmp); len(left) != 0 {
		t.Fatalf("loads left entries in %s: %v", tmp, left)
	}
}

// Another process's cleanup of the shared temporary directory (a test binary's TestMain, a tmp reaper) cannot break a
// later load: whatever sits under the old shared name is not the child's entry.
func TestNodeLoaderSurvivesAnotherProcessesCleanupOfTheTempDirectory(t *testing.T) {
	env := requireNodeLoaderEnv(t)
	first, second := t.TempDir(), t.TempDir()
	tmp := scopedTempDir(t)
	loader := env.loader()
	t.Cleanup(func() { _ = loader.Close() })
	loadGreeter(t, env, loader, first)
	for _, entry := range loaderEntries(t, tmp) {
		if err := os.RemoveAll(entry); err != nil {
			t.Fatal(err)
		}
	}
	loadGreeter(t, env, loader, second)
}
