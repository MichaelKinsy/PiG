package env

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/utils"
)

const (
	// exitStdioGrace bounds how long output may keep arriving after the
	// shell exits while a detached descendant still holds its stdio.
	exitStdioGrace  = 100 * time.Millisecond
	outputChunkSize = 64 * 1024
)

func abortedExecution() error {
	return &harness.ExecutionError{Code: harness.ExecutionErrorAborted, Message: "aborted"}
}

// Exec runs command through bash in the environment cwd (or options.Cwd). The
// combined stdout/stderr view is bounded and published through
// options.OnUpdate; a non-zero exit is a successful result. Cancellation of
// ctx, the timeout, a failing OnUpdate, and a failed requested spill each kill
// the process tree and fail the call.
func (env *NodeExecutionEnv) Exec(ctx context.Context, command string, options *harness.ShellExecOptions) (harness.ShellExecResult, error) {
	if ctx.Err() != nil {
		return harness.ShellExecResult{}, abortedExecution()
	}
	if options == nil {
		options = &harness.ShellExecOptions{}
	}
	timeout, err := resolveTimeout(options.Timeout)
	if err != nil {
		return harness.ShellExecResult{}, err
	}
	cwd := env.cwd
	if options.Cwd != "" {
		cwd = resolvePath(env.cwd, options.Cwd)
	}
	config, err := getShellConfig(ctx, env.shellPath)
	if err != nil {
		return harness.ShellExecResult{}, err
	}
	if _, statErr := os.Stat(cwd); statErr != nil {
		return harness.ShellExecResult{}, &harness.ExecutionError{
			Code:    harness.ExecutionErrorSpawnError,
			Message: "Working directory does not exist: " + cwd + "\nCannot execute bash commands.",
			Cause:   statErr,
		}
	}
	run, err := newShellRun(ctx, env, options)
	if err != nil {
		return harness.ShellExecResult{}, err
	}
	defer run.capture.Dispose()
	return run.execute(command, config, cwd, timeout)
}

// shellRun owns one command: its process, output pumps, timers, capture, and
// optional spill file.
type shellRun struct {
	env     *NodeExecutionEnv
	ctx     context.Context
	options *harness.ShellExecOptions
	capture *utils.OutputCapture

	errMu         sync.Mutex
	pid           int
	timedOut      bool
	callbackError *harness.ExecutionError
	spillError    *harness.ExecutionError

	feedMu       sync.Mutex
	spillPrefix  [][]byte
	spillStarted bool
	spillFile    *os.File

	feeding  atomic.Int32
	activity chan struct{}
}

func newShellRun(ctx context.Context, env *NodeExecutionEnv, options *harness.ShellExecOptions) (*shellRun, error) {
	run := &shellRun{env: env, ctx: ctx, options: options, activity: make(chan struct{}, 1)}
	handlers := utils.OutputCaptureHandlers{OnError: run.failCallback}
	if options.OnUpdate != nil {
		handlers.OnUpdate = func(ctx context.Context, update harness.ShellOutputUpdate) {
			if err := options.OnUpdate(ctx, update); err != nil {
				run.failCallback(err)
			}
		}
	}
	capture, err := utils.NewOutputCapture(ctx, options.Capture, handlers)
	if err != nil {
		return nil, &harness.ExecutionError{Code: harness.ExecutionErrorUnknown, Message: err.Error(), Cause: err}
	}
	run.capture = capture
	return run, nil
}

func (run *shellRun) execute(command string, config shellConfig, cwd string, timeout time.Duration) (harness.ShellExecResult, error) {
	cmd, stdout, stderr, err := run.start(command, config, cwd)
	if err != nil {
		return harness.ShellExecResult{}, err
	}
	pid := cmd.Process.Pid
	run.env.trackChild(pid, true)
	defer run.env.trackChild(pid, false)
	if timeout > 0 {
		onTimeout, drainTimeout := ownedCallback(run.timeout)
		timer := time.AfterFunc(timeout, onTimeout)
		defer drainTimeout(timer.Stop)
	}
	onAbort, drainAbort := ownedCallback(run.kill)
	defer drainAbort(context.AfterFunc(run.ctx, onAbort))

	var pumps sync.WaitGroup
	pumps.Go(func() { run.pump(stdout) })
	pumps.Go(func() { run.pump(stderr) })
	_ = cmd.Wait()
	run.drain(&pumps, stdout, stderr)
	run.finishSpill()
	run.capture.Finish()
	run.capture.Flush()
	return run.result(cmd.ProcessState)
}

// start spawns the shell with piped stdout/stderr as the leader of its own
// process group.
func (run *shellRun) start(command string, config shellConfig, cwd string) (*exec.Cmd, *os.File, *os.File, error) {
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, &harness.ExecutionError{Code: harness.ExecutionErrorSpawnError, Message: err.Error(), Cause: err}
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		closeAll(stdoutRead, stdoutWrite)
		return nil, nil, nil, &harness.ExecutionError{Code: harness.ExecutionErrorSpawnError, Message: err.Error(), Cause: err}
	}
	args := config.args
	if !config.commandFromStdin {
		args = append(append([]string(nil), args...), command)
	}
	cmd := exec.Command(config.shell, args...)
	cmd.Dir = cwd
	cmd.Env = getShellEnv(run.env.shellEnv, run.options.Env, run.options.InheritEnv)
	cmd.Stdout = stdoutWrite
	cmd.Stderr = stderrWrite
	cmd.SysProcAttr = detachedProcessAttributes()
	if config.commandFromStdin {
		cmd.Stdin = strings.NewReader(command)
	}
	startErr := cmd.Start()
	closeAll(stdoutWrite, stderrWrite)
	if startErr != nil {
		closeAll(stdoutRead, stderrRead)
		return nil, nil, nil, &harness.ExecutionError{Code: harness.ExecutionErrorSpawnError, Message: spawnErrorMessage(config.shell, startErr), Cause: startErr}
	}
	run.errMu.Lock()
	run.pid = cmd.Process.Pid
	run.errMu.Unlock()
	return cmd, stdoutRead, stderrRead, nil
}

// ownedCallback wraps fn for a timer or context callback. drain stops the
// registration and, when fn already started, waits for it to finish so no
// callback outlives the command.
func ownedCallback(fn func()) (callback func(), drain func(stop func() bool)) {
	finished := make(chan struct{})
	callback = func() {
		defer close(finished)
		fn()
	}
	drain = func(stop func() bool) {
		if !stop() {
			<-finished
		}
	}
	return callback, drain
}

func closeAll(files ...*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}

func (run *shellRun) kill() {
	run.errMu.Lock()
	pid := run.pid
	run.errMu.Unlock()
	if pid > 0 {
		_ = killProcessTree(pid)
	}
}

func (run *shellRun) timeout() {
	run.errMu.Lock()
	run.timedOut = true
	run.errMu.Unlock()
	run.kill()
}

func (run *shellRun) failCallback(err error) {
	run.errMu.Lock()
	first := run.callbackError == nil
	if first {
		run.callbackError = &harness.ExecutionError{Code: harness.ExecutionErrorCallbackError, Message: err.Error(), Cause: err}
	}
	run.errMu.Unlock()
	if first {
		run.kill()
	}
}

func (run *shellRun) failSpill(err error) {
	run.errMu.Lock()
	first := run.spillError == nil
	if first {
		run.spillError = &harness.ExecutionError{Code: harness.ExecutionErrorUnknown, Message: "Failed to preserve complete shell output: " + err.Error(), Cause: err}
	}
	run.errMu.Unlock()
	if first {
		run.kill()
	}
}

func (run *shellRun) pump(reader *os.File) {
	buffer := make([]byte, outputChunkSize)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			run.feed(buffer[:count])
		}
		if err != nil {
			return
		}
	}
}

// drain waits for both pumps after exit. A descendant that inherited stdio can
// hold the pipes open, so once no output has arrived for exitStdioGrace (and
// no chunk is mid-feed) the read ends are closed.
func (run *shellRun) drain(pumps *sync.WaitGroup, stdout, stderr *os.File) {
	defer closeAll(stdout, stderr)
	done := make(chan struct{})
	go func() {
		pumps.Wait()
		close(done)
	}()
	grace := time.NewTimer(exitStdioGrace)
	defer grace.Stop()
	for {
		select {
		case <-done:
			return
		case <-run.activity:
			grace.Reset(exitStdioGrace)
		case <-grace.C:
			if run.feeding.Load() > 0 {
				grace.Reset(exitStdioGrace)
				continue
			}
			closeAll(stdout, stderr)
			<-done
			return
		}
	}
}

// feed pushes one raw chunk into the bounded view and, when a spill was
// requested, preserves the exact bytes once the view first truncates.
func (run *shellRun) feed(chunk []byte) {
	run.feeding.Add(1)
	defer run.feeding.Add(-1)
	select {
	case run.activity <- struct{}{}:
	default:
	}
	run.feedMu.Lock()
	defer run.feedMu.Unlock()
	wasTruncated := run.capture.Truncated()
	run.capture.Push(chunk)
	if run.options.Capture == nil || !run.options.Capture.Spill || len(chunk) == 0 {
		return
	}
	switch {
	case run.spillStarted || wasTruncated:
		run.writeSpill(chunk)
	case run.capture.Truncated():
		for _, prefix := range run.spillPrefix {
			run.writeSpill(prefix)
		}
		run.spillPrefix = nil
		run.writeSpill(chunk)
	default:
		run.spillPrefix = append(run.spillPrefix, append([]byte(nil), chunk...))
	}
}

func (run *shellRun) writeSpill(chunk []byte) {
	if !run.spillStarted {
		run.spillStarted = true
		run.openSpill()
	}
	if run.spillFile == nil {
		return
	}
	if _, err := run.spillFile.Write(chunk); err != nil {
		run.failSpill(err)
		_ = run.spillFile.Close()
		run.spillFile = nil
	}
}

func (run *shellRun) openSpill() {
	path, err := run.env.spillTempFile(run.ctx, &harness.CreateTempFileOptions{Prefix: "pi-output-", Suffix: ".log"})
	if err != nil {
		run.failSpill(err)
		return
	}
	run.capture.SetSpillPath(path)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
	if err != nil {
		run.failSpill(err)
		return
	}
	run.spillFile = file
}

func (run *shellRun) finishSpill() {
	run.feedMu.Lock()
	defer run.feedMu.Unlock()
	if run.spillFile != nil {
		_ = run.spillFile.Close()
		run.spillFile = nil
	}
}

func (run *shellRun) result(state *os.ProcessState) (harness.ShellExecResult, error) {
	run.errMu.Lock()
	callbackError, spillError, timedOut := run.callbackError, run.spillError, run.timedOut
	run.errMu.Unlock()
	switch {
	case callbackError != nil:
		return harness.ShellExecResult{}, callbackError
	case timedOut:
		return harness.ShellExecResult{}, &harness.ExecutionError{Code: harness.ExecutionErrorTimeout, Message: "timeout:" + formatNumber(*run.options.Timeout)}
	case run.ctx.Err() != nil:
		return harness.ShellExecResult{}, abortedExecution()
	case spillError != nil:
		return harness.ShellExecResult{}, spillError
	}
	output := run.capture.Snapshot()
	return harness.ShellExecResult{ExitCode: exitCode(state), ShellOutputMetadata: output.ShellOutputMetadata}, nil
}

// exitCode maps a signal-terminated process to 128 + signal number so callers
// do not mistake it for a successful exit.
func exitCode(state *os.ProcessState) int {
	if state == nil {
		return 1
	}
	if code := state.ExitCode(); code >= 0 {
		return code
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok {
		if code, signaled := signalExitCode(status); signaled {
			return code
		}
	}
	return 1
}
