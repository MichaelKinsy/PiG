// Shell command execution for extensions.
//
// Mirrors upstream core/exec.ts. Provides the implementation backing
// extension.API.Exec: a simple process spawn with timeout and abort
// support.
//
// upstream: coding-agent/src/core/exec.ts (107 LOC)
package extension

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/internal/childwait"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

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
// Timeout and context cancellation signal the child alone with SIGTERM
// (Process.Kill on Windows, where Node's kill terminates the process). Like
// upstream, nothing follows it: Node sets proc.killed once SIGTERM is
// delivered, so upstream's SIGKILL after 5 seconds never runs, and no
// descendant is signalled. The result's code is the child's exit code; a child
// that a signal ended has none, and upstream resolves it as 0 (`code ?? 0`).
// On Windows a child that the kill ended has none either: libuv reports the
// signal it was sent instead of the exit status 1, so the code is 0 there too.
//
// upstream: core/exec.ts execCommand
func ExecCommand(ctx context.Context, cwd, command string, args []string, opts *ExecOptions) (ExecResult, error) {
	if opts == nil {
		opts = &ExecOptions{}
	}

	dir := cwd
	if opts.CWD != "" {
		dir = opts.CWD
	}

	// exec.ts:75: `options?.timeout && options.timeout > 0` starts a Node timer. Node truncates a fractional delay and runs a delay outside [1, 2^31-1] after one millisecond.
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, NodeTimerDelay(opts.Timeout))
		defer cancel()
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
	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	cmd.Stdout = stdoutWrite
	cmd.Stderr = stderrWrite
	// Upstream spawns with shell: false, so on Windows libuv finds the
	// program and the child receives Node's libuv command line.
	nodespawn.SetProgram(cmd)
	nodespawn.SetCommandLine(cmd)

	// Start closes the child's ends of the pipes.
	err := nodespawn.Start(cmd)
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
		// abort that came first has already set killed.
		return ExecResult{Code: 1, Killed: ctx.Err() != nil}, nil
	}

	var stdout, stderr strings.Builder
	pipes := childwait.Start([]*os.File{stdoutRead, stderrRead}, func(index int, chunk []byte) {
		if index == 0 {
			stdout.Write(chunk)
		} else {
			stderr.Write(chunk)
		}
	}, childwait.Hooks{})

	// Like upstream, killed reports that the timeout or cancellation fired,
	// even after the child exited: upstream's abort listener and timer stay
	// attached until the call settles, after the pipes' grace.
	var killed, terminated atomic.Bool
	stopWatch := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			killed.Store(true)
			terminated.Store(terminate(cmd.Process))
		case <-stopWatch:
		}
	}()

	// Wait returns the exit error of a failed status, which the state carries.
	_ = cmd.Wait()
	// A descendant that holds a pipe may still be running on every platform.
	pipes.Wait(func() bool { return true })
	close(stopWatch)
	<-watchDone

	result := ExecResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		Killed: killed.Load(),
	}
	// A child that a signal ended has no exit code (ExitCode is -1), nor has
	// a Windows child that terminate ended, and upstream resolves `code ?? 0`.
	switch state := cmd.ProcessState; {
	case state == nil:
		result.Code = 1
	case terminated.Load():
		result.Code = 0
	default:
		result.Code = max(state.ExitCode(), 0)
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
