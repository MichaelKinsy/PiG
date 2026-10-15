package resolvepath

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// pi: packages/coding-agent/src/utils/paths.ts

// Ports test/paths.test.ts: describe("resolvePath") and describe("normalizeWindowsShellPath").

func fileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func TestResolvePathExpandsOnlyHomeTildeShortcuts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd := filepath.Join(t.TempDir(), "pi-paths-cwd")
	if got, err := Normalize("~"); err != nil || got != home {
		t.Errorf(`Normalize("~") = %q, %v, want %q`, got, err, home)
	}
	if got, err := Normalize("~/file.txt"); err != nil || got != filepath.Join(home, "file.txt") {
		t.Errorf(`Normalize("~/file.txt") = %q, %v`, got, err)
	}
	if got, err := Resolve("~draft.md", cwd); err != nil || got != filepath.Join(cwd, "~draft.md") {
		t.Errorf(`Resolve("~draft.md") = %q, %v, want it under the base`, got, err)
	}
	if got, err := Normalize("~draft.md"); err != nil || got != "~draft.md" {
		t.Errorf(`Normalize("~draft.md") = %q, %v, want it unchanged`, got, err)
	}
}

func TestResolvePathResolvesRelativePathsAgainstTheBaseDirectory(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "pi-paths-cwd")
	want := filepath.Join(cwd, "subdir", "file.txt")
	if got, err := Resolve("subdir/file.txt", cwd); err != nil || got != want {
		t.Errorf("Resolve(relative, base) = %q, %v, want %q", got, err, want)
	}
	// the base directory is itself normalized, so a file URL base resolves like its path
	if got, err := Resolve("subdir/file.txt", fileURL(cwd)); err != nil || got != want {
		t.Errorf("Resolve(relative, file URL base) = %q, %v, want %q", got, err, want)
	}
}

func TestResolvePathAcceptsFileURLsAndRejectsInvalidOnes(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file with spaces.txt")
	if got, err := Resolve(fileURL(file), filepath.Join(dir, "base")); err != nil || got != file {
		t.Errorf("Resolve(file URL) = %q, %v, want %q", got, err, file)
	}
	if got, err := Resolve("file:///%E0%A4%A", ""); err == nil {
		t.Errorf("Resolve of an invalid file URL = %q, want an error", got)
	}
}

func TestResolvePathPreservesPOSIXAbsolutePathsWithLiteralPercentSequences(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX absolute paths")
	}
	dir := t.TempDir()
	for _, name := range []string{"report%2026.md", "foo%2Fbar", "malformed%A.md"} {
		path := filepath.Join(dir, name)
		if got, err := Resolve(path, filepath.Join(dir, "base")); err != nil || got != path {
			t.Errorf("Resolve(%q) = %q, %v, want it unchanged", path, got, err)
		}
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeWindowsShellPathConvertsDrivePathsAndLeavesOthers(t *testing.T) {
	for in, want := range map[string]string{
		"/c/Users/example/project": `C:\Users\example\project`,
		"/cygdrive/d/work":         `D:\work`,
		"/mnt/e/source":            `E:\source`,
		"/c":                       `C:\`,
	} {
		if got := NormalizeWindowsShellPath(in); got != want {
			t.Errorf("NormalizeWindowsShellPath(%q) = %q, want %q", in, got, want)
		}
	}
	for _, path := range []string{`C:/Users/example`, `C:\Users\example`, "//server/share/file", `/c/Users\example`, "relative/file", "/tmp/file"} {
		if got := NormalizeWindowsShellPath(path); got != path {
			t.Errorf("NormalizeWindowsShellPath(%q) = %q, want it unchanged", path, got)
		}
	}
}
