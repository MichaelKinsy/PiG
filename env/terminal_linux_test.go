//go:build linux

package env

// Reviewed against Pi's Rust daemon at packages/env 74613de6 (daemon/src/pty.rs): its terminals' environment and
// signal dispositions, as Rust's Command and the pre_exec hook set them.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// terminalOutput runs argv in a terminal of connection and returns its output without carriage returns.
func terminalOutput(t *testing.T, connection *Connection, options TerminalOptions) string {
	t.Helper()
	output := newCollector()
	options.Cwd, options.Cols, options.Rows, options.OnData = t.TempDir(), 80, 24, output.onData
	terminal, err := OpenTerminal(background, connection, options)
	if err != nil {
		t.Fatal(err)
	}
	if code, err := waitExit(t, terminal); err != nil || code != 0 {
		t.Fatalf("exit = %d, %v; output %q", code, err, output.bytes())
	}
	return strings.ReplaceAll(string(output.bytes()), "\r", "")
}

// Rust's Command builds a changed environment as a map ordered by name (CommandEnv::capture), so the program sees its
// variables sorted, with or without the daemon's own.
func TestTerminalEnvironmentIsOrderedByName(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, _ := terminalConnection(t)
		inherit := false
		env := map[string]string{"B": "1", "A": "2", "C": "3", "Z": "0", "M": "x", "a": "4", "_": "5"}
		got := terminalOutput(t, connection, TerminalOptions{Argv: []string{"/usr/bin/env"}, Env: env, InheritEnv: &inherit})
		if want := "A=2\nB=1\nC=3\nM=x\nZ=0\n_=5\na=4\n"; got != want {
			t.Fatalf("environment without inheritance = %q, want %q", got, want)
		}
		// The environment as execve gave it, NUL-separated: a value may hold newlines (an exported shell function).
		got = terminalOutput(t, connection, TerminalOptions{Argv: []string{"cat", "/proc/self/environ"}, Env: env})
		var names []string
		for entry := range strings.SplitSeq(strings.TrimSuffix(got, "\x00"), "\x00") {
			name, _, _ := strings.Cut(entry, "=")
			names = append(names, name)
		}
		if !slices.IsSorted(names) || !slices.Contains(names, "Z") || !slices.Contains(names, "PATH") {
			t.Fatalf("inherited environment is not ordered by name: %q", names)
		}
	})
}

// signalMask is the mask a /proc/<pid>/status line such as "SigIgn:\t0000000080000000" holds.
func signalMask(line, field string) (uint64, error) {
	return strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, field+":")), 16, 64)
}

// pty.rs resets every signal its hook covers to its default before exec, so a daemon started with SIGINT and SIGHUP ignored (from a
// background job or nohup) still gives the terminal's program Ctrl-C and hangup.
//
// The reset covers signals 1 through 31 only (`for signal in 1..32`, as the daemon's own loop does): a signal above that
// range, which glibc reserves for its threads (32 and 33), keeps the disposition the daemon inherited. GNU make 4.4 starts
// its recipes with 32 and 33 ignored, so a test run under `make test-fast` reaches the daemon with SigIgn 0x180000000 and
// the program ends with 0x80000000 under Pi's daemon and ours alike; the assertion is the daemon's contract (signals 1
// through 31), not the launcher's state.
func TestTerminalProgramGetsDefaultSignalsWhenTheDaemonStartedIgnoringThem(t *testing.T) {
	// resetSignals is the signals 1 through 31, the ones pty.rs's pre_exec hook sets to their default (`for signal in 1..32`).
	// A signal above them keeps the disposition the program's ancestry gave it, in Pi's daemon as in PiG's: glibc's posix_spawn,
	// which GNU make 4.4 runs its recipes with, leaves SIGCANCEL (32) and SIGSETXID (33), signals glibc keeps for itself and
	// sigaction refuses to change, ignored in every process below a recipe, so a test run by `make test` sees 0x80000000.
	const resetSignals = 1<<31 - 1 // bit n-1 is signal n
	eachDaemon(t, func(t *testing.T) {
		connection := NewConnection(ConnectionOptions{Command: []string{"sh", "-c", `trap '' INT HUP; exec "$0" "$@"`, terminalDaemon}})
		t.Cleanup(connection.Close)
		got := terminalOutput(t, connection, TerminalOptions{Argv: []string{"grep", "^SigIgn:", "/proc/self/status"}})
		mask, err := signalMask(got, "SigIgn")
		if err != nil || mask&resetSignals != 0 {
			t.Fatalf("ignored signals of the terminal's program = %q, want none of the signals 1 to 31", got)
		}
	})
}

// pty.rs sets an empty signal mask before exec (as libuv does), so a daemon started with signals blocked (a process manager, a
// debugger, a shell that blocks them) does not hand the block to the terminal's program. SIGUSR1, SIGUSR2 and SIGWINCH are
// blocked by the wrapper; Go's own runtime leaves them blocked in a child unless the fork happens under an empty mask.
func TestTerminalProgramGetsAnEmptySignalMaskWhenTheDaemonStartedWithSignalsBlocked(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 blocks the signals before the daemon starts (a test prerequisite): %v", err)
	}
	eachDaemon(t, func(t *testing.T) {
		const block = `import os, signal, sys
signal.pthread_sigmask(signal.SIG_BLOCK, [signal.SIGUSR1, signal.SIGUSR2, signal.SIGWINCH])
os.execv(sys.argv[1], sys.argv[1:])`
		connection := NewConnection(ConnectionOptions{Command: []string{python, "-c", block, terminalDaemon}})
		t.Cleanup(connection.Close)
		// The precondition: the daemon's own main thread carries the block (a terminal's program's parent is the daemon).
		const blocked = 1<<(syscall.SIGUSR1-1) | 1<<(syscall.SIGUSR2-1) | 1<<(syscall.SIGWINCH-1)
		parent := terminalOutput(t, connection, TerminalOptions{Argv: []string{"sh", "-c", "grep ^SigBlk: /proc/$PPID/status"}})
		mask, err := signalMask(parent, "SigBlk")
		if err != nil || mask&blocked != blocked {
			t.Fatalf("the daemon's blocked signals = %q, want SIGUSR1, SIGUSR2 and SIGWINCH among them", parent)
		}
		got := terminalOutput(t, connection, TerminalOptions{Argv: []string{"grep", "^SigBlk:", "/proc/self/status"}})
		if want := "SigBlk:\t0000000000000000\n"; got != want {
			t.Fatalf("blocked signals of the terminal's program = %q, want %q", got, want)
		}
	})
}

// Rust's Command passes the daemon's environment as it is when the request changes nothing (CommandEnv::
// capture_if_changed): the program's initial environment is byte for byte the daemon's, in the daemon's order.
func TestTerminalEnvironmentIsTheDaemonsOwnWhenTheRequestChangesNothing(t *testing.T) {
	eachDaemon(t, func(t *testing.T) {
		connection, _ := terminalConnection(t)
		// /proc/<pid>/environ is the environment execve gave the process, before the shell changes any of it.
		got := terminalOutput(t, connection, TerminalOptions{Argv: []string{"/bin/sh", "-c", `if cmp -s /proc/$$/environ /proc/$PPID/environ; then echo SAME; else echo DIFFERENT; fi`}})
		if got != "SAME\n" {
			t.Fatalf("the program's environment against the daemon's: %q", got)
		}
	})
}

// pty.rs reads /proc/<pid>/comm with read_to_string, which fails on bytes that are not UTF-8: the kernel cuts a name
// at 15 bytes, so a long name of two-byte characters loses half a character and has no name.
func TestTerminalForegroundOfANameCutInsideACharacterHasNoName(t *testing.T) {
	program := filepath.Join(t.TempDir(), strings.Repeat("é", 9))
	source, err := os.ReadFile("/bin/sleep")
	if err != nil {
		t.Fatal(err)
	}
	writeProgram(t, program, string(source))
	eachDaemon(t, func(t *testing.T) {
		connection, cwd := terminalConnection(t)
		output := newCollector()
		terminal, err := OpenTerminal(background, connection, TerminalOptions{
			Cwd: cwd, Argv: []string{"/bin/sh", "-i"}, Env: map[string]string{"PS1": "$ ", "TERM": "dumb"},
			Cols: 80, Rows: 24, OnData: output.onData,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(terminal.Close)
		terminal.Write("echo READY\n")
		output.waitFor(t, "READY")
		terminal.Write("'" + program + "' 100\n")
		var job *TerminalForeground
		eventually(t, "the job coming to the foreground", func() bool {
			job, err = terminal.Foreground(background)
			if err != nil {
				t.Fatal(err)
			}
			return job != nil
		})
		// The shell's child takes the terminal (tcsetpgrp) before it execs the program, so for a moment the job's group
		// still carries the shell's own comm and is named after it. Wait for the group leader to finish its exec: its
		// comm becomes the program's name cut at 15 bytes, which the kernel reports the instant the exec is done.
		wantComm := filepath.Base(program)[:15]
		eventually(t, fmt.Sprintf("the foreground job %+v execing the program", job), func() bool {
			comm, err := os.ReadFile("/proc/" + strconv.Itoa(job.Pid) + "/comm")
			if err != nil {
				t.Fatal(err)
			}
			return strings.TrimSuffix(string(comm), "\n") == wantComm
		})
		foreground, err := terminal.Foreground(background)
		if err != nil {
			t.Fatal(err)
		}
		if foreground == nil || foreground.Pid != job.Pid {
			t.Fatalf("foreground changed from %+v to %+v", job, foreground)
		}
		if foreground.Name != "" {
			t.Fatalf("foreground name = %q, want none", foreground.Name)
		}
	})
}

// A program that does not start fails the open with Rust's io::Error text, `<system text> (os error N)`, and execvp
// passes over a PATH entry that has the name but cannot run it, reporting EACCES when no entry runs it.
func TestTerminalThatCannotStartFailsWithTheSystemsWords(t *testing.T) {
	notExecutable := t.TempDir()
	if err := os.WriteFile(filepath.Join(notExecutable, "program"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executable := t.TempDir()
	writeProgram(t, filepath.Join(executable, "program"), "#!/bin/sh\nexit 4\n")
	missing := filepath.Join(t.TempDir(), "missing")
	eachDaemon(t, func(t *testing.T) {
		connection, cwd := terminalConnection(t)
		inherit := false
		for _, test := range []struct {
			name string
			argv []string
			path string
			want string
		}{
			{"not on PATH", []string{"program"}, missing, "No such file or directory (os error 2)"},
			{"a missing path", []string{missing}, missing, "No such file or directory (os error 2)"},
			{"a directory", []string{cwd}, missing, "Permission denied (os error 13)"},
			{"on PATH but not executable", []string{"program"}, missing + ":" + notExecutable, "Permission denied (os error 13)"},
			{"not executable, then executable", []string{"program"}, notExecutable + ":" + executable, ""},
		} {
			terminal, err := OpenTerminal(background, connection, TerminalOptions{
				Cwd: cwd, Argv: test.argv, Env: map[string]string{"PATH": test.path}, InheritEnv: &inherit,
				Cols: 80, Rows: 24, OnData: func([]byte) {},
			})
			if test.want == "" {
				if err != nil {
					t.Fatalf("%s: %v", test.name, err)
				}
				if code, err := waitExit(t, terminal); err != nil || code != 4 {
					t.Fatalf("%s: exit = %d, %v", test.name, code, err)
				}
				continue
			}
			if remoteCode(err) != "spawn_error" || err.Error() != test.want {
				t.Errorf("%s: error = %v, want spawn_error %q", test.name, err, test.want)
			}
		}
	})
}
