package testenv

import (
	"bytes"
	"go/build"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every test in this module that builds for Windows creates symbolic links
// through Symlink. It then skips, naming Developer Mode, only when Windows
// refuses the symlink privilege, and runs where the privilege is held. A
// direct os.Symlink call fails the test on such a host instead.
func TestWindowsTestsCreateSymlinksThroughSymlink(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %s: %v", root, err)
	}
	windows := build.Default
	windows.GOOS = "windows"
	windows.GOARCH = "amd64"
	call := []byte("os." + "Symlink(")
	var direct []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			switch entry.Name() {
			case ".git", ".upstream", "node_modules", "testdata", "target":
				return filepath.SkipDir
			}
			// A nested module cannot import this package.
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		match, err := windows.MatchFile(filepath.Dir(path), entry.Name())
		if err != nil || !match {
			return err
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(source, call) {
			rel, _ := filepath.Rel(root, path)
			direct = append(direct, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(direct) > 0 {
		t.Fatalf("tests that build for Windows call os.Symlink directly; use testenv.Symlink:\n%s", strings.Join(direct, "\n"))
	}
}
