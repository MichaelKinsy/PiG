//go:build !windows

// The tests kill the harness with POSIX signals (SIGTERM, SIGKILL), which Windows has no counterpart for.
package subprocess

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// harnessChildAlive reports whether pid is a live process; a zombie awaiting an init that does not reap is dead.
func harnessChildAlive(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	stat := strings.TrimSpace(string(out))
	return stat != "" && !strings.HasPrefix(stat, "Z")
}

func startHarnessEntry(t *testing.T, body string) (*exec.Cmd, string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	root := t.TempDir()
	pidFile := filepath.Join(root, "pid")
	binary := filepath.Join(root, "pig")
	script := "#!/bin/sh\necho $$ > '" + pidFile + ".tmp' && mv '" + pidFile + ".tmp' '" + pidFile + "'\n" + body
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, filepath.Join("runtime-node", "harness", "cli.mjs"))
	cmd.Env = append(os.Environ(), "PIG_HARNESS_BINARY="+binary)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd, pidFile
}

func waitHarnessChildPID(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the harness never started its binary")
	return 0
}

// Pi's subagent example ends a spawned child with TERM and then KILL; in Pi the child is pi, so KILL ends it. Here the child is the entry's own child, which cannot be sent the KILL the entry receives.
func TestHarnessEntryKillEndsItsBinary(t *testing.T) {
	cmd, pidFile := startHarnessEntry(t, "trap '' TERM\nwhile :; do sleep 0.1; done\n")
	pid := waitHarnessChildPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if !harnessChildAlive(pid) {
		t.Fatal("the binary ended on TERM, which it traps")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for harnessChildAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatal("the binary survived KILL of the harness entry")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHarnessEntryKeepsBinaryExitStatus(t *testing.T) {
	cmd, pidFile := startHarnessEntry(t, "exit 7\n")
	waitHarnessChildPID(t, pidFile)
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("entry exit = %v, want status 7", err)
	}
}

// The watchdog kills only a process that started when the binary did, so a binary that is already gone (its pid free, perhaps reused) ends the watchdog without a kill.
func TestHarnessWatchdogIgnoresGoneBinary(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, filepath.Join("runtime-node", "harness", "cli.mjs"))
	cmd.Env = append(os.Environ(), "PIG_HARNESS_WATCHDOG="+strconv.Itoa(gone.Process.Pid))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watchdog = %v, want a clean exit", err)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the watchdog outlived the binary it watches")
	}
}
