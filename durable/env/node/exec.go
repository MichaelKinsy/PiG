package node

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// Ports packages/durable/src/env/node.ts (NodeExecutionEnv.exec).

const (
	// exitStdioGrace bounds how long output may keep arriving after the shell
	// exits while a detached descendant still holds its stdio.
	exitStdioGrace  = 100 * time.Millisecond
	outputChunkSize = 64 * 1024
)

func abortedExecution() *durableenv.ExecutionError {
	return &durableenv.ExecutionError{Code: durableenv.ExecutionErrorAborted, Message: "aborted"}
}

// Exec runs command through bash in the environment's cwd (or options.Cwd).
// Every decoded chunk of combined stdout and stderr goes to options.OnOutput as
// it arrives. A non-zero exit is a successful result. Cancellation of ctx, the
// timeout, a failing OnOutput, and a failed requested spill each kill the
// process tree and fail the call.
func (env *NodeExecutionEnv) Exec(ctx context.Context, command string, options *durableenv.ShellExecOptions) (durableenv.ShellExecResult, error) {
	if ctx.Err() != nil {
		return durableenv.ShellExecResult{}, abortedExecution()
	}
	if options == nil {
		options = &durableenv.ShellExecOptions{}
	}
	timeout, err := resolveTimeout(options.Timeout)
	if err != nil {
		return durableenv.ShellExecResult{}, err
	}
	cwd := env.Cwd()
	if options.Cwd != "" {
		cwd = resolvePath(cwd, options.Cwd)
	}
	config, err := getShellConfig(ctx, env.shellPath)
	if err != nil {
		return durableenv.ShellExecResult{}, err
	}
	if _, statErr := os.Stat(cwd); statErr != nil {
		return durableenv.ShellExecResult{}, &durableenv.ExecutionError{
			Code:    durableenv.ExecutionErrorSpawnError,
			Message: "Working directory does not exist: " + cwd + "\nCannot execute bash commands.",
			Cause:   statErr,
		}
	}
	return newShellRun(ctx, env, options).execute(command, config, cwd, timeout)
}

// shellRun owns one command: its process, output pumps, timers, and optional
// spill file.
type shellRun struct {
	env     *NodeExecutionEnv
	ctx     context.Context
	options *durableenv.ShellExecOptions

	errMu         sync.Mutex
	pid           int
	timedOut      bool
	callbackError *durableenv.ExecutionError
	spillError    *durableenv.ExecutionError

	// feedMu serializes the pumps' chunks: decoding, emission, and spilling.
	feedMu sync.Mutex
	// One decoder per stream, so a character split across chunks of one stream
	// survives interleaving.
	stdoutDecoder, stderrDecoder utf8StreamDecoder
	// Output seen before the spill starts: counted against the thresholds and
	// kept for the spill's prefix.
	spillPrefix  [][]byte
	seenBytes    int
	seenNewlines int
	spillStarted bool
	spillFile    *os.File
	spillPath    string

	feeding  atomic.Int32
	activity chan struct{}
}

func newShellRun(ctx context.Context, env *NodeExecutionEnv, options *durableenv.ShellExecOptions) *shellRun {
	return &shellRun{env: env, ctx: ctx, options: options, activity: make(chan struct{}, 1)}
}

func (run *shellRun) execute(command string, config shellConfig, cwd string, timeout time.Duration) (durableenv.ShellExecResult, error) {
	cmd, stdin, stdout, stderr, err := run.start(command, config, cwd)
	if err != nil {
		return durableenv.ShellExecResult{}, err
	}
	defer run.env.untrackChild(cmd.Process.Pid)
	if timeout > 0 {
		onTimeout, drainTimeout := ownedCallback(run.timeout)
		timer := time.AfterFunc(timeout, onTimeout)
		defer drainTimeout(timer.Stop)
	}
	onAbort, drainAbort := ownedCallback(run.kill)
	defer drainAbort(context.AfterFunc(run.ctx, onAbort))

	var input sync.WaitGroup
	if stdin != nil {
		input.Go(func() { writeCommand(stdin, command) })
	}
	var pumps sync.WaitGroup
	pumps.Go(func() { run.pump(stdout, &run.stdoutDecoder) })
	pumps.Go(func() { run.pump(stderr, &run.stderrDecoder) })
	_ = cmd.Wait()
	// os/exec's Wait also waits for the goroutine that copies a reader to the
	// child's stdin.
	input.Wait()
	run.drain(&pumps, stdout, stderr)
	run.finishSpill()
	run.flushDecoders()
	return run.result(cmd.ProcessState)
}

// start spawns the shell with piped stdout/stderr as the leader of its own
// process group and registers it for Cleanup. A shell that reads the command
// from stdin gets a pipe, whose write end start returns; the command is an
// argument otherwise, and stdin is nil.
func (run *shellRun) start(command string, config shellConfig, cwd string) (*exec.Cmd, *os.File, *os.File, *os.File, error) {
	spawnError := func(err error) error {
		return &durableenv.ExecutionError{Code: durableenv.ExecutionErrorSpawnError, Message: err.Error(), Cause: err}
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, nil, spawnError(err)
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		closeAll(stdoutRead, stdoutWrite)
		return nil, nil, nil, nil, spawnError(err)
	}
	args := config.args
	if !config.commandFromStdin {
		args = append(append([]string(nil), args...), command)
	}
	cmd := exec.Command(config.shell, args...)
	cmd.Dir = cwd
	nodespawn.SetEnvProperties(cmd, getShellEnv(run.env.shellEnv, run.options.Env, run.options.InheritEnv))
	cmd.Stdout = stdoutWrite
	cmd.Stderr = stderrWrite
	cmd.SysProcAttr = detachedProcessAttributes()
	stdin := nodespawn.Ignore
	var stdinWrite *os.File
	if config.commandFromStdin {
		var stdinRead *os.File
		if stdinRead, stdinWrite, err = os.Pipe(); err != nil {
			closeAll(stdoutRead, stdoutWrite, stderrRead, stderrWrite)
			return nil, nil, nil, nil, spawnError(err)
		}
		cmd.Stdin = stdinRead
		stdin = nodespawn.Pipe
	}
	// Upstream spawns with stdio [commandFromStdin ? "pipe" : "ignore",
	// "pipe", "pipe"] and windowsHide: true.
	nodespawn.HideWindow(cmd, stdin, nodespawn.Pipe, nodespawn.Pipe)
	// Upstream starts the shell with Node's spawn, which finds it with libuv's
	// search on Windows. The shell must receive that command line byte for
	// byte: Git Bash parses it with MSYS2 rules, not the C runtime's.
	nodespawn.SetProgram(cmd)
	nodespawn.SetCommandLine(cmd)
	// startChild closes the child's ends of the pipes.
	if startErr := run.env.startChild(cmd); startErr != nil {
		closeAll(stdoutRead, stderrRead)
		if stdinWrite != nil {
			closeAll(stdinWrite)
		}
		return nil, nil, nil, nil, &durableenv.ExecutionError{Code: durableenv.ExecutionErrorSpawnError, Message: spawnErrorMessage(config.shell, startErr), Cause: startErr}
	}
	run.errMu.Lock()
	run.pid = cmd.Process.Pid
	run.errMu.Unlock()
	return cmd, stdinWrite, stdoutRead, stderrRead, nil
}

// writeCommand writes command to the shell's stdin and closes it, as os/exec
// copies a reader to a child's stdin. A write error means that the shell
// stopped reading; Exec ignores it, as it ignores the error of cmd.Wait that
// reports it.
func writeCommand(stdin *os.File, command string) {
	_, _ = io.WriteString(stdin, command)
	closeAll(stdin)
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
		run.callbackError = &durableenv.ExecutionError{Code: durableenv.ExecutionErrorCallbackError, Message: err.Error(), Cause: err}
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
		run.spillError = &durableenv.ExecutionError{Code: durableenv.ExecutionErrorUnknown, Message: "Failed to preserve complete shell output: " + err.Error(), Cause: err}
	}
	run.errMu.Unlock()
	if first {
		run.kill()
	}
}

func (run *shellRun) pump(reader *os.File, decoder *utf8StreamDecoder) {
	buffer := make([]byte, outputChunkSize)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			run.feed(buffer[:count], decoder)
		}
		if err != nil {
			return
		}
	}
}

// drain waits for both pumps after exit. A descendant that inherited stdio can
// hold the pipes open, so once no output has arrived for exitStdioGrace and no
// chunk is mid-feed (a pending spill write counts as mid-feed) the read ends are
// closed.
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

// feed hands one raw chunk to the caller as decoded text and, when a spill was
// requested, preserves the exact bytes once the output crosses a threshold.
func (run *shellRun) feed(chunk []byte, decoder *utf8StreamDecoder) {
	run.feeding.Add(1)
	defer run.feeding.Add(-1)
	select {
	case run.activity <- struct{}{}:
	default:
	}
	run.feedMu.Lock()
	defer run.feedMu.Unlock()
	run.emit(decoder.decode(chunk))
	spill := run.options.Spill
	if spill == nil || len(chunk) == 0 {
		return
	}
	if run.spillStarted {
		run.writeSpill(chunk)
		return
	}
	run.seenBytes += len(chunk)
	run.seenNewlines += bytes.Count(chunk, []byte{'\n'})
	lines := run.seenNewlines
	if chunk[len(chunk)-1] != '\n' {
		lines++
	}
	if int64(run.seenBytes) <= spill.AfterBytes && int64(lines) <= spill.AfterLines {
		run.spillPrefix = append(run.spillPrefix, append([]byte(nil), chunk...))
		return
	}
	run.spillStarted = true
	for _, prefix := range run.spillPrefix {
		run.writeSpill(prefix)
	}
	run.spillPrefix = nil
	run.writeSpill(chunk)
}

// emit passes text to OnOutput unless the text is empty or a callback already
// failed. Callers hold feedMu.
func (run *shellRun) emit(text string) {
	if text == "" || run.options.OnOutput == nil {
		return
	}
	run.errMu.Lock()
	failed := run.callbackError != nil
	run.errMu.Unlock()
	if failed {
		return
	}
	if err := run.callOnOutput(text); err != nil {
		run.failCallback(err)
	}
}

// callOnOutput delivers text to OnOutput. A panic in the callback is what a
// throw is upstream: it fails the command with a callback_error.
func (run *shellRun) callOnOutput(text string) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = durableenv.ToError(recovered)
		}
	}()
	run.options.OnOutput(run.ctx, text)
	return nil
}

// writeSpillChunk writes one chunk to the spill file; tests replace it to make
// spill writes slow.
var writeSpillChunk = (*os.File).Write

// writeSpill appends a chunk to the spill file, creating the file on the first
// chunk. The pump blocks while the write is pending, which is the backpressure
// that keeps a slow spill from buffering unbounded output.
func (run *shellRun) writeSpill(chunk []byte) {
	if run.spillFile == nil {
		if run.spillFailed() {
			return
		}
		if !run.openSpill() {
			return
		}
	}
	if _, err := writeSpillChunk(run.spillFile, chunk); err != nil {
		run.failSpill(err)
		closeQuietly(run.spillFile)
		run.spillFile = nil
	}
}

func (run *shellRun) spillFailed() bool {
	run.errMu.Lock()
	defer run.errMu.Unlock()
	return run.spillError != nil
}

func (run *shellRun) openSpill() bool {
	path, err := run.env.Self.CreateTempFile(run.ctx, &durableenv.CreateTempFileOptions{Prefix: "pi-output-", Suffix: ".log"})
	if err != nil {
		run.failSpill(err)
		return false
	}
	run.spillPath = path
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
	if err != nil {
		run.failSpill(err)
		return false
	}
	run.spillFile = file
	return true
}

func (run *shellRun) finishSpill() {
	run.feedMu.Lock()
	defer run.feedMu.Unlock()
	if run.spillFile != nil {
		closeQuietly(run.spillFile)
		run.spillFile = nil
	}
}

// flushDecoders emits what each decoder still holds, a character cut short by
// the end of its stream.
func (run *shellRun) flushDecoders() {
	run.feedMu.Lock()
	defer run.feedMu.Unlock()
	run.emit(run.stdoutDecoder.flush())
	run.emit(run.stderrDecoder.flush())
}

func (run *shellRun) result(state *os.ProcessState) (durableenv.ShellExecResult, error) {
	run.feedMu.Lock()
	spillPath := run.spillPath
	run.feedMu.Unlock()
	run.errMu.Lock()
	callbackError, spillError, timedOut := run.callbackError, run.spillError, run.timedOut
	run.errMu.Unlock()
	var interrupted *durableenv.ExecutionError
	switch {
	case callbackError != nil:
		return durableenv.ShellExecResult{}, callbackError
	case timedOut:
		interrupted = &durableenv.ExecutionError{Code: durableenv.ExecutionErrorTimeout, Message: "timeout:" + formatNumber(*run.options.Timeout)}
	case run.ctx.Err() != nil:
		interrupted = abortedExecution()
	}
	if interrupted != nil {
		interrupted.SpillPath = spillPath
		return durableenv.ShellExecResult{}, interrupted
	}
	if spillError != nil {
		return durableenv.ShellExecResult{}, spillError
	}
	return durableenv.ShellExecResult{ExitCode: exitCode(state), SpillPath: spillPath}, nil
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
