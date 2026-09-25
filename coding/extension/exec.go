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
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// ExecCommand runs a shell command synchronously and returns the result.
// This is the default implementation backing API.Exec when the host
// doesn't provide an override.
//
// The command is spawned directly (no shell wrapping). Use args to pass
// arguments. If opts.CWD is empty, cwd is used as the working directory.
// Like upstream, a program that cannot start does not fail the call: the
// result has code 1 and empty output.
//
// Timeout and context cancellation both trigger SIGTERM followed by
// SIGKILL after 5 seconds.
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

	// Apply timeout if specified.
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.Timeout)*time.Millisecond)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	// Detach the child into its own process group so a timeout/cancel can
	// target the whole tree without signalling pig itself.
	cmd.SysProcAttr = newProcAttr()
	// Like upstream, killed reports that the timeout or cancellation killed
	// the process. The exit status cannot tell: a process killed on Windows
	// exits with status 1.
	var killed atomic.Bool
	cmd.Cancel = func() error {
		killed.Store(true)
		return cmd.Process.Kill()
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Start()
	// exec() has now completed its synchronous prefix: the spawn was attempted
	// and, on success, the child is running. A subprocess host may advance the
	// extension's ordered call lane while this command completes independently.
	CallInitiated(ctx)
	if err == nil {
		err = cmd.Wait()
	}

	result := ExecResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		exitErr := &exec.ExitError{}
		switch {
		case errors.As(err, &exitErr):
			result.Code = exitErr.ExitCode()
			result.Killed = killed.Load()
		case ctx.Err() != nil:
			// Context cancelled or timed out before process started.
			result.Killed = true
			result.Code = 1
		default:
			// Like upstream, a program that cannot start (missing program
			// or working directory) resolves with code 1 instead of failing.
			result.Code = 1
		}
	}

	return result, nil
}
