package testenv

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// maxShortTempDirBytes bounds ShortTempDir's path so a socket path of up to 37 bytes below it (for example PiG's
// \h<uint32>\<count>.s under a \pig socket directory) stays within the 107 bytes a sun_path holds on Windows.
const maxShortTempDirBytes = 70

// tempRootEnv overrides the non-Windows /tmp base of MkdirShortTemp and ShortTempDir without resolving symbolic links in it,
// so a Linux gate job can place the test temp root behind a symbolic link (mirroring macOS's /tmp -> /private/tmp).
const tempRootEnv = "PIG_TEST_TEMP_ROOT"

// ShortTempDir creates a directory for the test whose path leaves room for
// Unix-domain socket names below it: sun_path holds 104 bytes on macOS and 108
// on Linux and Windows, and t.TempDir's name holds the test's name. It is in
// /tmp outside Windows (or $PIG_TEST_TEMP_ROOT when set, unresolved), since
// macOS's TMPDIR is long. On Windows it is in os.TempDir, or in
// %LOCALAPPDATA%\pig\s, the directory the extension Host falls back to, when
// os.TempDir is too long to leave that room. Its name starts with prefix.
// Cleanup removes it. On Windows the removal retries for two seconds while
// another process still holds a file, as t.TempDir's cleanup does.
func ShortTempDir(t testing.TB, prefix string) string {
	t.Helper()
	dir, err := MkdirShortTemp(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(dir) > maxShortTempDirBytes && runtime.GOOS == "windows" {
		_ = os.RemoveAll(dir)
		t.Fatalf("short temp directory %q has %d bytes; at most %d leave room for a socket path (set LOCALAPPDATA to a shorter directory)", dir, len(dir), maxShortTempDirBytes)
	}
	t.Cleanup(func() {
		err := os.RemoveAll(dir)
		for deadline := time.Now().Add(2 * time.Second); err != nil && runtime.GOOS == "windows" && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
			err = os.RemoveAll(dir)
		}
		if err != nil {
			t.Error(err)
		}
	})
	return dir
}

// MkdirShortTemp creates the directory ShortTempDir returns, with no test to remove it: a package's TestMain makes its scoped root with it, scopes the process temporary directory to
// it with ScopeTempDir, and removes it after m.Run. The root is then directly in /tmp outside Windows whatever TMPDIR names (or in $PIG_TEST_TEMP_ROOT when set, without resolving
// symbolic links in it), so every t.TempDir and b.TempDir of the package is <root>/<test>/001 at the same depth on every machine. Code that walks the ancestors of its working directory
// (a reload's context files, the skills of a project, the resolution of a path one component at a time) allocates per level, and the allocs/op of a benchmark that runs in a fixture below
// TMPDIR follows the depth of the TMPDIR of whoever runs it.
func MkdirShortTemp(prefix string) (string, error) {
	base := shortTempBase(runtime.GOOS, os.TempDir(), os.Getenv("LOCALAPPDATA"), prefix)
	if runtime.GOOS != "windows" {
		if root := os.Getenv(tempRootEnv); root != "" {
			base = filepath.Clean(root)
		}
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, prefix)
}

// shortTempBase returns the directory ShortTempDir creates its directory in.
func shortTempBase(goos, tempDir, localAppData, prefix string) string {
	if goos != "windows" {
		return "/tmp"
	}
	const uint32Digits = 10
	if len(filepath.Join(tempDir, prefix))+uint32Digits <= maxShortTempDirBytes || localAppData == "" {
		return tempDir
	}
	return filepath.Join(localAppData, "pig", "s")
}
