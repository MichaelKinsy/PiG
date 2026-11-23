//go:build unix

package testenv

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const sessionExitFixtureEnv = "PIG_TEST_SESSION_EXIT_FIXTURE"

func init() {
	switch os.Getenv(sessionExitFixtureEnv) {
	case "exit-now":
		os.Exit(0)
	case "hold-zombie":
		// Spawn a child in the same session that exits at once and stay alive
		// without reaping it until stdin closes, so the parent observes a real
		// zombie process alongside a live session leader.
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), sessionExitFixtureEnv+"=exit-now")
		if err := child.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "start exit-now child: %v\n", err)
			os.Exit(2)
		}
		waitForZombieState(child.Process.Pid)
		fmt.Printf("%d %d\n", os.Getpid(), child.Process.Pid)
		var buf [1]byte
		_, _ = os.Stdin.Read(buf[:])
		_ = child.Wait()
		os.Exit(0)
	}
}

func waitForZombieState(pid int) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if err == nil && strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeFakePgrepOnPATH(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pgrep")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake pgrep: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func TestLiveSessionProcessesRealSessionAndZombie(t *testing.T) {
	// On darwin, simulate macOS 15 where /usr/bin/pgrep rejects -s with exit status 2
	// and assert the fake pgrep is never executed. On other Unix platforms (Linux),
	// LiveSessionProcesses uses the real pgrep and ps.
	var pgrepMarker string
	if runtime.GOOS == "darwin" {
		pgrepMarker = filepath.Join(t.TempDir(), "pgrep-called")
		writeFakePgrepOnPATH(t, fmt.Sprintf(`#!/bin/sh
touch %q
echo "pgrep: illegal option -- s" >&2
echo "usage: pgrep [-Lfilnoqvx] [-d delim] [-F pidfile] [-G gid]" >&2
echo "             [-P ppid] [-U uid] [-g pgrp] [-t tty] [-u euid]" >&2
echo "             pattern ..." >&2
exit 2
`, pgrepMarker))
	}

	t.Run("live_then_reaped", func(t *testing.T) {
		cmd := exec.Command("sleep", "30")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			t.Fatalf("start sleep in new session: %v", err)
		}
		sid := cmd.Process.Pid
		reaped := false
		defer func() {
			if !reaped {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}()

		got, err := LiveSessionProcesses(sid)
		if err != nil {
			t.Fatalf("LiveSessionProcesses(%d) while live: %v", sid, err)
		}
		if !slices.Equal(got, []int{sid}) {
			t.Fatalf("LiveSessionProcesses(%d) while live = %v, want [%d]", sid, got, sid)
		}

		if err := cmd.Process.Kill(); err != nil {
			t.Fatalf("kill sleep(%d): %v", sid, err)
		}
		_ = cmd.Wait()
		reaped = true

		got, err = LiveSessionProcesses(sid)
		if err != nil {
			t.Fatalf("LiveSessionProcesses(%d) after reap: %v", sid, err)
		}
		if len(got) != 0 {
			t.Fatalf("LiveSessionProcesses(%d) after reap = %v, want empty", sid, got)
		}
	})

	t.Run("zombie_child_not_counted", func(t *testing.T) {
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), sessionExitFixtureEnv+"=hold-zombie")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatalf("stdin pipe: %v", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("stdout pipe: %v", err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatalf("start hold-zombie fixture: %v", err)
		}
		reaped := false
		defer func() {
			_ = stdin.Close()
			if !reaped {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}()

		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil {
			t.Fatalf("read hold-zombie pids: %v", err)
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("hold-zombie printed %q, want two pids", line)
		}
		leaderPID, err := strconv.Atoi(fields[0])
		if err != nil {
			t.Fatalf("parse leader pid %q: %v", fields[0], err)
		}
		zombiePID, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatalf("parse zombie pid %q: %v", fields[1], err)
		}

		got, err := LiveSessionProcesses(leaderPID)
		if err != nil {
			t.Fatalf("LiveSessionProcesses(%d) with zombie child %d: %v", leaderPID, zombiePID, err)
		}
		if !slices.Equal(got, []int{leaderPID}) {
			t.Fatalf("LiveSessionProcesses(%d) = %v, want only leader [%d] (zombie child %d must not count)", leaderPID, got, leaderPID, zombiePID)
		}

		_ = stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Fatalf("hold-zombie wait: %v", err)
		}
		reaped = true

		got, err = LiveSessionProcesses(leaderPID)
		if err != nil {
			t.Fatalf("LiveSessionProcesses(%d) after leader exit: %v", leaderPID, err)
		}
		if len(got) != 0 {
			t.Fatalf("LiveSessionProcesses(%d) after leader exit = %v, want empty", leaderPID, got)
		}
	})

	if pgrepMarker != "" {
		if _, err := os.Stat(pgrepMarker); err == nil {
			t.Fatalf("darwin LiveSessionProcesses executed pgrep (marker %s exists)", pgrepMarker)
		}
	}
}
