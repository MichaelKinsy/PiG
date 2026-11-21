//go:build !windows

// The tests kill the harness with POSIX signals (SIGTERM, SIGKILL), which Windows has no counterpart for.
package subprocess

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/execfile"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
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

// harnessParent is the pid of pid's parent, 0 once pid is gone.
func harnessParent(pid int) int {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	ppid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return ppid
}

// harnessEntrySpec is one run of the harness entry (runtime-node/harness/cli.mjs) over a stub binary: a shell script that runs prelude, publishes its pid in $ROOT/pid, then runs body.
// $ROOT, in prelude and body, is the test's temp directory. The stub publishes the pid with the shell's own echo, never with a command it would start: the supervisor kills the stub with SIGKILL, and a helper process of the stub (a mv) outlives it and changes the directory while the test removes it. A reader that finds the file empty, between its creation and the write, tries again.
type harnessEntrySpec struct {
	prelude, body string
	args          []string
	env           func(root string) []string
	setup         func(root string)
}

func newHarnessEntry(t *testing.T, spec harnessEntrySpec) (cmd *exec.Cmd, root string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	root = t.TempDir()
	binary := filepath.Join(root, "pig")
	script := "#!/bin/sh\nROOT='" + root + "'\n" + spec.prelude + "echo $$ > \"$ROOT/pid\"\n" + spec.body
	if err := execfile.Write(binary, os.O_CREATE|os.O_TRUNC, 0o700, execfile.Bytes([]byte(script))); err != nil {
		t.Fatal(err)
	}
	if spec.setup != nil {
		spec.setup(root)
	}
	cmd = exec.Command(node, append([]string{filepath.Join("runtime-node", "harness", "cli.mjs")}, spec.args...)...)
	cmd.Env = append(os.Environ(), "PIG_HARNESS_BINARY="+binary)
	if spec.env != nil {
		cmd.Env = append(cmd.Env, spec.env(root)...)
	}
	return cmd, root
}

// awaitHarnessEnd waits for pid to end: its exit is the event, and the budget only fails a hang.
func awaitHarnessEnd(t *testing.T, pid int, what string) {
	t.Helper()
	budget := testbudget.Wait(t)
	start := time.Now()
	for harnessChildAlive(pid) {
		if time.Since(start) > budget {
			t.Fatalf("%s (pid %d) was still running %s after the entry was killed", what, pid, budget)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// harnessSupervisor is the process that holds the kill of the binary while the entry lives: the binary's parent. The entry cannot be it: SIGKILL of the entry runs no code, so a binary the entry started itself is orphaned the moment the entry dies, unless a helper it started later is already armed.
func harnessSupervisor(t *testing.T, binary, entry int) int {
	t.Helper()
	parent := harnessParent(binary)
	if parent == entry || parent <= 1 {
		t.Fatalf("the binary's parent is pid %d, the entry is pid %d: nothing but the entry stands between SIGKILL of the entry and an orphaned binary", parent, entry)
	}
	return parent
}

// awaitHarnessBinaryEnd waits for the binary to end once its entry is killed and logs how long it took. The supervisor ends it, so it ends while its supervisor lives; a binary alive after its supervisor ended, or handed to another parent, is an orphan nothing will end. The budget is the repo's liveness bound for an observed exit, not a latency the product promises.
func awaitHarnessBinaryEnd(t *testing.T, binary, supervisor int) {
	t.Helper()
	budget := testbudget.Wait(t)
	start := time.Now()
	for harnessChildAlive(binary) {
		if !harnessChildAlive(supervisor) || harnessParent(binary) != supervisor {
			if !harnessChildAlive(binary) {
				break
			}
			t.Fatalf("the binary (pid %d) outlived its supervisor (pid %d): an orphan nothing will end", binary, supervisor)
		}
		if time.Since(start) > budget {
			t.Fatalf("the binary (pid %d) was still running %s after its entry was killed", binary, budget)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("the binary ended %s after its entry was killed", time.Since(start).Round(time.Millisecond))
}

// Pi's subagent example ends a spawned child with TERM and then KILL; in Pi the child is pi, so KILL ends it. Here the child is the entry, whose binary cannot be sent the KILL the entry receives: the binary's own supervisor ends it.
// Each step says what it saw, so a red names its path: an entry that ends on TERM ("signal: terminated") had no handler yet and never forwarded it; an entry that does not end on KILL ("signal: killed" expected) never ran; a binary that outlives its entry has no supervisor armed.
func TestHarnessEntryKillEndsItsBinary(t *testing.T) {
	cmd, root := newHarnessEntry(t, harnessEntrySpec{
		// A binary that traps TERM installs the trap before it publishes its pid: the test sends TERM as soon as the pid file exists. The trap file is how the test sees TERM arrive.
		prelude: "trap 'echo > \"$ROOT/term\"' TERM\n",
		body:    "while :; do sleep 0.1; done\n",
	})
	startHarnessEntry(t, cmd)
	entryEnded := harnessEntryEnds(cmd)
	pid := waitHarnessStubPID(t, filepath.Join(root, "pid"))
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	supervisor := harnessSupervisor(t, pid, cmd.Process.Pid)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	awaitHarnessFileWhileEntryLives(t, filepath.Join(root, "term"), entryEnded, "the binary got the TERM the entry forwards")
	if !harnessChildAlive(pid) {
		t.Fatal("the binary ended on TERM, which it traps")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if err := <-entryEnded; !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("the entry's wait status after KILL is %v, want signal: killed", err)
	}
	awaitHarnessBinaryEnd(t, pid, supervisor)
}

// An entry that dies while it or its supervisor is still starting the binary leaves nothing running, whichever way it dies: testdata/harness-stall.mjs stops the harness at the named point, the entry is killed there, and the process the point names (the entry's first child, or the supervisor) must end, with the binary if it was started. A SIGTERM at the first point finds the entry before it has installed a handler for it, so the default action ends the entry.
func TestHarnessEntryKilledWhileStarting(t *testing.T) {
	stall, err := filepath.Abs(filepath.Join("testdata", "harness-stall.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, at string
		signal   syscall.Signal
	}{
		{"SIGKILL right after the entry started its first child", "entry-after-first-spawn", syscall.SIGKILL},
		{"SIGTERM right after the entry started its first child", "entry-after-first-spawn", syscall.SIGTERM},
		{"SIGKILL while the supervisor is about to start the binary", "supervisor-before-spawn", syscall.SIGKILL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var flag, release string
			cmd, root := newHarnessEntry(t, harnessEntrySpec{
				prelude: "trap '' TERM\n",
				body:    "while :; do sleep 0.1; done\n",
				env: func(root string) []string {
					flag, release = filepath.Join(root, "flag"), filepath.Join(root, "release")
					return []string{"NODE_OPTIONS=--import=" + stall, "HARNESS_STALL_AT=" + tc.at, "HARNESS_STALL_FLAG=" + flag, "HARNESS_STALL_RELEASE=" + release}
				},
			})
			startHarnessEntry(t, cmd)
			stalled := waitHarnessChildPID(t, flag)
			t.Cleanup(func() { _ = syscall.Kill(stalled, syscall.SIGKILL) })
			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			if err := os.WriteFile(release, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			awaitHarnessEnd(t, stalled, "the process the entry started")
			if raw, err := os.ReadFile(filepath.Join(root, "pid")); err == nil {
				if pid, ok := harnessPublishedPID(raw); ok {
					t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
					if harnessChildAlive(pid) {
						t.Fatalf("the binary (pid %d) outlived the process that started it", pid)
					}
				}
			}
		})
	}
}

// A signal sent to the entry before its supervisor has started the binary is not lost: the entry's message waits in the channel until the supervisor listens, which it does as soon as it has started the binary, so the supervisor signals the binary the moment it exists. The binary then ends the entry's way: with the status its trap gives, or, when the signal comes before the binary has installed its trap, by the signal itself, which the supervisor and the entry relay. testdata/harness-stall.mjs holds the supervisor right before it starts the binary, marks the entry's send and records the supervisor's signals.
func TestHarnessEntrySignalBeforeTheBinaryStartsReachesTheBinary(t *testing.T) {
	stall, err := filepath.Abs(filepath.Join("testdata", "harness-stall.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		signal syscall.Signal
		status int
	}{{"TERM", syscall.SIGTERM, 3}, {"INT", syscall.SIGINT, 4}, {"HUP", syscall.SIGHUP, 5}} {
		t.Run(tc.name, func(t *testing.T) {
			var flag, release string
			cmd, root := newHarnessEntry(t, harnessEntrySpec{
				prelude: "trap 'exit 3' TERM\ntrap 'exit 4' INT\ntrap 'exit 5' HUP\n",
				body:    "while :; do sleep 0.1; done\n",
				env: func(root string) []string {
					flag, release = filepath.Join(root, "flag"), filepath.Join(root, "release")
					return []string{"NODE_OPTIONS=--import=" + stall, "HARNESS_STALL_AT=supervisor-before-spawn", "HARNESS_STALL_FLAG=" + flag, "HARNESS_STALL_RELEASE=" + release}
				},
			})
			startHarnessEntry(t, cmd)
			entryEnded := harnessEntryEnds(cmd)
			supervisor := waitHarnessChildPID(t, flag)
			t.Cleanup(func() { _ = syscall.Kill(supervisor, syscall.SIGKILL) })
			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatal(err)
			}
			awaitHarnessFileWhileEntryLives(t, flag+".sent", entryEnded, "the entry sent the signal to its supervisor")
			if _, err := os.Stat(filepath.Join(root, "pid")); err == nil {
				t.Fatal("the binary exists before its supervisor was released")
			}
			if err := os.WriteFile(release, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			budget := testbudget.Wait(t)
			select {
			case err := <-entryEnded:
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatalf("the entry ended with %v, want the binary's end", err)
				}
				status := exit.Sys().(syscall.WaitStatus)
				byTrap := status.Exited() && status.ExitStatus() == tc.status
				bySignal := status.Signaled() && status.Signal() == tc.signal
				if !byTrap && !bySignal {
					t.Fatalf("the entry ended with %v, want status %d (the binary's trap for %s) or the signal itself", err, tc.status, tc.name)
				}
			case <-time.After(budget):
				t.Fatalf("the %s the entry got before its binary existed never reached the binary: the entry still runs %s later", tc.name, budget)
			}
			if got, _ := os.ReadFile(flag + ".calls"); !strings.Contains(string(got), "child.kill SIG"+tc.name+" true\n") {
				t.Fatalf("the supervisor's signals were %q, want a kill SIG%s that reached the binary", got, tc.name)
			}
		})
	}
}

// A supervisor whose binary has ended but which has not ended itself yet (testdata/harness-stall.mjs holds it right before it exits) can still lose its entry. The kill it then runs is a no-op on the handle of the ended binary, which is all that names the binary, so a process that took over the binary's pid is never signalled, and the supervisor signals nothing else.
func TestHarnessEntryKillAfterTheBinaryEndedSignalsNothing(t *testing.T) {
	stall, err := filepath.Abs(filepath.Join("testdata", "harness-stall.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	var flag, release string
	cmd, root := newHarnessEntry(t, harnessEntrySpec{
		body: "exit 3\n",
		env: func(root string) []string {
			flag, release = filepath.Join(root, "flag"), filepath.Join(root, "release")
			return []string{"NODE_OPTIONS=--import=" + stall, "HARNESS_STALL_AT=supervisor-after-binary-exit", "HARNESS_STALL_FLAG=" + flag, "HARNESS_STALL_RELEASE=" + release}
		},
	})
	startHarnessEntry(t, cmd)
	supervisor := waitHarnessChildPID(t, flag)
	t.Cleanup(func() { _ = syscall.Kill(supervisor, syscall.SIGKILL) })
	if binary := waitHarnessStubPID(t, filepath.Join(root, "pid")); harnessChildAlive(binary) {
		t.Fatalf("the binary (pid %d) is still running: the supervisor is held after the binary ended", binary)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	calls := flag + ".calls"
	awaitHarnessFile(t, calls)
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	awaitHarnessEnd(t, supervisor, "the supervisor")
	if got, err := os.ReadFile(calls); err != nil || string(got) != "child.kill SIGKILL false\n" {
		t.Fatalf("the supervisor sent %q (%v) after its binary ended, want exactly one kill that found nothing to signal", got, err)
	}
}

func TestHarnessEntryKeepsBinaryExitStatus(t *testing.T) {
	cmd, root := newHarnessEntry(t, harnessEntrySpec{body: "exit 7\n"})
	startHarnessEntry(t, cmd)
	waitHarnessStubPID(t, filepath.Join(root, "pid"))
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("entry exit = %v, want status 7", err)
	}
}

// What makes a process the entry is the file Node runs, not a channel or a variable it carries: a Node parent that forks the entry gives it an IPC channel of its own, and it still runs the binary and ends with the binary's status.
func TestHarnessEntryForkedByANodeParentIsStillTheEntry(t *testing.T) {
	cmd, root := newHarnessEntry(t, harnessEntrySpec{body: "exit 6\n"})
	cli, err := filepath.Abs(cmd.Args[1])
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(cmd.Path, "--input-type=module", "-e", `import { fork } from "node:child_process"; fork(process.argv[1], [], { execArgv: [] }).on("exit", (code) => process.exit(code ?? 1));`, cli)
	parent.Env = cmd.Env
	startHarnessEntry(t, parent)
	waitHarnessStubPID(t, filepath.Join(root, "pid"))
	var exit *exec.ExitError
	if err := parent.Wait(); !errors.As(err, &exit) || exit.ExitCode() != 6 {
		t.Fatalf("the forked entry's parent ended with %v, want the binary's status 6", err)
	}
}

// A binary that ends by a signal ends the entry by the same signal, SIGINT, SIGTERM and SIGHUP included: the harness processes hold handlers for those three while the binary runs, and drop them before they re-raise the signal.
func TestHarnessEntryEndsBySignalLikeItsBinary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		signal syscall.Signal
	}{{"INT", syscall.SIGINT}, {"TERM", syscall.SIGTERM}, {"HUP", syscall.SIGHUP}, {"KILL", syscall.SIGKILL}, {"USR2", syscall.SIGUSR2}} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _ := newHarnessEntry(t, harnessEntrySpec{body: "kill -s " + tc.name + " $$\n"})
			startHarnessEntry(t, cmd)
			err := cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("entry ended with %v, want the signal %s", err, tc.signal)
			}
			if status, ok := exit.Sys().(syscall.WaitStatus); !ok || !status.Signaled() || status.Signal() != tc.signal {
				t.Fatalf("entry ended with %v, want the signal %s", exit, tc.signal)
			}
		})
	}
}

// A signal sent to the entry reaches the binary, and the binary's exit status is the entry's.
func TestHarnessEntryForwardsTermAndKeepsTheExitStatus(t *testing.T) {
	cmd, root := newHarnessEntry(t, harnessEntrySpec{prelude: "trap 'exit 3' TERM\n", body: "while :; do sleep 0.1; done\n"})
	startHarnessEntry(t, cmd)
	waitHarnessStubPID(t, filepath.Join(root, "pid"))
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("entry exit = %v, want the binary's status 3", err)
	}
}

// The binary gets the entry's stdin, stdout and stderr, the arguments after the entry's own, and the entry's process group, which is the foreground group of a terminal the entry runs in.
func TestHarnessEntryGivesTheBinaryItsStdioArgumentsAndProcessGroup(t *testing.T) {
	cmd, root := newHarnessEntry(t, harnessEntrySpec{
		args: []string{"--mode", "rpc x"},
		body: "read line\nprintf 'out %s|%s\\n' \"$line\" \"$*\"\nprintf 'err %s\\n' \"$line\" >&2\nps -o pgid= -p $$ | tr -d ' ' > \"$ROOT/pgid.tmp\" && mv \"$ROOT/pgid.tmp\" \"$ROOT/pgid\"\nread rest\nexit 0\n",
	})
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	startHarnessEntry(t, cmd)
	if _, err := stdin.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	awaitHarnessFile(t, filepath.Join(root, "pgid"))
	raw, _ := os.ReadFile(filepath.Join(root, "pgid"))
	entryGroup, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); got != strconv.Itoa(entryGroup) {
		t.Fatalf("binary process group = %q, the entry's is %d", got, entryGroup)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "out hello|--mode rpc x\n" || stderr.String() != "err hello\n" {
		t.Fatalf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}

// The harness processes between the entry and the binary never read stdin: the binary gets every byte, the ones already in the pipe before the entry exists and the ones written while it runs, and a read by a harness process would take bytes from the binary.
func TestHarnessEntryNeverReadsTheBinarysStdin(t *testing.T) {
	cmd, root := newHarnessEntry(t, harnessEntrySpec{body: "cat > \"$ROOT/stdin\"\n"})
	input := make([]byte, 1<<20)
	for i := range input {
		input[i] = byte(i*31 + i>>8)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	cmd.Stdin = r
	const early = 4096
	if _, err := w.Write(input[:early]); err != nil {
		t.Fatal(err)
	}
	startHarnessEntry(t, cmd)
	_ = r.Close()
	go func() {
		_, _ = w.Write(input[early:])
		_ = w.Close()
	}()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "stdin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, input) {
		t.Fatalf("the binary read %d bytes of the %d the entry's stdin held, or other bytes", len(got), len(input))
	}
}

// The binary runs with exactly the environment and the open files it has when a Node process starts it directly: the entry's helper adds no variable, and leaks neither its IPC channel's descriptor nor the variables Node reads it from. The binary is PiG, which starts Node extensions and the entry again.
func TestHarnessEntryLeavesTheBinaryEnvironmentAndFilesAlone(t *testing.T) {
	cmd, root := newHarnessEntry(t, harnessEntrySpec{
		prelude: "env | sort > \"$ROOT/env.$$\"\nls /dev/fd | tr '\\n' ' ' > \"$ROOT/fds.$$\"\n",
		body:    "exit 0\n",
		env:     func(string) []string { return []string{"HARNESS_TEST_MARK=1"} },
	})
	// What a Node process that starts the binary itself gives it: the shape the entry had before it started a supervisor. The same node and environment, so a launcher shim that edits the environment of every Node process shows in both.
	baseline := exec.Command(cmd.Path, "--input-type=module", "-e", `import { spawnSync } from "node:child_process"; spawnSync(process.env.PIG_HARNESS_BINARY, { stdio: "inherit" });`)
	baseline.Env = cmd.Env
	if err := baseline.Run(); err != nil {
		t.Fatal(err)
	}
	startHarnessEntry(t, cmd)
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	envFiles, _ := filepath.Glob(filepath.Join(root, "env.*"))
	fdFiles, _ := filepath.Glob(filepath.Join(root, "fds.*"))
	if len(envFiles) != 2 || len(fdFiles) != 2 {
		t.Fatalf("the binary ran %d times, want twice (by a bare Node process, then through the entry)", len(envFiles))
	}
	for _, files := range [][]string{envFiles, fdFiles} {
		a, _ := os.ReadFile(files[0])
		b, _ := os.ReadFile(files[1])
		if !bytes.Equal(a, b) {
			t.Fatalf("%s and %s differ:\n%s\n---\n%s", files[0], files[1], a, b)
		}
	}
}

// The binary may start the entry again (a subagent that starts a subagent). The outer binary here becomes the inner entry, which runs the inner binary: every level relays the status of the level below, and killing the outermost entry ends every level.
func TestHarnessEntryNests(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	cli, err := filepath.Abs(filepath.Join("runtime-node", "harness", "cli.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	nested := func(t *testing.T, innerBody string) (*exec.Cmd, string) {
		return newHarnessEntry(t, harnessEntrySpec{
			body: "PIG_HARNESS_BINARY=\"$ROOT/inner\" exec \"$HARNESS_TEST_NODE\" \"$HARNESS_TEST_CLI\"\n",
			env:  func(string) []string { return []string{"HARNESS_TEST_NODE=" + node, "HARNESS_TEST_CLI=" + cli} },
			setup: func(root string) {
				script := "#!/bin/sh\necho $$ > '" + root + "/inner.pid'\n" + innerBody
				if err := execfile.Write(filepath.Join(root, "inner"), os.O_CREATE|os.O_TRUNC, 0o700, execfile.Bytes([]byte(script))); err != nil {
					t.Fatal(err)
				}
			},
		})
	}
	t.Run("exit status", func(t *testing.T) {
		cmd, _ := nested(t, "exit 5\n")
		startHarnessEntry(t, cmd)
		var exit *exec.ExitError
		if err := cmd.Wait(); !errors.As(err, &exit) || exit.ExitCode() != 5 {
			t.Fatalf("outer entry exit = %v, want the inner binary's status 5", err)
		}
	})
	t.Run("kill", func(t *testing.T) {
		cmd, root := nested(t, "while :; do sleep 0.1; done\n")
		startHarnessEntry(t, cmd)
		innerEntry := waitHarnessStubPID(t, filepath.Join(root, "pid"))
		inner := waitHarnessStubPID(t, filepath.Join(root, "inner.pid"))
		t.Cleanup(func() {
			_ = syscall.Kill(inner, syscall.SIGKILL)
			_ = syscall.Kill(innerEntry, syscall.SIGKILL)
		})
		outerSupervisor := harnessSupervisor(t, innerEntry, cmd.Process.Pid)
		innerSupervisor := harnessSupervisor(t, inner, innerEntry)
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = cmd.Wait()
		awaitHarnessBinaryEnd(t, innerEntry, outerSupervisor)
		awaitHarnessBinaryEnd(t, inner, innerSupervisor)
	})
}

// harnessEntryEnd is how an entry ended and what it wrote to stderr, as a stub binary's end shows it to the process that ran the entry.
type harnessEntryEnd struct{ status, stderr string }

// harnessCoreFiles names the core files in dir: what a process that dumps core leaves in its working directory (core, or core.<pid>).
func harnessCoreFiles(dir string) map[string]bool {
	found := map[string]bool{}
	for _, pattern := range []string{"core", "core.*"} {
		names, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, name := range names {
			found[name] = true
		}
	}
	return found
}

// harnessNoCoreWritten fails t for every core file in dir that was not there in before, and removes it. A signal that ends a process by its default action dumps core (QUIT, ILL, TRAP, ABRT, BUS, FPE, SEGV, SYS, XCPU, XFSZ),
// and the stubs and the Node processes of these tests end by every such signal: without the limit core_limit_unix_test.go sets, each run leaves one core file per process in its working directory, which these tests start
// in the package directory (the stub of harnessStubNotEndedBy and the probe of the Node pin) and in a temporary directory (the entry and the oracle).
func harnessNoCoreWritten(t *testing.T, dir string, before map[string]bool) {
	t.Helper()
	for name := range harnessCoreFiles(dir) {
		if !before[name] {
			_ = os.Remove(name)
			t.Errorf("a process this test started wrote the core file %s: the tests of this package run with RLIMIT_CORE 0 (core_limit_unix_test.go), so a core file is a process that was started with the limit raised", name)
		}
	}
}

// harnessRunToEnd runs cmd to its end with its stderr captured and returns how it ended. The budget only fails a hang: a process that never ends is the failure, not a slow one. No process may write a core file into its working directory.
func harnessRunToEnd(t *testing.T, cmd *exec.Cmd) harnessEntryEnd {
	t.Helper()
	dir := cmd.Dir
	if dir == "" {
		dir = "."
	}
	cores := harnessCoreFiles(dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() { ended <- cmd.Wait() }()
	budget := testbudget.Wait(t)
	select {
	case <-ended:
	case <-time.After(budget):
		_ = cmd.Process.Kill()
		<-ended
		t.Fatalf("%s never ended: it was still running %s (stderr %q)", strings.Join(cmd.Args[:2], " "), budget, stderr.String())
	}
	harnessNoCoreWritten(t, dir, cores)
	status := "exit " + strconv.Itoa(cmd.ProcessState.ExitCode())
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		status = "signal " + strconv.Itoa(int(ws.Signal()))
	}
	return harnessEntryEnd{status, stderr.String()}
}

// harnessNodeEnv is the environment for a Node that may meet SIGUSR1: it opens an inspector, and port 0 keeps concurrent runs, of these tests and of others, off port 9229, where one run would find it taken and print a line of its own.
func harnessNodeEnv(options ...string) []string {
	return append(os.Environ(), "NODE_OPTIONS="+strings.TrimSpace(strings.Join(append([]string{os.Getenv("NODE_OPTIONS"), "--inspect-port=0"}, options...), " ")))
}

// runHarnessEntryToEnd runs the entry over /bin/sh -c script as its binary (the entry passes its arguments through) and returns how it ended: no stub file is written, so no exec can meet a file another
// goroutine still holds open for writing (ETXTBSY). With mark set, every Node process of the entry reports on stderr that it raised that signal on itself (testdata/harness-signal-mark.mjs).
func runHarnessEntryToEnd(t *testing.T, node, entry, script string, mark syscall.Signal) harnessEntryEnd {
	t.Helper()
	cmd := exec.Command(node, entry, "-c", script)
	cmd.Dir = t.TempDir()
	cmd.Env = append(harnessNodeEnv(), "PIG_HARNESS_BINARY=/bin/sh")
	if mark != 0 {
		preload, err := filepath.Abs(filepath.Join("testdata", "harness-signal-mark.mjs"))
		if err != nil {
			t.Fatal(err)
		}
		cmd.Env = append(harnessNodeEnv("--import", preload), "PIG_HARNESS_BINARY=/bin/sh", "HARNESS_MARK_SIGNALS="+strconv.Itoa(int(mark)))
	}
	return harnessRunToEnd(t, cmd)
}

// harnessRaise is the stub binary's script that ends it by sig, when sig ends a shell.
func harnessRaise(sig syscall.Signal) string {
	return "kill -" + strconv.Itoa(int(sig)) + " $$; sleep 1"
}

// harnessStubNotEndedBy tells why /bin/sh does not end by sig when it raises it on itself: it stops, or it survives. It returns "" when sig ends it. A stopped stub is killed.
func harnessStubNotEndedBy(t *testing.T, sig syscall.Signal) string {
	t.Helper()
	cores := harnessCoreFiles(".")
	own := exec.Command("/bin/sh", "-c", harnessRaise(sig))
	if err := own.Start(); err != nil {
		t.Fatal(err)
	}
	var ws syscall.WaitStatus
	if _, err := syscall.Wait4(own.Process.Pid, &ws, syscall.WUNTRACED, nil); err != nil {
		t.Fatal(err)
	}
	harnessNoCoreWritten(t, ".", cores)
	if ws.Stopped() {
		_ = own.Process.Kill()
		_, _ = syscall.Wait4(own.Process.Pid, &ws, 0, nil)
		return "the stub stops on " + sig.String() + " and does not end"
	}
	if !ws.Signaled() || ws.Signal() != sig {
		return "the stub does not end by " + sig.String() + " (it ended " + strconv.Itoa(ws.ExitStatus()) + ")"
	}
	return ""
}

// A binary ends by an exit code or by a signal, and the entry ends the same way: by the same code, by the same signal, or, where Node does not die from the signal (it ignores SIGPIPE and SIGXFSZ and takes SIGUSR1
// for its inspector), with code 0 and without raising the signal. testdata/harness-entry-oracle.mjs is the entry as it ended its binary before it had a supervisor; the supervised entry must end as it does for every
// exit code worth distinguishing and for every signal 1 to 31 by which a stub ends itself. A signal that does not end the stub (STOP, TSTP, CONT, CHLD, WINCH and the rest) is skipped by the stub's own wait status, not by a list.
// The oracle raises SIGUSR1 on itself and so opens an inspector, which writes a notice once or, when the process ends first, not at all: in that one row the oracle contributes its status and not its stderr, and the
// supervised entry must end with nothing on stderr, which is what the stub wrote.
func TestHarnessEntryEndsAsItsBinaryEndedForEveryExitAndSignal(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	oracle, err := filepath.Abs(filepath.Join("testdata", "harness-entry-oracle.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := filepath.Abs(filepath.Join("runtime-node", "harness", "cli.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	type end struct {
		name, body string
		signal     syscall.Signal
	}
	var ends []end
	for _, code := range []int{0, 1, 2, 127, 255} {
		ends = append(ends, end{name: "exit " + strconv.Itoa(code), body: "exit " + strconv.Itoa(code)})
	}
	for n := 1; n <= 31; n++ {
		ends = append(ends, end{name: "signal " + strconv.Itoa(n) + " " + syscall.Signal(n).String(), body: harnessRaise(syscall.Signal(n)), signal: syscall.Signal(n)})
	}
	for _, e := range ends {
		t.Run(e.name, func(t *testing.T) {
			t.Parallel()
			if e.signal != 0 {
				if why := harnessStubNotEndedBy(t, e.signal); why != "" {
					t.Skip(why)
				}
			}
			want := runHarnessEntryToEnd(t, node, oracle, e.body, 0)
			got := runHarnessEntryToEnd(t, node, entry, e.body, 0)
			if e.signal == syscall.SIGUSR1 {
				want.stderr = ""
			}
			if got != want {
				t.Errorf("the entry ended %+v, the entry before the supervisor ended %+v", got, want)
			}
			t.Logf("%s: both ended %q with stderr %q", e.name, got.status, got.stderr)
		})
	}
}

// Node does not end itself by every signal raised on it: it ignores SIGPIPE and SIGXFSZ and takes SIGUSR1 for its inspector. A hop that raised such a signal on itself to end as its binary ended would not end by it,
// and for SIGUSR1 would open an inspector, which writes a notice and can crash Node while it opens. So relay.mjs lists the signals Node survives (survivedSignals) and a hop ends with code 0 for them without raising them.
// This test finds what Node does for each signal that ends a stub, on the platform it runs on, and requires the list to be exactly that: a survivor missing from the list, or a signal that ends Node in it, fails. It then runs the
// entry on a stub that ends by each survivor, with a preload that reports a Node process of the entry raising that signal on itself: none may, and the entry ends with code 0.
func TestHarnessEntryNeverRaisesASignalNodeSurvives(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	relay, err := filepath.Abs(filepath.Join("runtime-node", "harness", "relay.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := filepath.Abs(filepath.Join("runtime-node", "harness", "cli.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	// The list's names as this platform's signal numbers; a name the platform has no signal for comes back null, which decodes as 0.
	out, err := exec.Command(node, "--input-type=module", "-e", `import { constants } from "node:os";
const { survivedSignals } = await import(process.argv[1]);
console.log(JSON.stringify(survivedSignals.map((name) => constants.signals[name] ?? null)));`, "file://"+relay).Output()
	if err != nil {
		t.Fatalf("reading survivedSignals from relay.mjs: %v", err)
	}
	var numbers []int
	if err := json.Unmarshal(out, &numbers); err != nil {
		t.Fatalf("survivedSignals as signal numbers %q: %v", out, err)
	}
	listed := map[int]bool{}
	for _, n := range numbers {
		if n < 1 || n > 31 {
			t.Errorf("survivedSignals names a signal this platform does not number from 1 to 31 (%d in %s)", n, out)
		}
		listed[n] = true
	}

	var mu sync.Mutex
	survives := map[int]bool{} // for each signal that ends a stub: whether bare Node survives it raised on itself
	t.Run("probe", func(t *testing.T) {
		for n := 1; n <= 31; n++ {
			sig := syscall.Signal(n)
			t.Run(strconv.Itoa(n)+" "+sig.String(), func(t *testing.T) {
				t.Parallel()
				if why := harnessStubNotEndedBy(t, sig); why != "" {
					t.Skip(why)
				}
				cmd := exec.Command(node, "-e", "process.kill(process.pid, Number(process.argv[1]))", strconv.Itoa(n))
				cmd.Env = harnessNodeEnv()
				end := harnessRunToEnd(t, cmd)
				mu.Lock()
				survives[n] = end.status == "exit 0"
				mu.Unlock()
				t.Logf("Node raised %v on itself and ended %q", sig, end.status)
			})
		}
	})
	var survivors []int
	for n := 1; n <= 31; n++ {
		sig := syscall.Signal(n)
		survived, probed := survives[n]
		switch {
		case !probed && listed[n]:
			t.Errorf("survivedSignals lists %v, which does not end a stub, so no binary ends by it", sig)
		case survived && !listed[n]:
			t.Errorf("Node survives %v raised on itself and survivedSignals does not list it: a hop would raise it", sig)
		case probed && !survived && listed[n]:
			t.Errorf("survivedSignals lists %v, which ends Node: a hop would not end by it", sig)
		}
		if survived {
			survivors = append(survivors, n)
		}
	}
	t.Run("entry", func(t *testing.T) {
		for _, n := range survivors {
			sig := syscall.Signal(n)
			t.Run(sig.String(), func(t *testing.T) {
				t.Parallel()
				got := runHarnessEntryToEnd(t, node, entry, harnessRaise(sig), sig)
				if want := (harnessEntryEnd{"exit 0", ""}); got != want {
					t.Errorf("a stub that ends by %v: the entry ended %+v, want %+v (a stderr line is a hop raising the signal on itself)", sig, got, want)
				}
			})
		}
	})
}
