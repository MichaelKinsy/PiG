//go:build !windows

// The tests kill the harness with POSIX signals (SIGTERM, SIGKILL), which Windows has no counterpart for.
package subprocess

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

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
// $ROOT, in prelude and body, is the test's temp directory.
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
	script := "#!/bin/sh\nROOT='" + root + "'\n" + spec.prelude + "echo $$ > \"$ROOT/pid.tmp\" && mv \"$ROOT/pid.tmp\" \"$ROOT/pid\"\n" + spec.body
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
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

func startHarnessEntry(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
}

// waitHarnessChildPID waits for the pid a stub published in pidFile. The bound only fails a hang: the file's appearance is the event.
func waitHarnessChildPID(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never held a pid", pidFile)
	return 0
}

func awaitHarnessFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
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

// harnessEntryEnds starts waiting for cmd in the background: the channel receives the entry's wait status once it ended.
func harnessEntryEnds(cmd *exec.Cmd) <-chan error {
	ended := make(chan error, 1)
	go func() { ended <- cmd.Wait() }()
	return ended
}

// awaitHarnessFileWhileEntryLives waits for path and fails, naming the entry's wait status, if the entry ends first.
func awaitHarnessFileWhileEntryLives(t *testing.T, path string, ended <-chan error, why string) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for time.Now().Before(deadline) {
		select {
		case err := <-ended:
			t.Fatalf("the entry ended (%v) before %s", err, why)
		default:
		}
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared, so %s did not happen", path, why)
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
	pid := waitHarnessChildPID(t, filepath.Join(root, "pid"))
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
				if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
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
	if binary := waitHarnessChildPID(t, filepath.Join(root, "pid")); harnessChildAlive(binary) {
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
	waitHarnessChildPID(t, filepath.Join(root, "pid"))
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
	waitHarnessChildPID(t, filepath.Join(root, "pid"))
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
	waitHarnessChildPID(t, filepath.Join(root, "pid"))
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
				script := "#!/bin/sh\necho $$ > '" + root + "/inner.pid.tmp' && mv '" + root + "/inner.pid.tmp' '" + root + "/inner.pid'\n" + innerBody
				if err := os.WriteFile(filepath.Join(root, "inner"), []byte(script), 0o700); err != nil {
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
		innerEntry := waitHarnessChildPID(t, filepath.Join(root, "pid"))
		inner := waitHarnessChildPID(t, filepath.Join(root, "inner.pid"))
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
