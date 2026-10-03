package testenv

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ScopeTempDir makes root the process temporary directory for a test binary and every process it starts: TMPDIR on Unix, TMP and TEMP on Windows. A package's TestMain calls it with the directory it removes after m.Run, so a temporary file or directory created through os.TempDir, os.CreateTemp("", ...), or a child's tmpdir() stays inside that root and is removed with it, whichever test or helper created it and whatever it forgot to remove. Extension hosts create their stderr logs this way (pig-ext-*.log and pig-packed-*.log) and keep the log of a crashed extension by design.
//
// It also pins GOCACHE and GOMODCACHE (KeepGoBuildCaches), so a test that points HOME at a temporary directory and then runs `go build` links from the compiled packages a user's builds share instead of recompiling PiG and its dependencies in an empty cache.
func ScopeTempDir(root string) error {
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		if err := os.Setenv(key, root); err != nil {
			return fmt.Errorf("scope %s to %s: %w", key, root, err)
		}
	}
	return KeepGoBuildCaches()
}

// KeepGoBuildCaches sets GOCACHE and GOMODCACHE to the directories the go command resolves for the current environment unless the caller already set them. The go command derives both from HOME, so a test that later replaces HOME would otherwise make every child `go build` start from empty caches: the whole module graph recompiles, and that costs minutes of CPU per build. A package's TestMain reaches it through ScopeTempDir, before any test changes HOME. A package without a go command on PATH keeps its environment unchanged.
func KeepGoBuildCaches() error {
	if os.Getenv("GOCACHE") != "" && os.Getenv("GOMODCACHE") != "" {
		return nil
	}
	out, err := exec.Command("go", "env", "-json", "GOCACHE", "GOMODCACHE").Output()
	if err != nil {
		return nil
	}
	var resolved map[string]string
	if err := json.Unmarshal(out, &resolved); err != nil {
		return fmt.Errorf("read go build cache locations: %w", err)
	}
	for key, value := range resolved {
		if value == "" || os.Getenv(key) != "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("pin %s to %s: %w", key, value, err)
		}
	}
	return nil
}

// RunScoped runs m with the process temporary directory scoped to a fresh directory named prefix+random under the real one, removes that directory afterwards, and returns the exit code for os.Exit. Removal failure turns a passing run into exit code 2 so a leak fails the package.
func RunScoped(m *testing.M, prefix string) int {
	root, err := os.MkdirTemp("", prefix)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create scoped test temp directory:", err)
		return 2
	}
	if err := ScopeTempDir(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(root)
		return 2
	}
	code := m.Run()
	if err := os.RemoveAll(root); err != nil {
		fmt.Fprintln(os.Stderr, "remove scoped test temp directory:", err)
		if code == 0 {
			code = 2
		}
	}
	return code
}

// RequireScopedTempDir fails the test unless the process temporary directory is the one this package's TestMain scoped, named prefix+random. It guards the TestMain wiring: without it, extension hosts and helpers write pig-ext-*.log and pig-packed-*.log files, and other scratch directories, straight into the shared temporary directory, where nothing removes them.
func RequireScopedTempDir(t testing.TB, prefix string) {
	t.Helper()
	dir := os.TempDir()
	if !strings.HasPrefix(filepath.Base(dir), prefix) {
		t.Fatalf("os.TempDir() = %q, want a directory named %s* scoped and removed by this package's TestMain", dir, prefix)
	}
	scratch, err := os.CreateTemp("", "pig-scope-probe-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(scratch.Name()) }()
	_ = scratch.Close()
	if filepath.Dir(scratch.Name()) != dir {
		t.Fatalf("os.CreateTemp(\"\", ...) made %q outside the scoped temporary directory %q", scratch.Name(), dir)
	}
}
