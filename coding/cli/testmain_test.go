package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// execEchoHelperEnv, when set to "1", makes this test binary behave like a
// portable `echo`: print the remaining arguments space-joined with a
// trailing newline, then exit, without running any tests. A test that needs
// to exec a real, natively executable command (through pi.exec, which spawns
// the requested command directly with no shell) sets this in the child's
// environment and passes os.Executable() (this same test binary) as the
// command, so the same fixture works on every OS the test binary runs on,
// including native Windows, instead of depending on /bin/echo or a
// cmd.exe-only builtin. Set to "hang", it prints "started" and waits for a
// signal to end it, for the tests of an exec call's abort signal.
const execEchoHelperEnv = "PIG_TEST_EXEC_ECHO_HELPER"

var fixtureRoot, fixtureSourceRoot string

// TestMain isolates the config root and points the SDK roots at this tree.
func TestMain(m *testing.M) {
	switch os.Getenv(execEchoHelperEnv) {
	case "1":
		fmt.Println(strings.Join(os.Args[1:], " "))
		os.Exit(0)
	case "hang":
		// A command that runs until a signal ends it.
		fmt.Println("started")
		time.Sleep(time.Minute)
		os.Exit(3)
	}
	if real := os.Getenv(commandShimRealEnv); real != "" {
		os.Exit(runCommandShim(real, os.Getenv(commandShimLogEnv)))
	}
	if log := os.Getenv(managerLogEnv); log != "" {
		os.Exit(runLoggingManager(log))
	}
	if root := os.Getenv(npmUpdateFixtureRootEnv); root != "" {
		if code, ok := runNpmUpdateFixture(root); ok {
			os.Exit(code)
		}
	}
	// PIG_TEST_STALL_DUMP (a duration, set by the Windows CI cli steps) bounds the setup before m.Run, which go test's -timeout
	// alarm does not cover: past it, every goroutine's stack goes to stderr and the binary exits, so a wedge names its frame.
	stopStallDump := func() {}
	if limit, err := time.ParseDuration(os.Getenv("PIG_TEST_STALL_DUMP")); err == nil && limit > 0 {
		stall := time.AfterFunc(limit, func() {
			buf := make([]byte, 64<<20)
			fmt.Fprintf(os.Stderr, "coding/cli TestMain did not reach m.Run within %s; goroutines:\n%s\n", limit, buf[:runtime.Stack(buf, true)])
			os.Exit(3)
		})
		stopStallDump = func() { stall.Stop() }
	}
	testRoot, err := os.MkdirTemp("", "pig-cmd-")
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
	for _, key := range []string{"PIG_CODING_AGENT_DIR", "PIG_CODING_AGENT_SESSION_DIR"} {
		if err := os.Unsetenv(key); err != nil {
			fmt.Fprintf(os.Stderr, "clear inherited %s: %v\n", key, err)
			_ = os.RemoveAll(testRoot)
			os.Exit(2)
		}
	}
	for key, value := range map[string]string{
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
	fixtureRoot, fixtureSourceRoot = testRoot, sourceRoot
	// Rust SDK dependencies compile once per machine into a persistent target keyed by the SDK lock, not once per package run (more than 10 minutes under load).
	if os.Getenv("CARGO_TARGET_DIR") == "" {
		sdk := filepath.Join(sourceRoot, "extensions", "sdk-rs")
		target, err := testenv.SharedCargoTargetDir("cli", filepath.Join(sdk, "Cargo.lock"), filepath.Join(sdk, "Cargo.toml"))
		if err == nil {
			err = os.Setenv("CARGO_TARGET_DIR", target)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "set package Cargo target:", err)
			_ = os.RemoveAll(testRoot)
			os.Exit(2)
		}
	}
	stopStallDump()
	code := m.Run()
	if err := os.RemoveAll(testRoot); err != nil {
		fmt.Fprintln(os.Stderr, "remove isolated test home:", err)
		if code == 0 {
			code = 2
		}
	}
	os.Exit(code)
}
