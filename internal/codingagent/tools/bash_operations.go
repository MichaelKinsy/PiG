// Shell process execution shared by the bash and powershell tools and user
// bash.
//
// Mirrors upstream core/tools/bash.ts BashOperations and
// createLocalShellOperations. internal/childwait applies utils/child-process.ts
// waitForChildProcess for the post-exit stdio grace.
package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/childwait"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// BashOperationsExecOptions, BashOperationsResult and BashOperations are the
// public extension contract (coding/extension), so an extension's operations
// plug straight into the shell tools and user bash.
type (
	BashOperationsExecOptions = extension.BashOperationsExecOptions
	BashOperationsResult      = extension.BashOperationsResult
	BashOperations            = extension.BashOperations
)

// errBashAborted mirrors upstream's `new Error("aborted")`.
var errBashAborted = errors.New("aborted")

// maxBashTimeoutMs mirrors upstream MAX_TIMEOUT_MS.
const maxBashTimeoutMs = 2_147_483_647

// maxBashTimeoutSeconds mirrors upstream MAX_TIMEOUT_SECONDS.
const maxBashTimeoutSeconds = maxBashTimeoutMs / 1000.0

// resolveTimeoutMs mirrors upstream resolveTimeoutMs.
func resolveTimeoutMs(timeout *float64) (time.Duration, bool, error) {
	if timeout == nil {
		return 0, false, nil
	}
	if math.IsNaN(*timeout) || math.IsInf(*timeout, 0) || *timeout <= 0 {
		return 0, false, errors.New("Invalid timeout: must be a finite number of seconds")
	}
	timeoutMs := *timeout * 1000
	if timeoutMs > maxBashTimeoutMs {
		return 0, false, errors.New("Invalid timeout: maximum is " + jsNumber(maxBashTimeoutSeconds) + " seconds")
	}
	return time.Duration(timeoutMs * float64(time.Millisecond)), true, nil
}

// LocalShellOperations mirrors upstream createLocalShellOperations(shellName,
// resolveShellConfig), with PowerShell's command wrapper
// (createLocalPowerShellOperations) as an optional hook.
type LocalShellOperations struct {
	ShellName    string
	ResolveShell func() (ShellConfig, error)
	// WrapCommand, when set, rewrites the command before execution.
	WrapCommand func(string) string
	// BinDir is prepended to PATH when the caller passes no environment
	// (upstream getShellEnv's getBinDir).
	BinDir string
}

// NewLocalBashOperations mirrors upstream createLocalBashOperations: local
// execution through getShellConfig(settings.getShellPath()), read when a
// command runs, so an invalid shell path fails that command.
func NewLocalBashOperations(settings SettingsView, binDir string) *LocalShellOperations {
	return &LocalShellOperations{
		ShellName:    "bash",
		BinDir:       binDir,
		ResolveShell: func() (ShellConfig, error) { return GetShellConfig(settings) },
	}
}

// Exec runs command through the resolved shell, streaming output to
// opts.OnData, and waits for the shell (not its background descendants).
func (o *LocalShellOperations) Exec(ctx context.Context, command, cwd string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
	if o.WrapCommand != nil {
		command = o.WrapCommand(command)
	}
	timeout, hasTimeout, err := resolveTimeoutMs(opts.Timeout)
	if err != nil {
		return BashOperationsResult{}, err
	}
	if ctx.Err() != nil {
		return BashOperationsResult{}, errBashAborted
	}
	shell, err := o.ResolveShell()
	if err != nil {
		return BashOperationsResult{}, err
	}
	if _, err := os.Stat(cwd); err != nil {
		return BashOperationsResult{}, fmt.Errorf("Working directory does not exist: %s\nCannot execute %s commands.", cwd, o.ShellName)
	}
	env := opts.Env
	if env == nil {
		env = GetShellEnv(o.BinDir)
	}

	commandFromStdin := shell.CommandTransport == "stdin"
	args := append([]string{}, shell.Args...)
	if !commandFromStdin {
		args = append(args, command)
	}
	cmd := exec.Command(shell.Path, args...)
	cmd.Dir = cwd
	// Upstream passes env as the spawn's env option.
	nodespawn.SetEnv(cmd, env)
	// The shell writes straight into an OS pipe (no exec copy goroutine), so
	// Wait returns when the shell exits even if a background descendant still
	// holds the write end. childwait then applies upstream's grace.
	pr, pw, err := os.Pipe()
	if err != nil {
		return BashOperationsResult{}, err
	}
	cmd.Stdout = pw
	cmd.Stderr = pw
	var stdinWrite *os.File
	if commandFromStdin {
		var stdinRead *os.File
		if stdinRead, stdinWrite, err = os.Pipe(); err != nil {
			_ = pw.Close()
			_ = pr.Close()
			return BashOperationsResult{}, err
		}
		cmd.Stdin = stdinRead
	}
	// Process group so abort and timeout reap descendants (upstream spawns
	// detached on non-win32 and kills the tree).
	setProcessGroup(cmd)
	// Upstream spawns with windowsHide: true and stdio
	// [commandFromStdin ? "pipe" : "ignore", "pipe", "pipe"].
	stdin := nodespawn.Ignore
	if commandFromStdin {
		stdin = nodespawn.Pipe
	}
	nodespawn.HideWindow(cmd, stdin, nodespawn.Pipe, nodespawn.Pipe)
	// Upstream starts the shell with Node's spawn, which finds it with libuv's
	// search on Windows. The shell must receive that command line byte for
	// byte: Git Bash parses it with MSYS2 rules, not the C runtime's.
	nodespawn.SetProgram(cmd)
	nodespawn.SetCommandLine(cmd)
	// Start closes the child's ends of the pipes.
	if err := nodespawn.Start(cmd); err != nil {
		_ = pr.Close()
		if stdinWrite != nil {
			_ = stdinWrite.Close()
		}
		return BashOperationsResult{}, &shellSpawnError{path: shell.Path, cause: err}
	}
	attachProcessGroup(cmd.Process)
	defer releaseProcessGroup(cmd.Process)
	// Upstream tracks the child until its wait ends, so an exit without orderly shutdown kills it.
	trackDetachedChild(cmd.Process)
	defer untrackDetachedChild(cmd.Process)
	// os/exec copies a reader to the child's stdin the same way, and Wait
	// waits for that copy.
	var input sync.WaitGroup
	if stdinWrite != nil {
		input.Go(func() {
			// A write error means that the shell stopped reading.
			_, _ = io.WriteString(stdinWrite, command)
			_ = stdinWrite.Close()
		})
	}

	var (
		killOnce sync.Once
		timedOut bool
		timeMu   sync.Mutex
	)
	kill := func() { killOnce.Do(func() { _ = killProcessGroup(cmd.Process) }) }
	stopWatch := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		var timer <-chan time.Time
		if hasTimeout {
			t := time.NewTimer(timeout)
			defer t.Stop()
			timer = t.C
		}
		select {
		case <-ctx.Done():
			kill()
		case <-timer:
			timeMu.Lock()
			timedOut = true
			timeMu.Unlock()
			kill()
		case <-stopWatch:
		}
	}()

	pipes := childwait.Start([]*os.File{pr}, func(_ int, chunk []byte) {
		if opts.OnData != nil {
			opts.OnData(chunk)
		}
	}, childwait.Hooks{BeforeRead: testHookBeforeStdioRead, GraceExpired: testHookStdioGraceExpired})

	waitErr := cmd.Wait()
	input.Wait()
	pipes.Wait(func() bool { return processGroupMayHoldOutput(cmd.Process) })
	close(stopWatch)
	<-watchDone

	if ctx.Err() != nil {
		return BashOperationsResult{}, errBashAborted
	}
	timeMu.Lock()
	didTimeOut := timedOut
	timeMu.Unlock()
	if didTimeOut {
		return BashOperationsResult{}, errors.New("timeout:" + jsNumber(*opts.Timeout))
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return BashOperationsResult{}, waitErr
	}
	code := shellExitCode(cmd.ProcessState)
	return BashOperationsResult{ExitCode: &code}, nil
}

// shellSpawnError preserves the spawn errno with Node's user-visible message.
type shellSpawnError struct {
	path  string
	cause error
}

func (e *shellSpawnError) Error() string {
	if spawnErr, ok := errors.AsType[*nodespawn.Error](e.cause); ok {
		return spawnErr.Error()
	}
	code := nodeErrorCode(e.cause)
	if e.notFound() {
		code = "ENOENT"
	}
	if code != "" {
		return "spawn " + e.path + " " + code
	}
	return e.cause.Error()
}
func (e *shellSpawnError) Unwrap() error { return e.cause }

// Is reports a shell that os/exec could not find as fs.ErrNotExist.
func (e *shellSpawnError) Is(target error) bool {
	return target == fs.ErrNotExist && e.notFound()
}

// notFound reports that os/exec found no executable file for the shell. It
// reports a missing one as exec.ErrNotFound; libuv reports the same spawn as
// ENOENT.
func (e *shellSpawnError) notFound() bool { return errors.Is(e.cause, exec.ErrNotFound) }

// Test hooks that order the reader, the shell's exit and the grace without
// sleeps. Nil outside tests.
var (
	testHookBeforeStdioRead   func()
	testHookStdioGraceExpired func()
)
