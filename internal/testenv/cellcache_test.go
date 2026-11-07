// SPDX-License-Identifier: MIT

package testenv

import (
	"os"
	"path/filepath"
	"testing"
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
