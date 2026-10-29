// Shell command execution for extensions.
//
// Mirrors upstream core/exec.ts. Provides the implementation backing
// extension.API.Exec: a simple process spawn with timeout and abort
// support.
//
// upstream: coding-agent/src/core/exec.ts
package extension

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/childwait"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
	"github.com/MichaelKinsy/PiG/internal/utf8stream"
)

// execForceKillDelay is exec.ts FORCE_KILL_DELAY_MS: SIGKILL follows SIGTERM this long after, if the child has not exited.
const execForceKillDelay = 5 * time.Second

// execMaxStringLength is buffer.constants.MAX_STRING_LENGTH of the runtime Pi's execCommand validates maxBuffer against: V8's 2^29-24 on 64-bit Node 24.
// upstream: node:buffer constants.MAX_STRING_LENGTH (exec.ts DEFAULT_EXEC_MAX_BUFFER, maxBuffer validation)
var execMaxStringLength int64 = 536870888

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
	return int64(value), nil
}

// execDrainHooksKey carries the childwait hooks that a test installs on the
// context of one ExecCommand call.
type execDrainHooksKey struct{}

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
// upstream resolves it as 0 (`code ?? 0`). On Windows a child that the kill
// ended has none either: libuv reports the signal it was sent instead of the
// exit status 1, so the code is 0 there too.
//
// opts.MaxBuffer caps the bytes of stdout and stderr together (default 256 MiB,
// capped by the runtime string limit; an invalid value fails the call, as
// upstream's RangeError rejects it, before anything runs). Output past the cap
// is dropped at the byte: both pipes are closed, the child is terminated, and
// the result carries Truncated, Killed and a nonzero code. Each stream is
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
	stdoutRead, stdoutWrite, pipeErr := os.Pipe()
	if pipeErr != nil {
		return ExecResult{Code: 1}, nil
	}
	stderrRead, stderrWrite, pipeErr := os.Pipe()
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
			_ = stdoutRead.Close()
			_ = stderrRead.Close()
			killProcess()
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
	// A child that a signal ended has no exit code (ExitCode is -1), nor has
	// a Windows child that terminate ended, and upstream resolves `code ?? 0`.
	switch state := cmd.ProcessState; {
	case state == nil:
		result.Code = 1
	case terminated:
		result.Code = 0
	default:
		result.Code = max(state.ExitCode(), 0)
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
	return time.Duration(int64(ms)) * time.Millisecond
}
