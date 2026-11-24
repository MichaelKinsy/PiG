//go:build windows

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/fsretry"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The Windows job of CI selects the packages that have a Windows-only test file: this file makes it run internal/atomicfile, whose other tests are
// tagged !windows because they use POSIX modes, ACLs, hard links and named pipes. These cells are the ones the Windows code has: ReplaceFileW and
// MoveFileEx with their retry, the recovery of a ReplaceFileW that stops half way, the directory that cannot be flushed, links and devices.

func windowsReportedPaths(t *testing.T) *[]string {
	t.Helper()
	var reported []string
	SetInPlaceReporter(func(path string, _ error) { reported = append(reported, path) })
	t.Cleanup(func() { SetInPlaceReporter(nil) })
	return &reported
}

func entryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func readString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWindowsWriteFileCreatesANewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readString(t, path); got != "new" {
		t.Fatalf("file = %q, want the content written", got)
	}
	if names := entryNames(t, filepath.Dir(path)); len(names) != 1 {
		t.Fatalf("directory holds %v after the write, want only the file", names)
	}
}

func TestWindowsWriteFileReplacesAnExistingFileAndLeavesNoTemporaryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readString(t, path); got != "new" {
		t.Fatalf("file = %q, want the content written", got)
	}
	if names := entryNames(t, filepath.Dir(path)); len(names) != 1 {
		t.Fatalf("directory holds %v after the replacement, want only the file (no temporary file, no backup)", names)
	}
}

// Go's os.Open holds a file without delete sharing, as an editor, an indexer or a sync client does: ReplaceFileW is then refused (sharing violation,
// ERROR_UNABLE_TO_REMOVE_REPLACED or ERROR_UNABLE_TO_MOVE_REPLACEMENT) until the handle closes. The write waits the hold out and then replaces the file.
func TestWindowsWriteFileWaitsOutAHandleHeldWithoutDeleteSharing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	closed := time.AfterFunc(400*time.Millisecond, func() { _ = held.Close() })
	defer closed.Stop()

	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile after the hold closed: %v", err)
	}
	if got := readString(t, path); got != "new" {
		t.Fatalf("file = %q, want the content written after the hold closed", got)
	}
	if names := entryNames(t, filepath.Dir(path)); len(names) != 1 {
		t.Fatalf("directory holds %v, want only the file", names)
	}
}

// A hold that outlasts fsretry.RenameBudget falls back to an in-place write as Pi writes it (auth-storage.ts:141), reporting the refusal once and removing the temporary file.
func TestWindowsWriteFileWritesInPlaceWhenTheHandleIsNeverClosed(t *testing.T) {
	if testing.Short() {
		t.Skipf("waits the retry budget of %v", fsretry.RenameBudget)
	}
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	reported := windowsReportedPaths(t)

	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile while another handle holds the file without delete sharing = %v, want in-place fallback", err)
	}
	if got := readString(t, path); got != "new" {
		t.Errorf("file = %q after the fallback write, want the new content written in place", got)
	}
	if len(*reported) != 1 || (*reported)[0] != path {
		t.Errorf("in-place writes reported = %v, want one line naming %s", *reported, path)
	}
	_ = held.Close()
	if names := entryNames(t, filepath.Dir(path)); len(names) != 1 {
		t.Errorf("directory holds %v after the fallback write, want only the file", names)
	}
}

// A directory is not flushed on Windows, and that is not an error.
func TestWindowsSyncDirectoryDoesNotFail(t *testing.T) {
	if err := syncDirectory(t.TempDir()); err != nil {
		t.Fatalf("syncDirectory = %v, want nil", err)
	}
}

// A symbolic link is followed and stays a link: the file it names is replaced, as D651 says for every platform. Creating a link needs the privilege or
// the developer mode; testenv.Symlink skips the cell, naming Developer Mode, on a runner without it.
func TestWindowsWriteFileFollowsASymbolicLink(t *testing.T) {
	dir := t.TempDir()
	target, link := filepath.Join(dir, "target.json"), filepath.Join(dir, "link.json")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, target, link)
	if err := WriteFile(link, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link = %v (%v), want it to stay a symbolic link", info, err)
	}
	if got := readString(t, target); got != "new" {
		t.Fatalf("target = %q, want the content written through the link", got)
	}
}

// A device is written in place, never replaced by a file: the NUL device takes the bytes and no file is made beside it.
func TestWindowsWriteFileWritesADeviceInPlace(t *testing.T) {
	const device = `\\.\NUL`
	info, err := os.Stat(device)
	if err != nil || info.Mode().IsRegular() {
		t.Skipf("%s is not a non-regular file here: %v (%v)", device, info, err)
	}
	reported := windowsReportedPaths(t)
	if err := WriteFile(device, []byte("discarded"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) = %v, want the bytes written to the device", device, err)
	}
	if len(*reported) != 1 {
		t.Errorf("in-place writes reported = %v, want one", *reported)
	}
}
