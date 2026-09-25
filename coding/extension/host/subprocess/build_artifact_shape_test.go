package subprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `go build -o out .` on a package that is not `main` writes a static library
// archive and exits 0. The build looks successful, the artifact is on disk at
// the expected path, and the failure only appears later when the host tries to
// execute it, reported as a launch error rather than as a build problem.
//
// This is the shape behind a bug report that read a 2 KB archive in the cache
// and concluded extension builds were broken. Catching it here names the real
// cause at the moment it happens.
func TestBuildRejectsANonMainPackageInsteadOfEmittingAnArchive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build test in short mode")
	}
	srcDir := t.TempDir()
	writeFixtureFile(t, srcDir, "go.mod", "module fixture-ext\ngo 1.26\n")
	// A factory-pattern extension that was never promoted to the factory path:
	// a legitimate package, wrong build strategy.
	writeFixtureFile(t, srcDir, "ext.go", "package ext\n\nfunc Extension() string { return \"hi\" }\n")

	result, err := NewBuilder(t.TempDir()).Build("fixture", srcDir)
	if err == nil {
		data, _ := os.ReadFile(result.BinaryPath)
		t.Fatalf("build reported success and wrote %d bytes starting %q; "+
			"the host will try to execute this and fail with a launch error",
			len(data), first(data, 8))
	}
	for _, want := range []string{"main"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q, so it does not say what to change", err, want)
		}
	}
}

// The ordinary case must keep working, or the guard has traded one broken build
// for another.
func TestBuildStillProducesAnExecutableForAMainPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build test in short mode")
	}
	srcDir := t.TempDir()
	writeFixtureFile(t, srcDir, "go.mod", "module fixture-ext\ngo 1.26\n")
	writeFixtureFile(t, srcDir, "main.go", "package main\n\nfunc main() {}\n")

	result, err := NewBuilder(t.TempDir()).Build("fixture", srcDir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	data, err := os.ReadFile(result.BinaryPath)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if isArchive(data) {
		t.Fatalf("a main package produced an archive at %s", result.BinaryPath)
	}
	// Running the artifact proves it is executable on every platform: the
	// execute bit on Unix, the .exe name on Windows.
	if output, err := exec.Command(result.BinaryPath).CombinedOutput(); err != nil {
		t.Errorf("artifact %s is not runnable: %v\n%s", result.BinaryPath, err, output)
	}
}

func writeFixtureFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func first(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}
