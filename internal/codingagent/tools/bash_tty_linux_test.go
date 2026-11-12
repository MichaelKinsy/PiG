//go:build linux

package tools

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's bash tool spawns the shell with `detached: true` (core/tools/bash.ts createLocalShellOperations; utils/shell.ts killProcessTree kills `-pid`), which
// Node starts with setsid(2): the shell leads a new session and a new process group, and neither it nor anything it starts has a controlling terminal.
// A shell that stays in PiG's session keeps PiG's terminal. An interactive job-control shell (`zsh -i -c true; true`) opens /dev/tty, makes its own
// process group the terminal's foreground group and exits without handing the group back, so PiG's next read from the terminal raised SIGTTIN and the
// user's shell reported `suspended (tty input)` (issue #427).
//
// Pi gives the shell no controlling terminal, so a command cannot take PiG's terminal whichever way the command reaches the shell: through the bash tool,
// through user bash (`!cmd`, RPC bash, SDK executeBash) or through the local shell operations they share, and whether bash forks the program (`; true`)
// or replaces itself with it. The test runs as PiG does in a user's terminal; the test binary stands in for the interactive shell.
func TestBashCommandCannotTakeThePigTerminal(t *testing.T) {
	if !testenv.InTerminalSession(t) {
		return
	}
	grabber := testenv.TerminalGrabberHelper + "=1 " + bashSingleQuotedArg(os.Args[0])
	for _, caller := range []string{"bash tool", "user bash", "local shell operations"} {
		for _, form := range []struct{ name, command string }{
			{"forked program", grabber + "; true"},
			{"exec'd program", grabber},
		} {
			t.Run(caller+"/"+form.name, func(t *testing.T) {
				output := runThroughCaller(t, caller, form.command)
				if !strings.Contains(output, testenv.NoControllingTerminal) {
					t.Errorf("the command could open /dev/tty, Pi's shell has no controlling terminal; output %q", output)
				}
				testenv.RequireTerminalForeground(t)
			})
		}
	}
}

// runThroughCaller runs command the way the named caller does and returns the output it shows.
func runThroughCaller(t *testing.T, caller, command string) string {
	t.Helper()
	dir := t.TempDir()
	switch caller {
	case "bash tool":
		return runFileTool(t, &BashTool{CWD: dir}, t.Context(), map[string]any{"command": command}).Text()
	case "user bash":
		shell, err := GetShellConfig("")
		if err != nil {
			t.Fatal(err)
		}
		result, err := ExecuteBash(t.Context(), command, dir, shell, BashExecOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return result.Output
	case "local shell operations":
		var output strings.Builder
		if _, err := CreateLocalBashOperations(nil).Exec(t.Context(), command, dir, BashOperationsExecOptions{OnData: func(data []byte) { output.Write(data) }}); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	t.Fatalf("unknown caller %q", caller)
	return ""
}

// A shell that leads its own session also leads its own process group (setsid makes the process both), so abort and timeout still reach every
// descendant with kill(-pid), as Pi's killProcessTree does.
func TestBashShellLeadsItsOwnSessionAndProcessGroup(t *testing.T) {
	var output strings.Builder
	command := `set -- $(cut -d' ' -f1,5,6 /proc/$$/stat); if [ "$1" = "$2" ] && [ "$1" = "$3" ]; then echo leader; else echo "shared: pid=$1 pgrp=$2 session=$3"; fi`
	result, err := CreateLocalBashOperations(nil).Exec(t.Context(), command, t.TempDir(), BashOperationsExecOptions{OnData: func(data []byte) { output.Write(data) }})
	if err != nil || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("exec: %+v, %v", result, err)
	}
	if got := output.String(); got != "leader\n" {
		t.Fatalf("the shell ran as %q, want the leader of a session and of a process group", strings.TrimSpace(got))
	}
}

// The group kill reaches a descendant that holds the output pipe, on abort and on timeout, from the shell that is its own session leader.
func TestBashAbortAndTimeoutKillTheShellsDescendants(t *testing.T) {
	for _, how := range []string{"abort", "timeout"} {
		t.Run(how, func(t *testing.T) {
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "descendant.pid")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			options := BashOperationsExecOptions{}
			timeout := make(chan time.Time, 1)
			if how == "timeout" {
				options.Timeout = new(60.0)
				testHookTimeoutTimer = func(time.Duration) (<-chan time.Time, func()) { return timeout, func() {} }
				t.Cleanup(func() { testHookTimeoutTimer = nil })
			}
			done := make(chan error, 1)
			go func() {
				_, err := CreateLocalBashOperations(nil).Exec(ctx, "sleep 600 & echo $! > "+bashSingleQuotedArg(pidFile)+"; wait", dir, options)
				done <- err
			}()
			var pid int
			waitFor(t, func() bool {
				data, err := os.ReadFile(pidFile)
				if err != nil || !strings.HasSuffix(string(data), "\n") {
					return false
				}
				pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
				return err == nil
			})
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			if how == "abort" {
				cancel()
			} else {
				timeout <- time.Now()
			}
			select {
			case <-done:
			case <-time.After(testbudget.Wait(t)):
				t.Fatalf("the shell did not return after the %s", how)
			}
			waitFor(t, func() bool { return !processRunning(pid) })
		})
	}
}

// processRunning reports whether process pid runs: a zombie its parent never reaped has ended, as kill(pid, 0) cannot tell.
func processRunning(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	end := bytes.LastIndexByte(stat, ')')
	return end >= 0 && end+2 < len(stat) && stat[end+2] != 'Z'
}
