//go:build windows

package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func shortPathName(t *testing.T, longPath string) string {
	t.Helper()
	in, err := windows.UTF16PtrFromString(longPath)
	if err != nil {
		t.Fatalf("UTF16PtrFromString(%q): %v", longPath, err)
	}
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetShortPathName(in, &buf[0], uint32(len(buf)))
		if err != nil {
			t.Fatalf("GetShortPathName(%q): %v", longPath, err)
		}
		if n == 0 {
			t.Fatalf("GetShortPathName(%q) returned length 0", longPath)
		}
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n])
		}
		buf = make([]uint16, n)
	}
}

func assertShortPathBehavior(t *testing.T, shortDir, longDir string) {
	t.Helper()
	t.Run("read-only file", func(t *testing.T) {
		subShort := filepath.Join(shortDir, "ro")
		subLong := filepath.Join(longDir, "ro")
		if err := os.Mkdir(subLong, 0o700); err != nil {
			t.Fatal(err)
		}
		shortGiven := filepath.Join(subShort, "store.json")
		longTarget := filepath.Join(subLong, "store.json")
		if err := os.WriteFile(longTarget, []byte("kept"), 0o400); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(longTarget, 0o600) })

		err := WriteFile(shortGiven, []byte("lost"), 0o600)
		pathErr, ok := errors.AsType[*fs.PathError](err)
		if !ok {
			t.Fatalf("WriteFile(%q) [resolved %q] = %T (%v), want *fs.PathError", shortGiven, longTarget, err, err)
		}
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("WriteFile(%q) [resolved %q] = %v, want fs.ErrPermission", shortGiven, longTarget, err)
		}
		if pathErr.Path != shortGiven {
			t.Fatalf("PathError.Path = %q, want given short path %q (resolved long path %q)", pathErr.Path, shortGiven, longTarget)
		}
		if got := readString(t, longTarget); got != "kept" {
			t.Fatalf("file at long path %q = %q, want kept", longTarget, got)
		}
		if names := entryNames(t, subLong); len(names) != 1 {
			t.Fatalf("directory %q holds %v after failed write, want only store.json", subLong, names)
		}
	})

	t.Run("new file in missing directory", func(t *testing.T) {
		shortGiven := filepath.Join(shortDir, "missing", "new.json")
		longTarget := filepath.Join(longDir, "missing", "new.json")

		err := WriteFile(shortGiven, []byte("new"), 0o600)
		pathErr, ok := errors.AsType[*fs.PathError](err)
		if !ok {
			t.Fatalf("WriteFile(%q) [resolved %q] = %T (%v), want *fs.PathError", shortGiven, longTarget, err, err)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("WriteFile(%q) [resolved %q] = %v, want fs.ErrNotExist", shortGiven, longTarget, err)
		}
		if pathErr.Path != shortGiven {
			t.Fatalf("PathError.Path = %q, want given short path %q (resolved long path %q)", pathErr.Path, shortGiven, longTarget)
		}
	})

	t.Run("successful write lands in long path", func(t *testing.T) {
		subShort := filepath.Join(shortDir, "ok")
		subLong := filepath.Join(longDir, "ok")
		if err := os.Mkdir(subLong, 0o700); err != nil {
			t.Fatal(err)
		}
		shortGiven := filepath.Join(subShort, "store.json")
		longTarget := filepath.Join(subLong, "store.json")

		if err := WriteFile(shortGiven, []byte("created-via-short"), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) [resolved %q] = %v, want nil", shortGiven, longTarget, err)
		}
		if got := readString(t, longTarget); got != "created-via-short" {
			t.Fatalf("file at long path %q (written via %q) = %q, want %q", longTarget, shortGiven, got, "created-via-short")
		}
		if err := WriteFile(shortGiven, []byte("replaced-via-short"), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) [resolved %q] replace = %v, want nil", shortGiven, longTarget, err)
		}
		if got := readString(t, longTarget); got != "replaced-via-short" {
			t.Fatalf("file at long path %q (replaced via %q) = %q, want %q", longTarget, shortGiven, got, "replaced-via-short")
		}
		if names := entryNames(t, subLong); len(names) != 1 {
			t.Fatalf("directory %q holds %v after write, want only store.json", subLong, names)
		}
	})
}

// WriteFile expands 8.3 short names through filepath.EvalSymlinks on Windows,
// but any *fs.PathError it returns must name the path as given rather than the
// expanded long path (as os.WriteFile and Node's writeFileSync do).
func TestWindowsWriteFileErrorsNameTheGivenShortPath(t *testing.T) {
	t.Run("os.TempDir short path", func(t *testing.T) {
		tempDir := os.TempDir()
		resolvedTemp, err := filepath.EvalSymlinks(tempDir)
		if err != nil {
			t.Fatalf("filepath.EvalSymlinks(%q): %v", tempDir, err)
		}
		if tempDir == resolvedTemp {
			t.Skipf("os.TempDir() %q equals its EvalSymlinks form %q (no 8.3 short component)", tempDir, resolvedTemp)
		}
		shortDir, err := os.MkdirTemp(tempDir, "atomicfile-shortname-*")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(shortDir) })
		longDir, err := filepath.EvalSymlinks(shortDir)
		if err != nil {
			t.Fatalf("filepath.EvalSymlinks(%q): %v", shortDir, err)
		}
		if shortDir == longDir {
			t.Fatalf("created temp dir %q equals its EvalSymlinks form %q despite os.TempDir() %q != %q", shortDir, longDir, tempDir, resolvedTemp)
		}
		assertShortPathBehavior(t, shortDir, longDir)
	})

	t.Run("created 8.3 directory alias", func(t *testing.T) {
		resolvedRoot, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		longDir := filepath.Join(resolvedRoot, "LongDirectoryNameForShortAlias")
		if err := os.Mkdir(longDir, 0o700); err != nil {
			t.Fatal(err)
		}
		shortDir := shortPathName(t, longDir)
		if shortDir == longDir {
			t.Skipf("volume has 8.3 short name creation disabled: GetShortPathName(%q) == %q", longDir, shortDir)
		}
		evalLong, err := filepath.EvalSymlinks(shortDir)
		if err != nil {
			t.Fatalf("filepath.EvalSymlinks(%q) (from %q): %v", shortDir, longDir, err)
		}
		if shortDir == evalLong {
			t.Skipf("volume has 8.3 short name creation disabled: EvalSymlinks(%q) == %q", shortDir, evalLong)
		}
		assertShortPathBehavior(t, shortDir, evalLong)
	})
}
