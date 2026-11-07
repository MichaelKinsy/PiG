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

// A directory of another hash is removed once nothing has used it for two weeks, except the four most recently used; the current directory, another
// package's and anything a held prune lock protects stay.
func TestSharedCellCacheDirPrunesOldDirectoriesOfOtherHashes(t *testing.T) {
	base := t.TempDir()
	t.Setenv("PIG_TEST_CELL_CACHE", base)
	sdk, fixtures := cellRoots(t)
	age := func(name string, days int) string {
		t.Helper()
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(filepath.Join(dir, "cache"), 0o755); err != nil {
			t.Fatal(err)
		}
		used := filepath.Join(dir, cellCacheUsed)
		if err := os.WriteFile(used, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
		if err := os.Chtimes(used, when, when); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	exists := func(dir string) bool {
		_, err := os.Stat(dir)
		return err == nil
	}
	// Seven directories of other hashes: the four most recently used stay however old; the three after them are older than the retention and go.
	var kept []string
	for i, days := range []int{20, 30, 40, 50} {
		kept = append(kept, age("pkg-kept"+string(rune('a'+i)), days))
	}
	gone := []string{age("pkg-old1", 60), age("pkg-old2", 90)}
	recent := age("pkg-recent", 1)
	other := age("other-ancient", 400)
	if _, err := SharedCellCacheDir("pkg", sdk, fixtures); err != nil {
		t.Fatal(err)
	}
	for _, dir := range gone {
		if exists(dir) {
			t.Errorf("%s, unused for more than the retention and beyond the newest four, was kept", dir)
		}
	}
	for _, dir := range append(kept[:3:3], recent, other) {
		if !exists(dir) {
			t.Errorf("%s was removed", dir)
		}
	}
	// The 50-day directory is the fifth newest and older than the retention.
	if exists(kept[3]) {
		t.Errorf("%s, the fifth newest and older than the retention, was kept", kept[3])
	}
	current, err := SharedCellCacheDir("pkg", sdk, fixtures)
	if err != nil || !exists(current) {
		t.Fatalf("the current directory %s: %v", current, err)
	}

	// A prune lock that is held means another process is pruning: nothing is removed.
	stale := age("pkg-stale", 100)
	lock := flock.New(filepath.Join(base, "pkg-prune.lock"))
	if locked, err := lock.TryLock(); err != nil || !locked {
		t.Fatalf("take the prune lock: %v", err)
	}
	if _, err := SharedCellCacheDir("pkg", sdk, fixtures); err != nil {
		t.Fatal(err)
	}
	if !exists(stale) {
		t.Errorf("%s was removed while the prune lock was held", stale)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := SharedCellCacheDir("pkg", sdk, fixtures); err != nil {
		t.Fatal(err)
	}
	if exists(stale) {
		t.Errorf("%s was kept after the lock was released", stale)
	}
}
