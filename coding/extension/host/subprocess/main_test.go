package subprocess

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const (
	nodePreflightHelperFIFOEnv  = "PIG_TEST_NODE_PREFLIGHT_FIFO"
	fakeProviderBridgeNodeEnv   = "PIG_TEST_FAKE_PROVIDER_BRIDGE_NODE"
	fakeProviderBridgeScriptEnv = "PIG_TEST_FAKE_PROVIDER_BRIDGE_SCRIPT"
)

var fixtureRoot string

func TestMain(m *testing.M) {
	if fifoPath := os.Getenv(nodePreflightHelperFIFOEnv); fifoPath != "" {
		fifo, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, "open Node preflight test FIFO:", err)
			os.Exit(2)
		}
		if _, err := fmt.Fprintln(fifo, os.Getpid()); err != nil {
			fmt.Fprintln(os.Stderr, "signal Node preflight test parent:", err)
			os.Exit(2)
		}
		_ = fifo.Close()
		for {
			time.Sleep(time.Hour)
		}
	}
	if nodePath := os.Getenv(fakeProviderBridgeNodeEnv); nodePath != "" {
		// `pig provider-bridge` stand-in: the shim spawns PIG_HARNESS_BINARY directly, and Windows runs only executables, not `#!/bin/sh` scripts.
		script := os.Getenv(fakeProviderBridgeScriptEnv)
		_ = os.Unsetenv(fakeProviderBridgeNodeEnv)
		bridge := exec.Command(nodePath, script)
		bridge.Stdin, bridge.Stdout, bridge.Stderr = os.Stdin, os.Stdout, os.Stderr
		err := bridge.Run()
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			os.Exit(exit.ExitCode())
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "run fake provider bridge:", err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	testRoot, err := os.MkdirTemp("", "pig-sp-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create isolated test home:", err)
		os.Exit(2)
	}
	if err := testenv.ScopeTempDir(testRoot); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(testRoot)
		os.Exit(2)
	}
	for key, value := range map[string]string{
		"PIG_HOME": filepath.Join(testRoot, "pig"),
	} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "set isolated %s: %v\n", key, err)
			_ = os.RemoveAll(testRoot)
			os.Exit(2)
		}
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve source SDK roots:", err)
		_ = os.RemoveAll(testRoot)
		os.Exit(2)
	}
	for key, path := range map[string]string{
		"PIG_SDK_GO_ROOT": filepath.Join(root, "extensions", "sdk"),
		"PIG_SDK_PY_ROOT": filepath.Join(root, "extensions", "sdk-py"),
		"PIG_SDK_RS_ROOT": filepath.Join(root, "extensions", "sdk-rs"),
	} {
		if err := os.Setenv(key, path); err != nil {
			fmt.Fprintf(os.Stderr, "set %s: %v\n", key, err)
			_ = os.RemoveAll(testRoot)
			os.Exit(2)
		}
	}
	fixtureRoot = filepath.Join(testRoot, "fixtures")
	// Rust SDK dependencies compile once per machine into a persistent target keyed by the SDK lock, not once per package run (more than 10 minutes under load).
	if os.Getenv("CARGO_TARGET_DIR") == "" {
		sdk := filepath.Join(root, "extensions", "sdk-rs")
		target, err := testenv.SharedCargoTargetDir("subprocess", filepath.Join(sdk, "Cargo.lock"), filepath.Join(sdk, "Cargo.toml"))
		if err == nil {
			err = os.Setenv("CARGO_TARGET_DIR", target)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "set package Cargo target:", err)
			_ = os.RemoveAll(testRoot)
			os.Exit(2)
		}
	}
	code := testenv.RunWithoutGoroutineLeaks(m)
	if err := os.RemoveAll(testRoot); err != nil {
		fmt.Fprintln(os.Stderr, "remove isolated test home:", err)
		if code == 0 {
			code = 2
		}
	}
	os.Exit(code)
}
