//go:build !race

package subprocess

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An SDK source tree that a build reads in place (PIG_SDK_*_ROOT, or a checkout) costs the memoized cache key one stat per source file and one directory listing entry per file beside it. The bundles embed every source file gzipped, and the generator keeps each <name>.gz in the bundle's .gz directory, which the walk skips whole, not beside the source it stands for: 25bd4e0f34 wrote them beside the sources and every walk of the tree then listed, joined and filtered a second entry per source (Go 2122 -> 2687, Python 741 -> 932, Rust 741 -> 970 allocations per key). Before that commit one source cost 6.0 to 6.6 allocations; the budget is 7. The cost is turnCost's floor over samples, which a goroutine of an earlier test allocating during one sample cannot raise.
//
// The tree is measured as checked in: a copy without the directories a build or a run leaves in the working tree (an interpreter's __pycache__ is walked, and lists a file per module).
func TestSDKSourceTreeKeyAllocationsStayPerSource(t *testing.T) {
	const perSource = 7
	for _, c := range []struct{ buildType, dir string }{
		{"go", "../../../../extensions/sdk"},
		{"python", "../../../../extensions/sdk-py"},
		{"rust", "../../../../extensions/sdk-rs"},
	} {
		t.Run(c.buildType, func(t *testing.T) {
			dir := copyCheckedInTree(t, c.dir)
			sources := 0
			if err := walkDirSources(dir, sdkSourceExts(c.buildType), func(string, string) error { sources++; return nil }); err != nil {
				t.Fatal(err)
			}
			if _, err := sdkSourceDigest(dir, c.buildType); err != nil {
				t.Fatal(err)
			}
			allocs, _ := turnCost(func() {
				if _, err := sdkSourceDigest(dir, c.buildType); err != nil {
					t.Fatal(err)
				}
			})
			if budget := float64(perSource * sources); allocs > budget {
				t.Fatalf("a memoized key of the %s SDK tree = %v allocations for %d sources, budget %v (%d per source)", c.buildType, allocs, sources, budget, perSource)
			}
		})
	}
}

// copyCheckedInTree copies the files of src, with their directories, to a temporary directory, except the byproducts a working tree may hold: an interpreter's __pycache__, a Cargo target, node_modules, and every hidden directory but the bundles' .gz.
func copyCheckedInTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch name := entry.Name(); {
			case name == "__pycache__" || name == "target" || name == "node_modules":
				return filepath.SkipDir
			case strings.HasPrefix(name, ".") && name != ".gz" && rel != ".":
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}
