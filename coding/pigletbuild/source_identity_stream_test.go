package pigletbuild

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A source tree is thousands of small files and a few large ones. Hashing it for a build lock must not allocate per
// file: ReadFile holds every file's bytes, and a fresh io.Copy buffer costs 32 KiB per file whatever its size.
const (
	treeSmallFiles = 3000
	treeSmallSize  = 1 << 10
	treeLargeSize  = 8 << 20
	// The tree is about 11 MiB; per-file allocation shows as at least 10 MiB (ReadFile) or 90 MiB (a buffer per file).
	// Streaming still allocates per file for the path list, Lstat and Open (about 2.7-2.9 MB on linux/amd64, roughly 900 B
	// a file), and that share differs by platform and Go release, so the budget sits between it and the 10 MiB floor.
	treeAllocBudget = 6 << 20
)

// writeSourceTree writes the tree and returns the relative paths it holds, slash-separated.
func writeSourceTree(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	write := func(relative string, data []byte) {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, relative)
	}
	for i := range treeSmallFiles {
		data := make([]byte, treeSmallSize)
		for j := range data {
			data[j] = byte(i + j*7)
		}
		write(fmt.Sprintf("pkg%02d/file%04d.go", i%40, i), data)
	}
	large := make([]byte, treeLargeSize)
	for i := range large {
		large[i] = byte(i * 31)
	}
	write("assets/large.bin", large)
	write("empty.txt", nil)
	return paths
}

// referenceTreeDigest is the digest definition, written with ReadFile so it shares nothing with the streaming code.
func referenceTreeDigest(t *testing.T, root string, paths []string, prefix string) string {
	t.Helper()
	sorted := slices.Clone(paths)
	slices.Sort(sorted)
	hash := sha256.New()
	for _, relative := range sorted {
		path := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = hash.Write([]byte(relative))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(info.Mode().String()))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
	}
	return prefix + hex.EncodeToString(hash.Sum(nil))
}

func allocatedBy(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestPigSourceIdentityStreamsEveryFile(t *testing.T) {
	root := t.TempDir()
	paths := writeSourceTree(t, root)
	want := referenceTreeDigest(t, root, paths, "sha256:")

	var revision, digest string
	var err error
	allocated := allocatedBy(func() {
		revision, digest, err = pigSourceIdentity(pigSource{Root: root, ModuleVersion: "v0.0.0-test"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision != "v0.0.0-test" || digest != want {
		t.Fatalf("pigSourceIdentity = %q, %q; want v0.0.0-test, %s", revision, digest, want)
	}
	if allocated > treeAllocBudget {
		t.Errorf("pigSourceIdentity allocated %d bytes for a %d-file tree; it must stream through one buffer (budget %d)", allocated, len(paths), treeAllocBudget)
	}
}

func TestHashTreeStreamsEveryFile(t *testing.T) {
	root := t.TempDir()
	paths := writeSourceTree(t, root)
	want := referenceTreeDigest(t, root, paths, "sha256:")
	// hashTree walks in lexical order, which is the order the reference sorts into for these names.
	var digest string
	var err error
	allocated := allocatedBy(func() { digest, err = hashTree(root) })
	if err != nil {
		t.Fatal(err)
	}
	if digest != want {
		t.Fatalf("hashTree = %s; want %s", digest, want)
	}
	if allocated > treeAllocBudget {
		t.Errorf("hashTree allocated %d bytes for a %d-file tree; it must stream through one buffer (budget %d)", allocated, len(paths), treeAllocBudget)
	}
}

// A symlink is identified by its target text, not by the file it points at.
func TestPigSourceIdentityHashesASymlinkByItsTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, "real.txt", filepath.Join(root, "link.txt"))
	info, err := os.Lstat(filepath.Join(root, "link.txt"))
	if err != nil {
		t.Fatal(err)
	}
	real, err := os.Lstat(filepath.Join(root, "real.txt"))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	for _, entry := range []struct{ name, mode, content string }{
		{"link.txt", info.Mode().String(), "real.txt"},
		{"real.txt", real.Mode().String(), "real"},
	} {
		_, _ = hash.Write([]byte(entry.name + "\x00" + entry.mode + "\x00" + entry.content + "\x00"))
	}
	want := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	_, got, err := pigSourceIdentity(pigSource{Root: root, ModuleVersion: "v0.0.0-test"})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("pigSourceIdentity = %s; want %s", got, want)
	}
}
