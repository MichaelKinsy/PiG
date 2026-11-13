// SPDX-License-Identifier: MIT

package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
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

// releaseCellCacheClaims lets go of this process's claims on the directories directly under base, so that a test's temporary cache root can be
// removed: a claim keeps its .used file open for the life of the process, and Windows deletes no file that is open.
func releaseCellCacheClaims(t *testing.T, base string) {
	t.Helper()
	cellCacheClaimsMu.Lock()
	defer cellCacheClaimsMu.Unlock()
	kept := cellCacheClaims[:0]
	for _, claim := range cellCacheClaims {
		if filepath.Dir(filepath.Dir(claim.Path())) != filepath.Clean(base) {
			kept = append(kept, claim)
		} else if err := claim.Unlock(); err != nil {
			t.Errorf("release the claim on %s: %v", claim.Path(), err)
		}
	}
	cellCacheClaims = kept
}

// A cell cache is reused while the sources a build reads are unchanged and replaced when one changes; an edit to an installed dependency, which no build reads, keeps it.
func TestSharedCellCacheDirIsKeyedByTheContentsOfItsInputs(t *testing.T) {
	base := t.TempDir()
	t.Setenv("PIG_TEST_CELL_CACHE", base)
	t.Cleanup(func() { releaseCellCacheClaims(t, base) }) // runs before the temporary directory's removal
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

// cellCacheNamed is the name of a directory of package pkg in checkout number checkout (16 hex digits) with content number n.
func cellCacheNamed(pkg string, checkout, n int) string {
	return fmt.Sprintf("%s-%016x-%016x", pkg, checkout, n)
}

// Each checkout keeps the two most recently used directories of other contents beside its current one, whatever their age; a directory unused for 14 days
// goes whatever its checkout keeps; a directory some process holds survives; names that are not exactly <pkg>-<16 hex>-<16 hex> (another package's, one that
// merely starts with the package's) are not touched; and a held prune lock means another process is pruning.
func TestSharedCellCacheDirPrunesPerCheckout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot rename or delete a directory holding a file that is open for locking, so pruning there removes nothing (best effort)")
	}
	base := t.TempDir()
	t.Setenv("PIG_TEST_CELL_CACHE", base)
	sdk, fixtures := cellRoots(t)
	current, err := SharedCellCacheDir("pkg", sdk, fixtures)
	if err != nil {
		t.Fatal(err)
	}
	// The checkout that is running: its two newest others stay, the next goes.
	own := strings.TrimPrefix(filepath.Base(current), "pkg-")[:16]
	ownCheckout, _ := strconv.ParseUint(own, 16, 64)
	named := func(checkout uint64, n int) string { return fmt.Sprintf("pkg-%016x-%016x", checkout, n) }
	mine := []string{cellCacheAged(t, base, named(ownCheckout, 1), 24), cellCacheAged(t, base, named(ownCheckout, 2), 48), cellCacheAged(t, base, named(ownCheckout, 3), 72)}
	// Another checkout on the same cache root keeps its own two newest too, though the root holds more than two others.
	other := []string{cellCacheAged(t, base, named(0xabc, 1), 25), cellCacheAged(t, base, named(0xabc, 2), 49), cellCacheAged(t, base, named(0xabc, 3), 73)}
	// A checkout that went away: everything unused for 14 days goes, the two newest included.
	gone := []string{cellCacheAged(t, base, named(0xdef, 1), 24*15), cellCacheAged(t, base, named(0xdef, 2), 24*20)}
	held := cellCacheAged(t, base, named(ownCheckout, 4), 24*90)
	untouched := []string{cellCacheAged(t, base, "pkg-extra-"+fmt.Sprintf("%016x", 1), 24*400), cellCacheAged(t, base, fmt.Sprintf("pkgextra-%016x-%016x", 1, 1), 24*400), cellCacheAged(t, base, fmt.Sprintf("other-%016x-%016x", 1, 1), 24*400), cellCacheAged(t, base, fmt.Sprintf("pkg-%016X-%016x", 0xabcdef, 1), 24*400)}

	// Another process is using held: it holds the directory's .used shared, as claimCellCache does.
	claim := flock.New(filepath.Join(held, cellCacheUsed))
	if ok, err := claim.TryRLock(); err != nil || !ok {
		t.Fatalf("hold %s: %v", held, err)
	}
	defer func() { _ = claim.Unlock() }()

	pruneCellCaches(base, "pkg", current)
	for _, dir := range append([]string{mine[0], mine[1], other[0], other[1], held, current}, untouched...) {
		if !pathExists(dir) {
			t.Errorf("%s was removed", dir)
		}
	}
	for _, dir := range append([]string{mine[2], other[2]}, gone...) {
		if pathExists(dir) {
			t.Errorf("%s, beyond its checkout's two newest or unused for 14 days, and held by no process, was kept", dir)
		}
	}

	// A prune lock that is held means another process is pruning: nothing is removed.
	stale := cellCacheAged(t, base, named(0x123, 1), 24*100)
	lock := flock.New(filepath.Join(base, "pkg-prune.lock"))
	if locked, err := lock.TryLock(); err != nil || !locked {
		t.Fatalf("take the prune lock: %v", err)
	}
	pruneCellCaches(base, "pkg", current)
	if !pathExists(stale) {
		t.Errorf("%s was removed while the prune lock was held", stale)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	pruneCellCaches(base, "pkg", current)
	if pathExists(stale) {
		t.Errorf("%s was kept after the lock was released", stale)
	}
	entries, _ := os.ReadDir(base)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".removing") {
			t.Errorf("a removal left %s behind", entry.Name())
		}
	}
}

// A process that starts on a directory while a pruner is removing it waits for the pruner, then claims the directory the next use creates, never the file
// the pruner removed.
func TestClaimCellCacheWaitsForARemovalAndClaimsTheNewDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot rename or delete a directory holding a file that is open for locking, so pruning there removes nothing (best effort)")
	}
	dir := cellCacheAged(t, t.TempDir(), cellCacheNamed("pkg", 1, 1), 1)
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

// A process that starts on a directory at any moment of its removal either finds it held (the pruner leaves it) or ends up with a directory that exists and
// that it holds: never a claim on a file the removal went on to delete, and never an error. Removing in place broke this in about half the tries: the
// removal unlinked .used first, a process that arrived meanwhile created and locked a new .used, and the removal then deleted that and its directory.
func TestClaimCellCacheDuringAPruneOfTheSameDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("creates 20,000 files in each of 12 directories")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot rename or delete a directory holding a file that is open for locking, so pruning there removes nothing (best effort)")
	}
	for try := range 12 {
		base := t.TempDir()
		current := cellCacheAged(t, base, cellCacheNamed("pkg", 1, 1), 0)
		for i := 2; i <= 3; i++ {
			cellCacheAged(t, base, cellCacheNamed("pkg", 1, i), i)
		}
		victim := cellCacheAged(t, base, cellCacheNamed("pkg", 1, 4), 24*20)
		// Many entries make the removal long, so that a process can start in the middle of it; hard links to one file are the cheapest to create.
		seed := filepath.Join(base, "seed")
		if err := os.WriteFile(seed, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		for i := range 20000 {
			if err := os.Link(seed, filepath.Join(victim, fmt.Sprintf("f%d", i))); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		claimed := make(chan error, 1)
		wg.Go(func() { pruneCellCaches(base, "pkg", current) })
		wg.Go(func() {
			time.Sleep(time.Duration(try%10) * 15 * time.Millisecond)
			claimed <- claimCellCache(victim)
		})
		wg.Wait()
		if err := <-claimed; err != nil {
			t.Fatalf("try %d: the claim failed: %v", try, err)
		}
		held, err := os.Stat(filepath.Join(victim, cellCacheUsed))
		if err != nil {
			t.Fatalf("try %d: the claimed directory has no .used: %v", try, err)
		}
		// The claim is the process's: a prune now cannot take the directory.
		pruneCellCaches(base, "pkg", current)
		if again, err := os.Stat(filepath.Join(victim, cellCacheUsed)); err != nil || !os.SameFile(held, again) {
			t.Fatalf("try %d: a prune removed or replaced the directory a process holds: %v", try, err)
		}
	}
}
