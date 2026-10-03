package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ScopeTempDir must redirect the parent's os.TempDir, os.CreateTemp("", ...) and the environment a child inherits, so a file a helper forgets to remove stays under the directory TestMain removes.
func TestScopeTempDirRedirectsParentAndChildren(t *testing.T) {
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, os.Getenv(key)) // restores the original value when the test ends
	}
	root := t.TempDir()
	if err := ScopeTempDir(root); err != nil {
		t.Fatal(err)
	}
	if got := os.TempDir(); got != root {
		t.Fatalf("os.TempDir() = %q, want %q", got, root)
	}
	file, err := os.CreateTemp("", "pig-ext-scope-*.log")
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if filepath.Dir(file.Name()) != root {
		t.Fatalf("log %q outside %q", file.Name(), root)
	}
	if runtime.GOOS == "windows" {
		return
	}
	out, err := exec.Command("sh", "-c", `printf %s "$TMPDIR"`).Output()
	if err != nil || strings.TrimSpace(string(out)) != root {
		t.Fatalf("child TMPDIR = %q, %v; want %q", out, err, root)
	}
}

func TestRequireScopedTempDirAcceptsScopeAndRejectsShared(t *testing.T) {
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, os.Getenv(key))
	}
	root, err := os.MkdirTemp("", "pig-scope-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := ScopeTempDir(root); err != nil {
		t.Fatal(err)
	}
	RequireScopedTempDir(t, "pig-scope-test-")
	leaks, err := filepath.Glob(filepath.Join(root, "pig-scope-probe-*"))
	if err != nil || len(leaks) != 0 {
		t.Fatalf("probe files left behind: %v, %v", leaks, err)
	}
	fake := &recordingTB{TB: t}
	func() {
		defer func() { _ = recover() }()
		RequireScopedTempDir(fake, "pig-other-")
	}()
	if !fake.failed {
		t.Fatal("a temporary directory without the package prefix was accepted")
	}
}

type recordingTB struct {
	testing.TB
	failed bool
}

func (r *recordingTB) Helper() {}
func (r *recordingTB) Fatalf(string, ...any) {
	r.failed = true
	panic("fatal")
}

// A test that replaces HOME must not move the go command's build and module caches: the child `go build` of PiG would start from empty caches and recompile the module graph.
func TestKeepGoBuildCachesSurvivesAHomeChange(t *testing.T) {
	t.Setenv("GOCACHE", "")
	t.Setenv("GOMODCACHE", "")
	goEnv := func() string {
		t.Helper()
		out, err := exec.CommandContext(t.Context(), "go", "env", "GOCACHE", "GOMODCACHE").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	want := goEnv()
	if err := KeepGoBuildCaches(); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	if got := goEnv(); got != want {
		t.Fatalf("go env after a HOME change = %q, want the caches %q", got, want)
	}
}
