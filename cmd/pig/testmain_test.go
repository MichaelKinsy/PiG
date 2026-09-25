package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// execEchoHelperEnv, when set to "1", makes this test binary behave like a
// portable `echo`: print the remaining arguments space-joined with a
// trailing newline, then exit, without running any tests. A test that needs
// to exec a real, natively executable command (through pi.exec, which spawns
// the requested command directly with no shell) sets this in the child's
// environment and passes os.Executable() (this same test binary) as the
// command, so the same fixture works on every OS the test binary runs on,
// including native Windows, instead of depending on /bin/echo or a
// cmd.exe-only builtin.
const execEchoHelperEnv = "PIG_TEST_EXEC_ECHO_HELPER"

// TestMain isolates the config root and points the SDK roots at this tree.
func TestMain(m *testing.M) {
	if os.Getenv(execEchoHelperEnv) == "1" {
		fmt.Println(strings.Join(os.Args[1:], " "))
		os.Exit(0)
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
	testRoot, err := os.MkdirTemp("", "pig-command-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create isolated test home:", err)
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
	code := m.Run()
	if err := os.RemoveAll(testRoot); err != nil {
		fmt.Fprintln(os.Stderr, "remove isolated test home:", err)
		if code == 0 {
			code = 2
		}
	}
	os.Exit(code)
}
