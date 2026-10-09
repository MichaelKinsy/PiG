package testenv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// startDir is the working directory when the test binary started. go test runs every test binary in its package directory, so this is the package source directory. It is captured before any test changes directory.
var startDir, startDirErr = os.Getwd()

// PackageDirPath returns the source directory of the package whose test binary is running. It does not use runtime.Caller: under -trimpath the file names it reports are module-relative import paths, not directories, and the build cache is only shared between checkouts when every build uses -trimpath.
func PackageDirPath() (string, error) {
	if startDirErr != nil {
		return "", fmt.Errorf("package directory: %w", startDirErr)
	}
	return startDir, nil
}

// ModuleRootPath returns the root of the module that contains the running test's package: the nearest directory at or above the package directory that holds a go.mod file.
func ModuleRootPath() (string, error) {
	dir, err := PackageDirPath()
	if err != nil {
		return "", err
	}
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("module root: %w", err)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("module root: no go.mod at or above %s", dir)
		}
		d = parent
	}
}

// PackageDir is PackageDirPath for a test; it fails the test on error.
func PackageDir(t testing.TB) string {
	t.Helper()
	dir, err := PackageDirPath()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// ModuleRoot is ModuleRootPath for a test; it fails the test on error.
func ModuleRoot(t testing.TB) string {
	t.Helper()
	root, err := ModuleRootPath()
	if err != nil {
		t.Fatal(err)
	}
	return root
}
