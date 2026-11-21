// Shell command execution for extensions.
//
// Mirrors upstream core/exec.ts. Provides the implementation backing
// extension.API.Exec: a simple process spawn with timeout and abort
// support.
//
// upstream: coding-agent/src/core/exec.ts
package extension

// Ports packages/coding-agent/src/core/exec.ts.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/childwait"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
	"github.com/MichaelKinsy/PiG/internal/utf8stream"
)

// execForceKillDelay is exec.ts FORCE_KILL_DELAY_MS: SIGKILL follows SIGTERM this long after, if the child has not exited.
const execForceKillDelay = 5 * time.Second

// nodeMaxStringLength is buffer.constants.MAX_STRING_LENGTH of Node 24 on 64-bit builds (V8 String::kMaxLength), which exec.ts reads from node:buffer; see GUARD-13 in automation/ci/divguard/baseline.toml.
const nodeMaxStringLength = 536870888

// execMaxStringLength is the limit a test replaces (export_test.go), as exec-runtime-limit.test.ts mocks it.
var execMaxStringLength int64 = nodeMaxStringLength

// execDefaultMaxBuffer is DEFAULT_EXEC_MAX_BUFFER: 256 MiB, capped by the runtime string limit.
func execDefaultMaxBuffer() int64 { return min(256*1024*1024, execMaxStringLength) }

// execMaxBuffer is the cap of one call: options.maxBuffer ?? DEFAULT_EXEC_MAX_BUFFER, which exec.ts rejects with a RangeError unless it is an integer between 0 and the runtime string limit.
// ExecOptions.MaxBuffer is a JavaScript number, so NaN, a fraction and an infinity are rejected like -1.
func execMaxBuffer(opts *ExecOptions) (int64, error) {
	if opts.MaxBuffer == nil {
		return execDefaultMaxBuffer(), nil
	}
	value := *opts.MaxBuffer
	if value != math.Trunc(value) || value < 0 || value > float64(execMaxStringLength) {
		return 0, fmt.Errorf("maxBuffer must be an integer between 0 and %d bytes", execMaxStringLength)
	}
	//portlint:allow numbers value is finite, integral and within [0, 536870888] after the check above (NaN fails value != Trunc(value), infinities fail the upper bound), so int64(value) is exact like JS
	return int64(value), nil
}

// ExecOptions configures a shell command execution.
//
// upstream: core/exec.ts ExecOptions
type ExecOptions struct {
	// Timeout in milliseconds. Pi's timeout is a JavaScript number: a positive value starts a timer and anything else starts none.
	Timeout float64 `json:"timeout,omitempty"`
	// CWD overrides the working directory. Empty uses the extension's CWD.
	CWD string `json:"cwd,omitempty"`
	// MaxBuffer is the byte limit of stdout and stderr together; exceeding it terminates the command with Truncated set. nil is undefined: the default, 256 MiB capped by the runtime string limit. Pi's maxBuffer is a JavaScript number that must be an integer from 0 to the runtime string limit, so a fraction, a negative value, NaN or an infinity fails the call (a RangeError upstream); the pointer keeps 0 distinct from undefined.
	MaxBuffer *float64 `json:"maxBuffer,omitempty"`
	// Signal is upstream's options.signal: when it is done the command ends as when ExecCommand's own context ends, with SIGTERM to the command alone. It does not cross the wire; a subprocess extension's signal reaches the host as the call's own cancellation.
	Signal AbortSignal `json:"-"`
}

// ExecResult is the outcome of a shell command execution.
//
// upstream: core/exec.ts ExecResult
type ExecResult struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	// Code is the exit code, or 128 + the signal number when a signal terminated the command (143 for SIGTERM), as a shell reports it.
	Code int `json:"code"`
	// Killed reports that pi terminated the command: timeout, abort, or output over MaxBuffer.
	Killed bool `json:"killed"`
	// Truncated reports that output exceeded MaxBuffer: Stdout and Stderr are incomplete and Code is nonzero.
	Truncated bool `json:"truncated"`
}

// execDrainHooksKey carries the childwait hooks that a test installs on the
// context of one ExecCommand call.
type execDrainHooksKey struct{}

// execBeforeSignalKey carries the function that a test installs on the context of one ExecCommand call (export_test.go). killProcess calls it once,
// just before the SIGTERM (Process.Kill on Windows), with the read ends of the command's stdout and stderr, so that a test can see whether the call
// had closed them by then: it had on a cap, and it had not on a timeout or an abort (exec.ts:111-113 against exec.ts:88-98).
type execBeforeSignalKey struct{}

// ExecCommand runs a shell command synchronously and returns the result.
// This is the default implementation backing API.Exec when the host
// doesn't provide an override.
//
// The command is spawned directly (no shell wrapping). Use args to pass
// arguments. If opts.CWD is empty, cwd is used as the working directory.
// Like upstream, a program that cannot start does not fail the call: the
// result has code 1 and empty output. Arguments that Node's spawn rejects, an
// empty command or a NUL in the command, an argument, or the working
// directory, fail the call with Node's ERR_INVALID_ARG_VALUE message, which
// upstream's spawn throws. On Windows the program is looked up as Node's spawn
// looks it up (nodespawn.LookPath), so a name that matches only through
// PATHEXT, such as npm for npm.cmd, cannot start. A batch file name fails the
// call with Node's "spawn EINVAL" error, which upstream's spawn throws.
//
// Like upstream, the call waits for the child, then for its output pipes to
// end or to fall idle for 100 ms (utils/child-process.ts waitForChildProcess),
// so a descendant that keeps a pipe open does not block it, and output the
// descendant writes while the pipes stay active is kept.
//
// Timeout, context cancellation, opts.Signal and output beyond opts.MaxBuffer
// signal the child alone with SIGTERM (Process.Kill on Windows, where Node's
// kill terminates the process), and execForceKillDelay later with SIGKILL if
// the child has not exited by then. No descendant is signalled. The result's
// code is the child's exit code; a child that a signal ended has none, and
// upstream reports 128 + the signal number, as a shell does (143 for SIGTERM,
// 137 for SIGKILL), so a timeout, an abort or a crash does not read as success.
// With neither a code nor a signal it is 1. On Windows a child that the kill
// ended has no code either: libuv reports the signal it was sent instead of the
// exit status 1, so the code is 143 there too.
//
// opts.MaxBuffer caps the bytes of stdout and stderr together (default 256 MiB,
// capped by the runtime string limit; an invalid value fails the call, as
// upstream's RangeError rejects it, before anything runs). Output past the cap
// is dropped at the byte: both pipes are closed and then the child is
// signalled, in upstream's order (exec.ts:111-113), and the result carries
// Truncated, Killed and a nonzero code: the command's own when it had exited
// by then (1 for a 0), 141 when its next write met the closed pipe before the
// SIGTERM did (SIGPIPE), 143 when the SIGTERM came first. Each stream is
// decoded across chunk boundaries (utf8stream); a character the cap cuts is
// dropped with the bytes a decoder still holds, and a stream that ends inside
// a character yields one U+FFFD.
//
// upstream: core/exec.ts execCommand
func ExecCommand(ctx context.Context, cwd, command string, args []string, opts *ExecOptions) (ExecResult, error) {
	if opts == nil {
		opts = &ExecOptions{}
	}
	// options.signal ends the command like the call's own cancellation (exec.ts:46-52 has no other kind).
	if opts.Signal != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		defer context.AfterFunc(opts.Signal, cancel)()
	}

	maxBuffer, rangeErr := execMaxBuffer(opts)
	if rangeErr != nil {
		// An async function's throw rejects the call before the spawn.
		CallInitiated(ctx)
		return ExecResult{}, rangeErr
	}

	dir := cwd
	if opts.CWD != "" {
		dir = opts.CWD
	}

	// The child writes straight into OS pipes, so Wait returns when the child
	// exits even if a descendant still holds a write end.
	// pig divergence (D631): Node gives the command a socket pair for stdout and one for stderr (libuv). These are pipes: a command can open one again
	// through /dev/stdout, which a socket refuses (ENXIO), and its write after the close raises SIGPIPE, where a socket's first one fails with ECONNRESET.
	stdoutRead, stdoutWrite, pipeErr := childwait.NewPipe()
	if pipeErr != nil {
		return ExecResult{Code: 1}, nil
	}
	// pig divergence (D631): a pipe where Node gives a socket pair, as for stdout above.
	stderrRead, stderrWrite, pipeErr := childwait.NewPipe()
	if pipeErr != nil {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		return ExecResult{Code: 1}, nil
	}
	//nolint:gosec // G204 (class c): program and args are what an installed extension passes to its exec call, as Pi's pi.exec(command, args) passes them to spawn; extensions are code the user chose to install.
	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	cmd.Stdout = stdoutWrite
	cmd.Stderr = stderrWrite
	// Upstream spawns with shell: false, so on Windows libuv finds the
	// program and the child receives Node's libuv command line.
	prepareChildGroup(cmd)
	nodespawn.SetProgram(cmd)
	nodespawn.SetCommandLine(cmd)

	// Start closes the child's ends of the pipes.
	err := nodespawn.Start(cmd)
	if err == nil {
		attachChildGroup(cmd.Process)
		defer releaseChildGroup(cmd.Process)
	}
	// exec() has now completed its synchronous prefix: the spawn was attempted
	// and, on success, the child is running. A subprocess host may advance the
	// extension's ordered call lane while this command completes independently.
	CallInitiated(ctx)
	// Upstream's spawn throws inside the Promise executor, which rejects the
	// call.
	var spawnErr *nodespawn.Error
	if errors.As(err, &spawnErr) && spawnErr.Thrown {
		_ = stdoutRead.Close()
		_ = stderrRead.Close()
		return ExecResult{}, err
	}
	if err != nil {
		_ = stdoutRead.Close()
		_ = stderrRead.Close()
		// Like upstream, a program that cannot start (missing program or
		// working directory) resolves with code 1 instead of failing, and an
		// abort that came first has already set killed. Nothing ran, so stderr carries the spawn error's message
		// (exec.ts `stderr || error.message`, Pi #11085).
		return ExecResult{Stderr: nodespawn.SpawnMessage(command, err), Code: 1, Killed: ctx.Err() != nil}, nil
	}

	// exec.ts:75 starts the timer once spawn has returned, with the child
	// running, so finding and starting the program does not use up the
	// timeout: `options?.timeout && options.timeout > 0` starts a Node timer.
	// Node truncates a fractional delay and runs a delay outside [1, 2^31-1]
	// after one millisecond.
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, NodeTimerDelay(opts.Timeout))
		defer cancel()
	}

	// Only a test installs drain hooks (export_test.go); every other call
	// measures the grace on the wall clock.
	hooks, _ := ctx.Value(execDrainHooksKey{}).(*childwait.Hooks)
	beforeSignal, _ := ctx.Value(execBeforeSignalKey{}).(func(stdout, stderr *os.File))

	// Like upstream, killed reports that the timeout, cancellation or cap fired,
	// even after the child exited: upstream's abort listener and timer stay
	// attached until the call settles, after the pipes' grace.
	var (
		killMu     sync.Mutex
		killed     bool
		terminated bool
		forceKill  *time.Timer
	)
	killProcess := func() {
		killMu.Lock()
		defer killMu.Unlock()
		if killed {
			return
		}
		killed = true
		if beforeSignal != nil {
			beforeSignal(stdoutRead, stderrRead)
		}
		terminated = terminate(cmd.Process)
		// exec.ts checks that the child has not exited (exitCode and signalCode): a signal to a reaped child fails and changes nothing.
		forceKill = time.AfterFunc(execForceKillDelay, func() { forceTerminate(cmd.Process) })
	}

	// The cap counts the bytes of both streams (#11360). onData calls never overlap. On overflow both pipes are closed so that
	// a descendant that keeps writing cannot hold the call after the child exits; only the bytes within the cap are kept.
	var (
		stdout, stderr             strings.Builder
		stdoutDecode, stderrDecode utf8stream.Decoder
		outputBytes                int64
		truncated                  bool
	)
	pipes := childwait.Start([]*os.File{stdoutRead, stderrRead}, func(index int, chunk []byte) {
		if truncated {
			return
		}
		if remaining := maxBuffer - outputBytes; int64(len(chunk)) > remaining {
			truncated = true
			// exec.ts:111-113 destroys both pipes and then calls killProcess, and Node's destroy() closes the descriptor before it returns, so the
			// child's next write meets a closed pipe before SIGTERM is sent. The order decides the code of a command that is still writing: the
			// writer can die of SIGPIPE, and a shell that waited for it can run on into the same closed pipe, before SIGTERM lands (141), or
			// SIGTERM lands first (143); a command that had already exited keeps its own code (1 once truncated). Signalling first ends every
			// such command by SIGTERM and never reports 141, which most of Pi's runs of an endless writer report. Close returns once the reader
			// of the other pipe has left its wait, so both descriptors are closed when the signal goes.
			// On Windows, Close on a pipe blocks behind the sibling overlapped reader goroutine; closing before killProcess lets MSYS2 sh observe a broken pipe and exit with 1 or raw status 3328 (13 << 8) before TerminateProcess runs. Kill first on Windows, then close both pipes.
			stopOnTruncationOrder(runtime.GOOS == "windows", func() { _ = stdoutRead.Close() }, func() { _ = stderrRead.Close() }, killProcess)
			chunk = chunk[:max(remaining, 0)]
		}
		outputBytes += int64(len(chunk))
		if index == 0 {
			stdout.WriteString(stdoutDecode.Decode(chunk, true))
		} else {
			stderr.WriteString(stderrDecode.Decode(chunk, true))
		}
	}, hooks)

	stopWatch := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			killProcess()
		case <-stopWatch:
			// exec.ts keeps its timer and abort listener until the call settles, so a timeout or abort that came due while the call
			// waited counts, however the two ready channels would have been picked: a host that kept this process off the CPU past the
			// timer and past the child's end settles with the deadline due and its cancellation not yet delivered, and Pi's call, on a
			// prompt host, killed the child.
			due, hasDeadline := ctx.Deadline()
			//portlint:allow clock the call's own timeout is wall time against the deadline context.WithTimeout set; it is the settle's check that the timer has fired, not a scheduler's throttle
			if ctx.Err() != nil || hasDeadline && !time.Now().Before(due) {
				killProcess()
			}
		}
	}()

	// Wait returns the exit error of a failed status, which the state carries.
	_ = cmd.Wait()
	// A descendant that holds a pipe may still be running: where the child has no job, the pipes tell Wait.
	pipes.Wait(func() bool { return childGroupMayHoldOutput(cmd.Process) })
	close(stopWatch)
	<-watchDone
	killMu.Lock()
	if forceKill != nil {
		forceKill.Stop()
	}
	killMu.Unlock()

	// On truncation a decoder's incomplete suffix is discarded: flushing it would add a replacement character that was not in the accepted output.
	if !truncated {
		stdout.WriteString(stdoutDecode.Decode(nil, false))
		stderr.WriteString(stderrDecode.Decode(nil, false))
	}

	result := ExecResult{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Killed:    killed,
		Truncated: truncated,
	}
	// A child that a signal ended has no exit code (ExitCode is -1), nor has a Windows child that terminate ended: exitCode gives them
	// 128 + the signal number, as upstream does (#12088).
	if state := cmd.ProcessState; state == nil {
		result.Code = 1
	} else {
		result.Code = exitCode(state, terminated)
	}
	// A truncated result never reads as success.
	if truncated && result.Code == 0 {
		result.Code = 1
	}
	return result, nil
}

// NodeTimerDelay is the delay Node gives setTimeout(fn, ms): it truncates a fractional value and runs a delay outside [1, 2^31-1], or NaN, after one millisecond.
func NodeTimerDelay(ms float64) time.Duration {
	if !(ms >= 1 && ms <= 2147483647) {
		return time.Millisecond
	}
	//portlint:allow numbers ms is within [1, 2147483647] here (NaN and out-of-range returned above), so int64(ms) is the truncation Node applies and cannot overflow
	return time.Duration(int64(ms)) * time.Millisecond
}

// stopOnTruncationOrder runs the pipe closes and process kill in the platform order on maxBuffer overflow.
// On Unix it closes both pipes before signalling (matching exec.ts:111-113 so a writer meets SIGPIPE before SIGTERM);
// on Windows it kills before closing so a blocking overlapped Close cannot let MSYS2 sh exit on broken pipe first.
func stopOnTruncationOrder(killBeforeClose bool, closeStdout, closeStderr, kill func()) {
	if killBeforeClose {
		kill()
		closeStdout()
		closeStderr()
		return
	}
	closeStdout()
	closeStderr()
	kill()
}
