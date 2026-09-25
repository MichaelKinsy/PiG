package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// managerLogEnv makes a copy of this test binary a package manager that
// appends each invocation's arguments to the named file and succeeds.
const managerLogEnv = "PIG_TEST_MANAGER_LOG"

// runLoggingManager is the package manager managerLogEnv selects.
func runLoggingManager(log string) int {
	file, err := os.OpenFile(log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logging manager:", err)
		return 2
	}
	_, err = fmt.Fprintln(file, strings.Join(os.Args[1:], " "))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "logging manager:", err)
		return 2
	}
	return 0
}

// npmUpdateFixtureRootEnv names the global node_modules root of the npm-owned
// installation TestWindowsNpmSelfUpdateReplacesTheRunningInstallation builds.
// Copies of this test binary play both of its programs there.
const npmUpdateFixtureRootEnv = "PIG_TEST_NPM_UPDATE_ROOT"

// runNpmUpdateFixture runs the program this binary was copied to be: pig runs
// `pig update`, and npm answers `npm root -g` and replaces root/pig on install.
// It reports false for any other name.
func runNpmUpdateFixture(root string) (int, bool) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "npm update fixture:", err)
		return 2, true
	}
	switch strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe") {
	case "pig":
		return runSelfUpdate(false), true
	case "npm":
		return runFakeNpm(root, os.Args[1:]), true
	}
	return 0, false
}

// runFakeNpm removes the installed package directory and writes the new
// package's pig.exe, recording the install arguments as its content.
func runFakeNpm(root string, args []string) int {
	if slices.Equal(args, []string{"root", "-g"}) {
		fmt.Println(root)
		return 0
	}
	if len(args) == 0 || args[0] != "install" {
		fmt.Fprintf(os.Stderr, "fake npm: unexpected arguments %q\n", args)
		return 2
	}
	pkg := filepath.Join(root, "pig")
	if err := os.RemoveAll(pkg); err != nil {
		fmt.Fprintln(os.Stderr, "fake npm:", err)
		return 1
	}
	bin := filepath.Join(pkg, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "fake npm:", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(bin, "pig.exe"), []byte(strings.Join(args, " ")), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "fake npm:", err)
		return 1
	}
	return 0
}

// copyTestBinary copies the running test binary to path. A hard link would
// share the running image, which Windows then refuses to delete.
func copyTestBinary(t *testing.T, path string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
