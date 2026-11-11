package testenv

import (
	"errors"
	"io/fs"
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
	t.Setenv("XDG_CONFIG_HOME", "")
	DisableGoTelemetry(t, home)
	if got := goEnv(); got != want {
		t.Fatalf("go env after a HOME change = %q, want the caches %q", got, want)
	}
}

// The go command must read the mode DisableGoTelemetry writes, and a go command run in that home must then fork no sidecar. The sidecar's first act is to create local/ under the telemetry directory, so a go command that leaves none behind started none.
func TestDisableGoTelemetryKeepsGoCommandsFromForkingASidecar(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(home, config); err != nil || !filepath.IsLocal(rel) {
		t.Skipf("the go configuration directory %q is outside HOME on %s", config, runtime.GOOS)
	}
	DisableGoTelemetry(t, home)
	out, err := exec.CommandContext(t.Context(), "go", "env", "GOTELEMETRY", "GOTELEMETRYDIR").Output()
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(fields) != 2 || fields[0] != "off" || fields[1] != filepath.Join(config, "go", "telemetry") {
		t.Fatalf("go env GOTELEMETRY GOTELEMETRYDIR = %q, want off and the telemetry directory under %q", out, config)
	}
	if _, err := os.Stat(filepath.Join(fields[1], "local")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a go command in a home with telemetry off created counters in %q (stat err = %v): it forked a sidecar", fields[1], err)
	}
}

// A configuration directory outside the test's home is the caller's own: its telemetry mode must survive the test.
func TestDisableGoTelemetryLeavesAConfigurationOutsideHomeAlone(t *testing.T) {
	outside := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", outside)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if config, err := os.UserConfigDir(); err != nil || config != outside {
		t.Skipf("the go configuration directory %q does not follow XDG_CONFIG_HOME on %s", config, runtime.GOOS)
	}
	DisableGoTelemetry(t, home)
	for _, dir := range []string{home, outside} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("DisableGoTelemetry wrote %v into %q, which holds the caller's own configuration", entries, dir)
		}
	}
}

// A lane exports PIG_CODING_AGENT_DIR for its own pig process and every child inherits it. ScopeTempDir must replace it, and every sibling that selects the agent directory, with directories under the scoped root.
func TestScopeTempDirIsolatesAgentDirectoryEnvironment(t *testing.T) {
	real := t.TempDir()
	keys := []string{"PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "PIG_HOME", "PI_HOME", "PIG_CODING_AGENT_SESSION_DIR", "PI_CODING_AGENT_SESSION_DIR", "TMPDIR", "TMP", "TEMP"}
	for _, key := range keys {
		t.Setenv(key, real)
	}
	root := t.TempDir()
	if err := ScopeTempDir(root); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys[:4] {
		got := os.Getenv(key)
		if got == real || filepath.Dir(got) != root {
			t.Errorf("%s = %q, want a directory directly under %q", key, got, root)
		}
		if info, err := os.Stat(got); err != nil || !info.IsDir() {
			t.Errorf("%s directory %q: %v", key, got, err)
		}
	}
	for _, key := range keys[4:6] {
		if value, ok := os.LookupEnv(key); ok {
			t.Errorf("%s = %q, want unset", key, value)
		}
	}
}

func TestRequireIsolatedAgentEnvAcceptsOnlyTheScopedDirectories(t *testing.T) {
	scoped := t.TempDir()
	t.Setenv("TMPDIR", scoped)
	t.Setenv("TMP", scoped)
	t.Setenv("TEMP", scoped)
	for _, key := range append([]string{"PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "PIG_HOME", "PI_HOME"}, agentSessionEnv...) {
		t.Setenv(key, t.TempDir())
	}
	if err := IsolateAgentEnv(scoped); err != nil {
		t.Fatal(err)
	}
	RequireIsolatedAgentEnv(t) // passes after isolation

	probe := &errorRecorder{TB: t}
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	RequireIsolatedAgentEnv(probe)
	if !probe.failed {
		t.Fatal("an agent directory outside the scoped root passed")
	}
}

type errorRecorder struct {
	testing.TB
	failed bool
}

func (r *errorRecorder) Helper()               {}
func (r *errorRecorder) Errorf(string, ...any) { r.failed = true }
