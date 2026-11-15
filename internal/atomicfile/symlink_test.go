//go:build !windows

package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func isSymlink(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()&os.ModeSymlink != 0
}

// writeFileSync follows a symbolic link, to the file or, where the file does not exist, to the file it names, which it creates: the link stays a link. A
// link whose target is missing must not be replaced by a regular file with the target still missing.
func TestWriteFileFollowsALinkToAMissingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real", "auth.json")
	if err := os.Mkdir(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "auth.json")
	if err := os.Symlink(filepath.Join("real", "auth.json"), link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(link, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isSymlink(t, link) {
		t.Fatal("the link to a missing target was replaced by a regular file")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "new" {
		t.Fatalf("target = %q, %v; want the new bytes in the file the link names", data, err)
	}
	noTemporaryFile(t, dir)
	noTemporaryFile(t, filepath.Dir(target))
}

// A chain of links that ends at a missing file: every link stays, the file at the end of the chain is created.
func TestWriteFileFollowsAChainOfLinksToAMissingTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	last := filepath.Join(dir, "b", "auth.json")
	middle := filepath.Join(dir, "a", "hop")
	first := filepath.Join(dir, "auth.json")
	if err := os.Symlink(last, middle); err != nil { // absolute
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("a", "hop"), first); err != nil { // relative to the link's directory
		t.Fatal(err)
	}
	if err := WriteFile(first, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isSymlink(t, first) || !isSymlink(t, middle) {
		t.Fatal("a link of the chain was replaced by a regular file")
	}
	if data, err := os.ReadFile(last); err != nil || string(data) != "new" {
		t.Fatalf("end of the chain = %q, %v; want the new bytes", data, err)
	}
}

// Where the file a link names cannot be created (its directory does not exist) or the links go round in a circle, writeFileSync fails (ENOENT, ELOOP) and
// leaves the link as it was: the link is not turned into a regular file.
func TestWriteFileFailsLikeWriteFileSyncForALinkItCannotFollow(t *testing.T) {
	dir := t.TempDir()
	dangling := filepath.Join(dir, "dangling.json")
	if err := os.Symlink(filepath.Join("nowhere", "auth.json"), dangling); err != nil {
		t.Fatal(err)
	}
	loopA, loopB := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	if err := os.Symlink("b.json", loopA); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.json", loopB); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		want error
	}{{dangling, syscall.ENOENT}, {loopA, syscall.ELOOP}} {
		if err := WriteFile(c.path, []byte("new"), 0o600); !errors.Is(err, c.want) {
			t.Errorf("WriteFile(%s) = %v, want %v", filepath.Base(c.path), err, c.want)
		}
		if !isSymlink(t, c.path) {
			t.Errorf("%s was replaced by a regular file", filepath.Base(c.path))
		}
	}
	noTemporaryFile(t, dir)
}
