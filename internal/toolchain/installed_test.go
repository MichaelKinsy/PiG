//go:build unix

package toolchain

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeTool writes an executable named name into dir.
func writeTool(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho "+name+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// An installation's toolchain directory is the only place a configured binary looks: a conflicting tool on PATH never replaces it, and a
// tool the installation lacks is missing rather than taken from PATH.
func TestLookPathReadsOnlyTheInstalledToolchainDir(t *testing.T) {
	pathDir, installed := t.TempDir(), t.TempDir()
	for _, name := range []string{"cargo", "uv", "node"} {
		writeTool(t, pathDir, name)
	}
	t.Setenv("PATH", pathDir)
	if got, err := LookPath("cargo"); err != nil || got != filepath.Join(pathDir, "cargo") {
		t.Fatalf("without an installed directory LookPath(cargo) = %q, %v; want PATH's", got, err)
	}
	if got := Command("cargo"); got != "cargo" {
		t.Fatalf("without an installed directory Command(cargo) = %q, want the bare name the start resolves", got)
	}

	defer SetInstalledToolchainDirForTest(installed)()
	want := writeTool(t, installed, "cargo")
	if err := os.WriteFile(filepath.Join(installed, "node"), []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := LookPath("cargo"); err != nil || got != want {
		t.Fatalf("LookPath(cargo) = %q, %v; want the installed %q", got, err, want)
	}
	for _, name := range []string{"uv", "node"} {
		got, err := LookPath(name)
		if !errors.Is(err, exec.ErrNotFound) || !strings.Contains(err.Error(), installed) {
			t.Fatalf("LookPath(%s) = %q, %v; want not found in %s, not PATH's", name, got, err, installed)
		}
		if got := Command(name); got != filepath.Join(installed, name) {
			t.Fatalf("Command(%s) = %q, want its installed path, whose start reports it missing instead of running PATH's", name, got)
		}
	}
	if got := Command("cargo"); got != want {
		t.Fatalf("Command(cargo) = %q, want %q", got, want)
	}
}

func TestEnvironPutsTheInstalledToolchainDirFirst(t *testing.T) {
	environ := []string{"HOME=/home/u", "PATH=/usr/bin:/bin"}
	if got := Environ(environ); strings.Join(got, "\n") != strings.Join(environ, "\n") {
		t.Fatalf("Environ without an installed directory = %v", got)
	}
	installed := t.TempDir()
	defer SetInstalledToolchainDirForTest(installed)()
	got := Environ(environ)
	if path, count := envValue(got, "PATH"); count != 1 || path != installed+":/usr/bin:/bin" {
		t.Fatalf("PATH = %q (%d), want the installed directory first", path, count)
	}
	if environ[1] != "PATH=/usr/bin:/bin" {
		t.Fatal("Environ modified its argument")
	}
	// go.env sets GOTOOLCHAIN=auto, which would run another go release named by a go or toolchain directive.
	if value, count := envValue(Environ(append(slices.Clone(environ), "GOTOOLCHAIN=go1.99.0+auto")), "GOTOOLCHAIN"); count != 1 || value != "local" {
		t.Fatalf("GOTOOLCHAIN = %q (%d), want the installed go pinned with local", value, count)
	}
	if path, _ := envValue(Environ([]string{"HOME=/h"}), "PATH"); path != installed {
		t.Fatalf("PATH without one inherited = %q, want %q", path, installed)
	}
}

// ResolveGo selects the installation's go even when PATH has another, gives a build that installation's PATH (cgo's C compiler), and never
// falls back to a PiG-managed Go when the installation supplies none.
func TestResolveGoUsesTheInstalledToolchain(t *testing.T) {
	onPath := fakeGoRoot(t, "go1.26.1")
	nixGo := fakeGoRoot(t, "go1.27.3")
	selectGo(t, filepath.Join(onPath, "bin"), "")
	installed := t.TempDir()
	if err := os.Symlink(filepath.Join(nixGo, "bin", "go"), filepath.Join(installed, "go")); err != nil {
		t.Fatal(err)
	}
	defer SetInstalledToolchainDirForTest(installed)()
	tc, err := ResolveGo()
	if err != nil {
		t.Fatal(err)
	}
	if tc.Root != nixGo || tc.Version() != "go1.27.3" {
		t.Fatalf("ResolveGo = %+v, want the installed go1.27.3 at %s", tc, nixGo)
	}
	if path, _ := envValue(tc.Environ([]string{"PATH=" + filepath.Join(onPath, "bin")}), "PATH"); !strings.HasPrefix(path, installed+":") {
		t.Fatalf("build PATH = %q, want %s first", path, installed)
	}

	managed := ManagedGoRoot(os.Getenv("PIG_HOME"))
	if err := os.MkdirAll(filepath.Join(managed, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTool(t, filepath.Join(managed, "bin"), "go")
	empty := t.TempDir()
	defer SetInstalledToolchainDirForTest(empty)()
	if tc, err := ResolveGo(); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("ResolveGo with an installation that supplies no go = %+v, %v; want not found", tc, err)
	}
}

// An installation without Python names its own missing interpreter, so the launch reports it instead of running python3 from PATH.
func TestPythonExecutableStaysInTheInstalledToolchainDir(t *testing.T) {
	pathDir, installed := t.TempDir(), t.TempDir()
	writeTool(t, pathDir, "python3")
	t.Setenv("PATH", pathDir)
	defer SetInstalledToolchainDirForTest(installed)()
	if got := PythonExecutable("linux", LookPath); got != filepath.Join(installed, "python3") {
		t.Fatalf("PythonExecutable = %q, want the installation's missing %s", got, filepath.Join(installed, "python3"))
	}
	want := writeTool(t, installed, "python3")
	if got := PythonExecutable("linux", LookPath); got != want {
		t.Fatalf("PythonExecutable = %q, want %q", got, want)
	}
}

// GoToolchain.Environ returns a new environment and leaves its argument as it was, with or without an installed toolchain directory.
func TestGoToolchainEnvironLeavesItsArgument(t *testing.T) {
	for _, installed := range []string{"", t.TempDir()} {
		restore := SetInstalledToolchainDirForTest(installed)
		environ := []string{"A=1", "GOROOT=/old", "B=2"}
		if got, _ := envValue(GoToolchain{Root: "/new"}.Environ(environ), "GOROOT"); got != "/new" {
			t.Fatalf("installed=%q: GOROOT = %q, want /new", installed, got)
		}
		if !slices.Equal(environ, []string{"A=1", "GOROOT=/old", "B=2"}) {
			t.Fatalf("installed=%q: Environ modified its argument to %v", installed, environ)
		}
		restore()
	}
}
