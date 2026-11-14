//go:build !windows

package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// reportedPaths collects what WriteFile reports for the writes it did in place, and puts the previous reporter back when the test ends.
func reportedPaths(t *testing.T) *[]string {
	t.Helper()
	var reported []string
	SetInPlaceReporter(func(path string, cause error) {
		if cause == nil {
			t.Errorf("an in-place write of %s was reported without a cause", path)
		}
		reported = append(reported, path)
	})
	t.Cleanup(func() { SetInPlaceReporter(nil) })
	return &reported
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return uint64(info.Sys().(*syscall.Stat_t).Ino)
}

// refusedRename makes the rename of the temporary file fail with err, as the platform does where it cannot replace the target.
func refusedRename(t *testing.T, err error) {
	t.Helper()
	previous := replaceTemp
	replaceTemp = func(temp, path string, _ bool) error {
		return &os.LinkError{Op: "rename", Old: temp, New: path, Err: err}
	}
	t.Cleanup(func() { replaceTemp = previous })
}

// Where a platform refuses the replacement (a file mounted into a container on its own is a mount point: rename gives EBUSY), the file is written in place with the same bytes, as Pi does, once, and the temporary file is gone.
func TestWriteFileWritesInPlaceWhereTheRenameIsRefused(t *testing.T) {
	for _, refusal := range []error{syscall.EBUSY, syscall.EXDEV, syscall.ETXTBSY} {
		t.Run(refusal.Error(), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "auth.json")
			if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			before := inode(t, path)
			reported := reportedPaths(t)
			refusedRename(t, refusal)
			if err := WriteFile(path, []byte("new bytes"), 0o600); err != nil {
				t.Fatalf("WriteFile where the rename is refused with %v = %v, want an in-place write", refusal, err)
			}
			if data, _ := os.ReadFile(path); string(data) != "new bytes" {
				t.Fatalf("content = %q, want the new bytes written in place", data)
			}
			if inode(t, path) != before {
				t.Fatal("the file was replaced, not written in place")
			}
			if len(*reported) != 1 || (*reported)[0] != path {
				t.Fatalf("reported = %v, want one line naming %s", *reported, path)
			}
			noTemporaryFile(t, dir)
		})
	}
}

// A rename that fails for any other reason is an error with the old file untouched: only a refusal to replace falls back.
func TestWriteFileKeepsTheFileWhenTheRenameFailsForAnotherReason(t *testing.T) {
	for _, failure := range []error{syscall.ENOSPC, syscall.EIO} {
		t.Run(failure.Error(), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "auth.json")
			if err := os.WriteFile(path, []byte("kept"), 0o600); err != nil {
				t.Fatal(err)
			}
			reported := reportedPaths(t)
			refusedRename(t, failure)
			if err := WriteFile(path, []byte("lost"), 0o600); !errors.Is(err, failure) {
				t.Fatalf("WriteFile = %v, want %v", err, failure)
			}
			if data, _ := os.ReadFile(path); string(data) != "kept" {
				t.Fatalf("content = %q, want the old bytes", data)
			}
			if len(*reported) != 0 {
				t.Fatalf("reported %v for a failure that is not a refusal", *reported)
			}
			noTemporaryFile(t, dir)
		})
	}
}

// A directory the process cannot write into, holding a file it can: the temporary file cannot be created, so the file is written in place as Pi writes it.
func TestWriteFileWritesInPlaceInADirectoryItCannotWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into any directory")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "keybindings.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	before := inode(t, path)
	reported := reportedPaths(t)
	if err := WriteFile(path, []byte("new bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile in a read-only directory = %v, want an in-place write", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "new bytes" {
		t.Fatalf("content = %q", data)
	}
	if inode(t, path) != before {
		t.Fatal("the file was replaced, not written in place")
	}
	if len(*reported) != 1 || (*reported)[0] != path {
		t.Fatalf("reported = %v, want one line naming %s", *reported, path)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	noTemporaryFile(t, dir)
}

// A file that cannot be created at all in a directory that cannot be written is an error, as it is for an in-place write.
func TestWriteFileCannotCreateAFileInADirectoryItCannotWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into any directory")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := WriteFile(filepath.Join(dir, "new.json"), []byte("x"), 0o600); !os.IsPermission(err) {
		t.Fatalf("WriteFile of a new file in a read-only directory = %v, want a permission error", err)
	}
}

// An in-place write that fails for a real reason (the disk is full) is the error it was: a symbolic link to /dev/full resolves to a target whose directory refuses a temporary file, so the write goes in place and fails there.
func TestWriteFileReportsARealFailureOfTheInPlaceWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root creates files in /dev")
	}
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full")
	}
	link := filepath.Join(t.TempDir(), "auth.json")
	if err := os.Symlink("/dev/full", link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(link, []byte("data"), 0o600); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("WriteFile onto a full device = %v, want ENOSPC", err)
	}
}

// A rename would give the path a new inode and leave the other names of the old file with the old bytes: a file with more than one hard link is written in place, so every name sees the write.
func TestWriteFileWritesInPlaceThroughAHardLink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	other := filepath.Join(dir, "elsewhere.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, other); err != nil {
		t.Skipf("hard links are not available here: %v", err)
	}
	before := inode(t, path)
	reported := reportedPaths(t)
	if err := WriteFile(path, []byte("new bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, other} {
		if data, _ := os.ReadFile(name); string(data) != "new bytes" {
			t.Fatalf("%s = %q, want the new bytes through every link", name, data)
		}
	}
	if inode(t, path) != before || inode(t, other) != before {
		t.Fatal("the link was broken by a replacement")
	}
	if len(*reported) != 1 {
		t.Fatalf("reported = %v, want one line", *reported)
	}
	noTemporaryFile(t, dir)
}

// The replacement is atomic whenever it changes nothing but the inode: a plain file is replaced and nothing is reported.
func TestWriteFileReplacesAPlainFileAndReportsNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := inode(t, path)
	reported := reportedPaths(t)
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if inode(t, path) == before {
		t.Fatal("a plain file was written in place")
	}
	if len(*reported) != 0 {
		t.Fatalf("reported = %v for an atomic replacement", *reported)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 || strings.Contains(entries[0].Name(), ".tmp-") {
		t.Fatalf("directory holds %v", entries)
	}
}
