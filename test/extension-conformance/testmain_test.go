package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	if os.Getenv("CARGO_TARGET_DIR") == "" {
		if err := os.Setenv("CARGO_TARGET_DIR", filepath.Join(testRoot, "cargo-target")); err != nil {
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
