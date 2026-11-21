//go:build !pig_strip_node_extensions

package subprocess

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess/internal/noderuntimepack"
	"github.com/MichaelKinsy/PiG/internal/zstdblob"
)

// extractEveryFileMakesItsDirectory is extractNodeRuntime as it was before the directory set: one MkdirAll per file. The identity test compares the tree it writes with the tree extractNodeRuntime writes.
func extractEveryFileMakesItsDirectory(archive, dst string) error {
	return noderuntimepack.Extract(archive, func(name string, data []byte) error {
		target := filepath.Join(dst, filepath.FromSlash(strings.TrimPrefix(name, "runtime-node/")))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// listTree is every path under root with its type, mode, size, sha256 (files) and target (symlinks), sorted.
func listTree(t *testing.T, root string) []string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		line := fmt.Sprintf("%s\t%s\t%d", filepath.ToSlash(rel), info.Mode(), info.Size())
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			line += "\t-> " + target
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			line += "\t" + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(lines)
	return lines
}

func TestExtractNodeRuntimeWritesTheTreeOneMkdirPerFileDid(t *testing.T) {
	before := filepath.Join(t.TempDir(), "runtime")
	after := filepath.Join(t.TempDir(), "runtime")
	for _, dir := range []string{before, after} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := extractEveryFileMakesItsDirectory(nodeRuntimeArchive, before); err != nil {
		t.Fatal(err)
	}
	if err := extractNodeRuntime(after); err != nil {
		t.Fatal(err)
	}
	want, got := listTree(t, before), listTree(t, after)
	if len(want) < 100 {
		t.Fatalf("the embedded runtime listed %d paths; the comparison would prove nothing", len(want))
	}
	if !slices.Equal(want, got) {
		for i := range min(len(want), len(got)) {
			if want[i] != got[i] {
				t.Fatalf("trees differ at %d: before %q, after %q (%d and %d paths)", i, want[i], got[i], len(want), len(got))
			}
		}
		t.Fatalf("trees differ in length: before %d, after %d", len(want), len(got))
	}
}

func syntheticRuntimeArchive(t *testing.T, names ...string) string {
	t.Helper()
	files := make([]noderuntimepack.File, len(names))
	for i, name := range names {
		files[i] = noderuntimepack.File{Name: "runtime-node/" + name, Data: []byte("data of " + name)}
	}
	archive, err := noderuntimepack.Pack(files, func(raw []byte) ([]byte, error) { return zstdblob.EncodeLibrary(raw), nil })
	if err != nil {
		t.Fatal(err)
	}
	return string(archive)
}

// The archive carries files only (a tar entry per file, no directory entries), so every directory comes from the first file below it.
func TestExtractRuntimeArchiveMakesNestedDirectories(t *testing.T) {
	names := []string{"a.js", "shims/x/one.js", "shims/x/two.js", "shims/x/deep/er/three.js", "shims/y/four.js"}
	dst := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractRuntimeArchive(syntheticRuntimeArchive(t, names...), dst, os.MkdirAll); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		path := filepath.Join(dst, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		// Windows does not preserve POSIX group/other permission bits (files read back 0666).
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644&^umaskForTest(t) {
			t.Errorf("%s mode %v", name, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "data of "+name {
			t.Errorf("%s holds %q, %v", name, data, err)
		}
	}
	for _, dir := range []string{"shims", "shims/x", "shims/x/deep", "shims/x/deep/er", "shims/y"} {
		info, err := os.Lstat(filepath.Join(dst, filepath.FromSlash(dir)))
		// Windows does not preserve POSIX group/other permission bits (directories read back 0777).
		if err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o755&^umaskForTest(t)) {
			t.Errorf("%s: %v, %v", dir, info, err)
		}
	}
}

func umaskForTest(t *testing.T) fs.FileMode {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "probe")
	if err := os.Mkdir(probe, 0o777); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(probe)
	if err != nil {
		t.Fatal(err)
	}
	return 0o777 &^ info.Mode().Perm()
}

// An error from the directory step is the extraction's error, as before.
func TestExtractRuntimeArchiveReturnsMkdirError(t *testing.T) {
	dst := t.TempDir()
	want := fmt.Errorf("no directory")
	err := extractRuntimeArchive(syntheticRuntimeArchive(t, "a/b.js"), dst, func(string, os.FileMode) error { return want })
	if err == nil || !strings.Contains(err.Error(), "no directory") {
		t.Fatalf("got %v", err)
	}
}

// Each directory is made once however many files it holds: MkdirAll calls equal the distinct directories of the files.
func TestExtractRuntimeArchiveMakesEachDirectoryOnce(t *testing.T) {
	names := []string{"a.js", "b.js", "shims/x/one.js", "shims/x/two.js", "shims/x/three.js", "shims/y/four.js", "shims/y/deep/five.js", "shims/y/deep/six.js"}
	distinct := map[string]bool{}
	for _, name := range names {
		distinct[filepath.Dir(name)] = true
	}
	var mu sync.Mutex
	var made []string
	var calls atomic.Int64
	dst := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	err := extractRuntimeArchive(syntheticRuntimeArchive(t, names...), dst, func(dir string, mode os.FileMode) error {
		calls.Add(1)
		mu.Lock()
		made = append(made, dir)
		mu.Unlock()
		return os.MkdirAll(dir, mode)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := int(calls.Load()); got != len(distinct) {
		t.Fatalf("MkdirAll ran %d times for %d files in %d directories: %v", got, len(names), len(distinct), made)
	}
}

// The embedded runtime: one MkdirAll per distinct directory that holds a file.
func TestExtractNodeRuntimeMakesEachDirectoryOnce(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	if err := extractRuntimeArchive(nodeRuntimeArchive, dst, func(dir string, mode os.FileMode) error {
		calls.Add(1)
		return os.MkdirAll(dir, mode)
	}); err != nil {
		t.Fatal(err)
	}
	distinct := map[string]bool{}
	files := 0
	err := filepath.WalkDir(dst, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files++
			distinct[filepath.Dir(path)] = true
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := int(calls.Load()); got != len(distinct) {
		t.Fatalf("MkdirAll ran %d times for %d files in %d directories", got, files, len(distinct))
	}
}
