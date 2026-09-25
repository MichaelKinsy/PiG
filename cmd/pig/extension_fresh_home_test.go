package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A released pig binary carries its extension SDKs embedded and stages them
// into the config root. A user with no PiG checkout must be able to scaffold a
// Go, Rust, or Python extension in one HOME and load it from a fresh HOME,
// including the pre-trust load that runs before the session starts.
func TestFreshHomeLoadsScaffoldedExtensionsWithoutCheckout(t *testing.T) {
	binary := buildReleasePigBinary(t)
	toolchainEnv := freshHomeToolchainEnv(t)
	for _, lang := range []string{"go", "rust", "python"} {
		t.Run(lang, func(t *testing.T) {
			work := t.TempDir()
			name := "fresh" + lang
			source := filepath.Join(work, name)
			initHome := t.TempDir()
			runFreshHomePig(t, binary, toolchainEnv, initHome, work, "extension", "init", source, "--lang", lang)

			project := trustProjectFixture(t)
			loadHome := t.TempDir()
			output := runFreshHomePig(t, binary, append(toolchainEnv, "PIG_STARTUP_TRACE=1"), loadHome, project, "-ne", "-e", source, "--list-models")
			if strings.Contains(output, "cannot locate") || strings.Contains(output, "warning:") {
				t.Fatalf("fresh HOME load reported an SDK failure:\n%s", output)
			}
			handshake := strings.Index(output, "extension."+name+".handshake-done")
			preload := strings.Index(output, "trust-preload-done")
			if handshake < 0 || preload < 0 || handshake > preload {
				t.Fatalf("extension %s did not load before the pre-trust load finished:\n%s", name, output)
			}
		})
	}
}

// `pig login --list` inspects configured extensions for OAuth providers before
// any session starts, so it must stage the SDKs it builds against too.
func TestFreshHomeLoginListInspectsScaffoldedExtension(t *testing.T) {
	binary := buildReleasePigBinary(t)
	toolchainEnv := freshHomeToolchainEnv(t)
	work := t.TempDir()
	source := filepath.Join(work, "freshlogin")
	runFreshHomePig(t, binary, toolchainEnv, t.TempDir(), work, "extension", "init", source, "--lang", "go")

	loadHome := t.TempDir()
	agentDir := filepath.Join(loadHome, ".pig", "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(map[string][]string{"extensions": {source}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	output := runFreshHomePig(t, binary, toolchainEnv, loadHome, work, "login", "--list", "--json")
	var listed struct {
		Diagnostics []authInspectionDiagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(output), &listed); err != nil {
		t.Fatalf("decode login --list output: %v\n%s", err, output)
	}
	if len(listed.Diagnostics) != 0 {
		t.Fatalf("fresh HOME login --list could not inspect the extension: %+v", listed.Diagnostics)
	}
}

// buildReleasePigBinary builds pig the way a release does: no cgo, trimmed
// paths, and no VCS stamp, so nothing in the binary points back at this tree.
func buildReleasePigBinary(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "pig")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.CommandContext(testbudget.Context(t), "go", "build", "-trimpath", "-buildvcs=false", "-o", out, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build release pig: %v\n%s", err, data)
	}
	return out
}

// freshHomeToolchainEnv keeps the language toolchains and their caches, which a
// user's machine has, and drops every variable that could point pig at this
// checkout or at an existing config root.
func freshHomeToolchainEnv(t *testing.T) []string {
	t.Helper()
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "TERM=dumb"}
	keep := []string{"TMPDIR", "GOPATH", "GOPROXY", "GOSUMDB", "GOTOOLCHAIN", "GOFLAGS", "UV_CACHE_DIR", "UV_PYTHON_INSTALL_DIR", "SSL_CERT_FILE"}
	if runtime.GOOS == "windows" {
		// Every Windows session has these. Without TEMP and TMP a process's
		// temporary directory is the Windows directory, which a user cannot
		// write, and programs locate the system through SystemRoot. Toolchains
		// live in the profile's application data: uv's managed Pythons in
		// APPDATA, the Store Python aliases in LOCALAPPDATA. PiG keeps no
		// configuration there.
		keep = append(keep, "SystemRoot", "SystemDrive", "windir", "TEMP", "TMP", "PATHEXT", "ComSpec", "APPDATA", "LOCALAPPDATA")
	}
	for _, key := range keep {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	for key, fallback := range map[string]string{"CARGO_HOME": ".cargo", "RUSTUP_HOME": ".rustup"} {
		value := os.Getenv(key)
		if value == "" {
			value = filepath.Join(realHome, fallback)
		}
		env = append(env, key+"="+value)
	}
	out, err := exec.Command("go", "env", "GOCACHE", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	caches := strings.Fields(string(out))
	if len(caches) != 2 {
		t.Fatalf("go env GOCACHE GOMODCACHE = %q", out)
	}
	return append(env, "GOCACHE="+caches[0], "GOMODCACHE="+caches[1])
}

// runFreshHomePig bounds each run by the go test deadline alone: a fresh HOME
// has no build cache, and a cold Cargo build of the SDK's dependencies on a
// loaded machine outlasts the default test budget.
func runFreshHomePig(t *testing.T, binary string, env []string, home, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), binary, args...)
	cmd.Dir = dir
	cmd.Env = append(append([]string(nil), env...), "HOME="+home)
	if runtime.GOOS == "windows" {
		// os.UserHomeDir, and Node's os.homedir, read USERPROFILE on Windows.
		cmd.Env = append(cmd.Env, "USERPROFILE="+home)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pig %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
