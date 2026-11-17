package testenv

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ScopeTempDir makes root the process temporary directory for a test binary and every process it starts: TMPDIR on Unix, TMP and TEMP on Windows. A package's TestMain calls it with the directory it removes after m.Run, so a temporary file or directory created through os.TempDir, os.CreateTemp("", ...), or a child's tmpdir() stays inside that root and is removed with it, whichever test or helper created it and whatever it forgot to remove. Extension hosts create their stderr logs this way (pig-ext-*.log and pig-packed-*.log) and keep the log of a crashed extension by design.
//
// It also moves every agent-directory variable under root (IsolateAgentEnv), so a test that reaches the agent directory through the environment cannot write the credentials, settings, trust file or sessions of the user running `go test`.
//
// It also pins GOCACHE and GOMODCACHE (KeepGoBuildCaches), so a test that points HOME at a temporary directory and then runs `go build` links from the compiled packages a user's builds share instead of recompiling PiG and its dependencies in an empty cache.
func ScopeTempDir(root string) error {
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		if err := os.Setenv(key, root); err != nil {
			return fmt.Errorf("scope %s to %s: %w", key, root, err)
		}
	}
	if err := IsolateAgentEnv(root); err != nil {
		return err
	}
	return KeepGoBuildCaches()
}

// agentEnvDirs maps each variable that selects an agent or home directory to its subdirectory of the isolation root. PIG_CODING_AGENT_DIR (or PI_CODING_AGENT_DIR in shared-directory mode) wins over PIG_HOME, so a lane that exports it points every child process at the lane's real credentials unless a test replaces it.
var agentEnvDirs = []struct{ key, dir string }{
	{"PIG_CODING_AGENT_DIR", "agent"},
	{"PI_CODING_AGENT_DIR", "pi-agent"},
	{"PIG_HOME", "pig-home"},
	{"PI_HOME", "pi-home"},
}

// agentSessionEnv names the variables that move session storage out of the agent directory. They are cleared so sessions follow the isolated agent directory.
var agentSessionEnv = []string{"PIG_CODING_AGENT_SESSION_DIR", "PI_CODING_AGENT_SESSION_DIR"}

// IsolateAgentEnv points PIG_CODING_AGENT_DIR, PI_CODING_AGENT_DIR, PIG_HOME and PI_HOME at fresh directories under root and clears the session-directory overrides. A test binary calls it from TestMain through ScopeTempDir, before any test or child process reads the environment; a test that needs an unset variable clears it itself. Each directory is created empty. HOME stays unchanged: the Go, Node and Rust toolchains resolve their caches and shims from it.
func IsolateAgentEnv(root string) error {
	for _, entry := range agentEnvDirs {
		dir := filepath.Join(root, entry.dir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create isolated %s: %w", entry.key, err)
		}
		if err := os.Setenv(entry.key, dir); err != nil {
			return fmt.Errorf("isolate %s: %w", entry.key, err)
		}
	}
	for _, key := range agentSessionEnv {
		if err := os.Unsetenv(key); err != nil {
			return fmt.Errorf("clear %s: %w", key, err)
		}
	}
	return nil
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

// DisableGoTelemetry turns the go command's telemetry off in the configuration directory of a test whose HOME is now home. Call it after setting HOME and before the test's first go command.
//
// A fresh configuration directory has no telemetry mode, so the first go command in it finds telemetry on, claims the upload token, and forks a detached sidecar ("** telemetry **", working directory in that configuration directory; golang.org/x/telemetry start.go parent and startChild, reached from cmd/go through cmd/internal/telemetry.MaybeParent). The sidecar outlives the command and creates counter and report files there. A test whose cleanup removes home then fails with "directory not empty" when the sidecar writes during RemoveAll, or leaves a recreated tree behind when it writes after. In mode off a go command opens no counters and forks nothing.
//
// The mode file is the only input: GOTELEMETRY and GOTELEMETRYDIR are values `go env` reports ("non-settable" in `go help telemetry`; `go env -w` refuses them with "cannot be modified"), and cmd/go reads neither from the environment, so exporting one changes nothing.
//
// It writes only when the configuration directory lies inside home. Where XDG_CONFIG_HOME or APPDATA keeps it elsewhere the sidecar cannot write into home, and the mode of the user running `go test` must not change.
func DisableGoTelemetry(t testing.TB, home string) {
	t.Helper()
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(home, config); err != nil || !filepath.IsLocal(rel) {
		return
	}
	writeGoTelemetryOff(t, config)
}

// SetHome points HOME at home for the rest of the test and turns the go command's telemetry off there (DisableGoTelemetry). A test that moves HOME and can then run a go command, itself or through a pig build, calls it instead of t.Setenv("HOME", home).
func SetHome(t testing.TB, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	DisableGoTelemetry(t, home)
}

func writeGoTelemetryOff(t testing.TB, config string) {
	t.Helper()
	dir := filepath.Join(config, "go", "telemetry")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mode"), []byte("off"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// DisableGoTelemetryForChild is DisableGoTelemetry for a child process that is started with HOME=home, whatever HOME this process has: the mode file goes into the configuration directory the child derives from home (see ChildHomeEnv). Call it before the child starts, at the one place a test names the child's home. A child that inherits XDG_CONFIG_HOME or APPDATA from this process keeps its configuration there, outside home, and the mode file in home is then merely unused.
func DisableGoTelemetryForChild(t testing.TB, home string) {
	t.Helper()
	writeGoTelemetryOff(t, childConfigDir(home))
}

// ChildHomeEnv returns the environment entries that give a child process the scratch home home, for a caller that builds the child's Env by hand instead of inheriting os.Environ(): HOME (with USERPROFILE and APPDATA under home on Windows), the GOCACHE and GOMODCACHE this process pinned (KeepGoBuildCaches), and the go command's telemetry mode off in the configuration directory the child derives from them: home/.config, home/Library/Application Support on macOS, home/AppData/Roaming on Windows, home/lib on Plan 9. A caller that adds XDG_CONFIG_HOME for the child moves that directory out of home.
//
// A child that is itself a test binary runs testenv.ScopeTempDir from its TestMain, and KeepGoBuildCaches there runs `go env` unless both caches are set. In a scratch home that is the first go command, it forks the telemetry sidecar, and the sidecar keeps writing under home after the parent test removes it (macOS CI: TestRefreshedCredentialSurvivesALockTakenMeanwhile, "unlinkat .../Library/Application Support/go/telemetry: directory not empty"). The pinned caches make that child run no go command, and the mode file covers a go command the child starts itself (pig build, go build).
func ChildHomeEnv(t testing.TB, home string) []string {
	t.Helper()
	env := []string{"HOME=" + home}
	if runtime.GOOS == "windows" {
		env = append(env, "USERPROFILE="+home, "APPDATA="+childConfigDir(home))
	}
	for _, key := range []string{"GOCACHE", "GOMODCACHE"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	DisableGoTelemetryForChild(t, home)
	return env
}

// childConfigDir is the configuration directory of a child whose HOME is home and whose environment carries no XDG_CONFIG_HOME: ChildHomeEnv sets APPDATA to it on Windows.
func childConfigDir(home string) string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Roaming")
	case "darwin", "ios":
		return filepath.Join(home, "Library", "Application Support")
	case "plan9":
		return filepath.Join(home, "lib")
	default:
		return filepath.Join(home, ".config")
	}
}

// RequireNoGoTelemetryActivity fails the test when a go command has run with telemetry on in the scratch home home that ChildHomeEnv gave a child: the telemetry directory then holds counters (local) or the upload state a sidecar keeps, beside the mode file. The go command creates local before it exits, so the check needs no wait.
func RequireNoGoTelemetryActivity(t testing.TB, home string) {
	t.Helper()
	dir := filepath.Join(childConfigDir(home), "go", "telemetry")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "mode" {
			t.Fatalf("a go command ran with telemetry on in %q: it holds %q beside the mode file, and its sidecar goes on writing there after the test removes the home", dir, entry.Name())
		}
	}
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

// RequireIsolatedAgentEnv fails the test unless every variable IsolateAgentEnv sets points under the process temporary directory this package's TestMain scoped. It guards the TestMain wiring: without it, a test that builds a default path (a session directory, trust file or credential store) writes the agent directory the user's shell exported.
func RequireIsolatedAgentEnv(t testing.TB) {
	t.Helper()
	root := filepath.Clean(os.TempDir())
	for _, entry := range agentEnvDirs {
		got := filepath.Clean(os.Getenv(entry.key))
		if filepath.Dir(got) != root || filepath.Base(got) != entry.dir {
			t.Errorf("%s = %q, want %q: this package's TestMain must call testenv.ScopeTempDir or testenv.RunScoped", entry.key, got, filepath.Join(root, entry.dir))
		}
	}
	for _, key := range agentSessionEnv {
		if value, ok := os.LookupEnv(key); ok {
			t.Errorf("%s = %q, want it unset", key, value)
		}
	}
}
