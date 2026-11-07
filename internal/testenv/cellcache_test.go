// SPDX-License-Identifier: MIT

package testenv

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func cellRoots(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{"sdk/lib.go": "package sdk\n", "sdk/lib_test.go": "package sdk\n", "sdk/node_modules/dep.js": "x", "fixtures/main.py": "print(1)\n"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, "sdk"), filepath.Join(root, "fixtures")
}

// A cell cache is reused while the sources a build reads are unchanged and replaced when one changes; an edit to an installed dependency, which no build reads, keeps it.
func TestSharedCellCacheDirIsKeyedByTheContentsOfItsInputs(t *testing.T) {
	t.Setenv("PIG_TEST_CELL_CACHE", t.TempDir())
	sdk, fixtures := cellRoots(t)
	dir := func(pkg string) string {
		t.Helper()
		got, err := SharedCellCacheDir(pkg, sdk, fixtures)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	first := dir("pkg")
	if info, err := os.Stat(first); err != nil || !info.IsDir() {
		t.Fatalf("the cache directory %s was not created: %v", first, err)
	}
	if again := dir("pkg"); again != first {
		t.Fatalf("unchanged inputs moved the cache: %s then %s", first, again)
	}
	if other := dir("other"); other == first {
		t.Fatalf("two packages share %s", first)
	}
	for _, unread := range []string{"node_modules/dep.js"} {
		if err := os.WriteFile(filepath.Join(sdk, filepath.FromSlash(unread)), []byte("changed"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if same := dir("pkg"); same != first {
		t.Fatalf("an edit to an installed dependency moved the cache: %s then %s", first, same)
	}
	if err := os.WriteFile(filepath.Join(sdk, "lib_test.go"), []byte("package sdk // a test file writes fixture sources\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	afterTest := dir("pkg")
	if afterTest == first {
		t.Fatalf("an edited test file kept the cache %s", first)
	}
	if err := os.WriteFile(filepath.Join(sdk, "lib.go"), []byte("package sdk // edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := dir("pkg")
	if changed == afterTest {
		t.Fatalf("an edited SDK source kept the cache %s", first)
	}
	if err := os.WriteFile(filepath.Join(fixtures, "extra.py"), []byte("print(2)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if added := dir("pkg"); added == changed {
		t.Fatalf("a new fixture file kept the cache %s", changed)
	}
}

func cellCacheAged(t *testing.T, base, name string, hours int) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(filepath.Join(dir, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	used := filepath.Join(dir, cellCacheUsed)
	if err := os.WriteFile(used, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-time.Duration(hours) * time.Hour)
	if err := os.Chtimes(used, when, when); err != nil {
		t.Fatal(err)
	}
	return dir
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Beside the current directory only the two most recently used directories of other hashes survive, whatever their age; a directory some process holds
// survives too; another package's directories (a name that merely starts with the package's) are not touched; and a held prune lock means another
// process is pruning.
func TestSharedCellCacheDirPrunesDirectoriesOfOtherHashes(t *testing.T) {
	base := t.TempDir()
	t.Setenv("PIG_TEST_CELL_CACHE", base)
	sdk, fixtures := cellRoots(t)
	newest := []string{cellCacheAged(t, base, "pkg-aaaaaaaaaaaaaaa1", 24), cellCacheAged(t, base, "pkg-aaaaaaaaaaaaaaa2", 48)}
	gone := []string{cellCacheAged(t, base, "pkg-aaaaaaaaaaaaaaa3", 72), cellCacheAged(t, base, "pkg-aaaaaaaaaaaaaaa4", 24*30)}
	held := cellCacheAged(t, base, "pkg-aaaaaaaaaaaaaaa5", 24*90)
	otherPackages := []string{cellCacheAged(t, base, "pkg-extra-aaaaaaaaaaaaaaa6", 24*400), cellCacheAged(t, base, "pkgextra-aaaaaaaaaaaaaaa7", 24*400), cellCacheAged(t, base, "other-aaaaaaaaaaaaaaa8", 24*400)}

	// Another process is using held: it holds the directory's .used shared, as claimCellCache does.
	claim := flock.New(filepath.Join(held, cellCacheUsed))
	if ok, err := claim.TryRLock(); err != nil || !ok {
		t.Fatalf("hold %s: %v", held, err)
	}
	defer func() { _ = claim.Unlock() }()

	current, err := SharedCellCacheDir("pkg", sdk, fixtures)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range append([]string{newest[0], newest[1], held, current}, otherPackages...) {
		if !pathExists(dir) {
			t.Errorf("%s was removed", dir)
		}
	}
	for _, dir := range gone {
		if pathExists(dir) {
			t.Errorf("%s, beyond the two most recently used and held by no process, was kept", dir)
		}
	}

	// A prune lock that is held means another process is pruning: nothing is removed.
	stale := cellCacheAged(t, base, "pkg-aaaaaaaaaaaaaaa9", 100)
	lock := flock.New(filepath.Join(base, "pkg-prune.lock"))
	if locked, err := lock.TryLock(); err != nil || !locked {
		t.Fatalf("take the prune lock: %v", err)
	}
	if _, err := SharedCellCacheDir("pkg", sdk, fixtures); err != nil {
		t.Fatal(err)
	}
	if !pathExists(stale) {
		t.Errorf("%s was removed while the prune lock was held", stale)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := SharedCellCacheDir("pkg", sdk, fixtures); err != nil {
		t.Fatal(err)
	}
	if pathExists(stale) {
		t.Errorf("%s was kept after the lock was released", stale)
	}
}

// A process that starts on a directory while a pruner is removing it waits for the pruner, then claims the directory the next use creates, never the file
// the pruner removed.
func TestClaimCellCacheWaitsForARemovalAndClaimsTheNewDirectory(t *testing.T) {
	dir := cellCacheAged(t, t.TempDir(), "pkg-bbbbbbbbbbbbbbbb", 1)
	prune := flock.New(filepath.Join(dir, cellCacheUsed))
	if ok, err := prune.TryLock(); err != nil || !ok {
		t.Fatalf("take the pruner's lock: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- claimCellCache(dir) }()
	select {
	case err := <-done:
		t.Fatalf("the claim returned while a pruner held the directory: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := prune.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// The claim is on the file of the directory that exists now, so a pruner cannot take it.
	again := flock.New(filepath.Join(dir, cellCacheUsed))
	if ok, err := again.TryLock(); err != nil || ok {
		t.Fatalf("the directory was not claimed after the removal (locked=%v, err=%v)", ok, err)
	}
}
