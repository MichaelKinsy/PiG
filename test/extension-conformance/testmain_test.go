package extensionconformance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var fixtureRoot, fixtureSourceRoot string

// busBuildRoot is the config root of every event-bus rig's Host, shared the way concurrent PiG sessions share ~/.pig: a fixture compiles once for the package run and its builds are reused by later rigs.
var busBuildRoot string

func TestMain(m *testing.M) {
	parent := ""
	if runtime.GOOS != "windows" {
		parent = "/tmp" // Unix socket paths must also fit macOS's 104-byte limit.
	}
	testRoot, err := os.MkdirTemp(parent, "pig-conf-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create isolated test home:", err)
		os.Exit(2)
	}
	if err := testenv.ScopeTempDir(testRoot); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(testRoot)
		os.Exit(2)
	}
	sourceRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve source SDK roots:", err)
		_ = os.RemoveAll(testRoot)
		os.Exit(2)
	}
	fixtureRoot, fixtureSourceRoot = testRoot, sourceRoot
	busBuildRoot = filepath.Join(testRoot, "bus-builds")
	// Each Host creates its own socket directory beneath this short package root.
	for key, value := range map[string]string{
		"XDG_RUNTIME_DIR": testRoot,
		"PIG_HOME":        filepath.Join(testRoot, "pig"),
		"PIG_SDK_GO_ROOT": filepath.Join(sourceRoot, "extensions", "sdk"),
		"PIG_SDK_PY_ROOT": filepath.Join(sourceRoot, "extensions", "sdk-py"),
		"PIG_SDK_RS_ROOT": filepath.Join(sourceRoot, "extensions", "sdk-rs"),
	} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "set isolated %s: %v\n", key, err)
			_ = os.RemoveAll(testRoot)
			os.Exit(2)
		}
	}
	ensureUserToolchainsOnPath()
	if os.Getenv("CARGO_TARGET_DIR") == "" {
		sdk := filepath.Join(sourceRoot, "extensions", "sdk-rs")
		target, err := testenv.SharedCargoTargetDir("conformance", filepath.Join(sdk, "Cargo.lock"), filepath.Join(sdk, "Cargo.toml"))
		if err == nil {
			err = os.Setenv("CARGO_TARGET_DIR", target)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "set package Cargo target:", err)
			_ = os.RemoveAll(testRoot)
			os.Exit(2)
		}
	}
	code := m.Run()
	if err := os.RemoveAll(testRoot); err != nil {
		fmt.Fprintln(os.Stderr, "remove isolated test home:", err)
		if code == 0 {
			code = 2
		}
	}
	os.Exit(code)
}

// ensureUserToolchainsOnPath appends the per-user toolchain directories (mise shims, rustup's cargo bin) for each SDK tool that PATH does not already resolve. Non-interactive shells such as ssh commands never source the profile that adds them, so a host that has node and cargo installed would otherwise report them missing. A tool already on PATH is never overridden.
func ensureUserToolchainsOnPath() {
	// The missing-tool probe runs with a deliberately empty PATH and must see the tools as absent.
	if os.Getenv("PIG_TEST_MISSING_CONFORMANCE_TOOL") != "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	candidates := map[string][]string{
		"node":  {filepath.Join(home, ".local", "share", "mise", "shims")},
		"cargo": {filepath.Join(home, ".local", "share", "mise", "shims"), filepath.Join(home, ".cargo", "bin")},
	}
	path := os.Getenv("PATH")
	for _, tool := range []string{"node", "cargo"} {
		if _, err := exec.LookPath(tool); err == nil {
			continue
		}
		for _, dir := range candidates[tool] {
			if _, err := os.Stat(filepath.Join(dir, testExecutableName(tool))); err != nil || strings.Contains(string(os.PathListSeparator)+path+string(os.PathListSeparator), string(os.PathListSeparator)+dir+string(os.PathListSeparator)) {
				continue
			}
			path += string(os.PathListSeparator) + dir
			_ = os.Setenv("PATH", path)
			break
		}
	}
}
