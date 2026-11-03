package daemon

// Ports packages/env/daemon/src/process.rs
//
// `process`: a long-lived program with plain pipes, for programs that speak a protocol on stdin and stdout (an MCP server).
// Unlike `exec`, the program reads input (`processWrite`, `processCloseStdin`, addressed by the `process` request's id) and
// output is raw bytes per stream; unlike `pty`, nothing is echoed, translated or merged.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// processExitGrace is how much silence ends the collection of output still arriving after the program exited, as for `exec`.
const processExitGrace = 100 * time.Millisecond

type processRequest struct {
	argv       []string
	cwd        string
	env        map[string]string
	inheritEnv bool
}

func parseProcessRequest(request Object) (*processRequest, *Failure) {
	argv, failure := parseArgv(request)
	if failure != nil {
		return nil, failure
	}
	if argv == nil {
		return nil, newFailure("EINVAL", "process needs argv")
	}
	cwd, ok := request["cwd"].(string)
	if !ok {
		return nil, newFailure("EINVAL", "process needs cwd")
	}
	environment, inherit := requestEnvironment(request)
	return &processRequest{argv: argv, cwd: cwd, env: environment, inheritEnv: inherit}, nil
}

// runningProcess is what `processWrite` and `processCloseStdin` reach: the stdin of one running `process` request, and the
// process group a lost connection kills.
type runningProcess struct {
	input *inputQueue
	pid   int
}

// processWrite queues input for the program's stdin; it is written in the order it arrives, without holding up other
// requests.
func (s *server) processWrite(request Object, payload []byte) (Object, *Failure) {
	found, failure := findRunning(s, s.processes, request, "process")
	if failure != nil {
		return nil, failure
	}
	if !found.input.push(payload) {
		return nil, newFailure("EBADF", "the program's stdin is closed")
	}
	return Object{}, nil
}

// processCloseStdin closes the program's stdin once the input queued before has been written.
func (s *server) processCloseStdin(request Object) (Object, *Failure) {
	found, failure := findRunning(s, s.processes, request, "process")
	if failure != nil {
		return nil, failure
	}
	if !found.input.finish() {
		return nil, newFailure("EBADF", "the program's stdin is closed")
	}
	return Object{}, nil
}

type programMessageKind int

const (
	programData programMessageKind = iota
	programEOF
	programExit
	programWaitFailed
)

type programMessage struct {
	kind   programMessageKind
	stream int
	data   []byte
	exit   int
	err    error
}

// programExitCode is the exit code of a program that ended, 128 plus the signal for a program a signal ended; on Windows
// it is the unsigned 32-bit code.
func programExitCode(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return state.ExitCode()
}

// reapProgram reaps the program and reports how it ended.
func reapProgram(cmd *exec.Cmd) (int, error) {
	if err := cmd.Wait(); err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			return 0, err
		}
	}
	return programExitCode(cmd.ProcessState), nil
}

// runProcess runs the program, sending a `started` event and then its output, until it exits.
func (s *server) runProcess(id uint32, request *processRequest, ctl *control) (Object, *Failure) {
	//portlint:allow ctxbackground a request ends through the client's cancel frame, which control.attach connects (process.rs:run, exec.rs Control)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctl.attach(cancel)
	if ctl.aborted.Load() {
		return nil, newFailure("aborted", "aborted")
	}
	if !pathExists(request.cwd) {
		return nil, newFailure("spawn_error", "Working directory does not exist: "+request.cwd)
	}
	program, failure := startProgram(request)
	if failure != nil {
		return nil, failure
	}
	input := newInputQueue()
	// Registered as soon as the program runs, so a lost connection reaches it and input sent once `started` is out finds it.
	s.mu.Lock()
	s.processes[id] = &runningProcess{input: input, pid: program.pid}
	s.mu.Unlock()
	s.out.sendControl(&Frame{Kind: FrameEvent, ID: id, JSON: Object{"kind": "started", "pid": program.pid}})

	messages := make(chan programMessage, 16)
	// done ends the helpers: output waiting for room, and messages nobody reads any more.
	done := make(chan struct{})
	send := func(message programMessage) bool {
		select {
		case messages <- message:
			return true
		case <-done:
			return false
		}
	}
	names := [2]string{"stdout", "stderr"}
	var helpers sync.WaitGroup
	for stream, pipe := range [2]*os.File{program.stdout, program.stderr} {
		helpers.Go(func() {
			buffer := make([]byte, 64*1024)
			for {
				count, err := pipe.Read(buffer)
				if count > 0 {
					delivered := send(programMessage{kind: programData, stream: stream, data: append([]byte(nil), buffer[:count]...)})
					// Like a full pipe: a program that writes faster than the link waits.
					s.out.waitForRoom(done)
					if !delivered {
						return
					}
				}
				if err != nil {
					send(programMessage{kind: programEOF, stream: stream})
					return
				}
			}
		})
	}
	helpers.Go(func() {
		// Closing stdin ends the program's input; later input fails with EBADF.
		defer func() { _ = program.stdin.Close() }()
		for {
			chunk, ok := input.next()
			if !ok {
				input.close()
				return
			}
			if _, err := program.stdin.Write(chunk); err != nil {
				input.close()
				return
			}
		}
	})
	helpers.Go(func() {
		code, err := reapProgram(program.cmd)
		if err != nil {
			send(programMessage{kind: programWaitFailed, err: err})
			return
		}
		send(programMessage{kind: programExit, exit: code})
	})

	var (
		exited   bool
		ended    [2]bool
		exitCode int
		waitErr  error
		idle     time.Time
		aborted  bool
		killed   bool
	)
	cancelled, killRequested := ctx.Done(), ctl.kill
	kill := func() {
		if !killed {
			killed = true
			killTree(program.pid)
		}
	}
	var graceTimer waitTimer
	defer graceTimer.stop()
	for !exited || !ended[0] || !ended[1] {
		var grace <-chan time.Time
		if !idle.IsZero() {
			// A descendant may hold the pipes open after the program exited; stop waiting for it.
			remaining := time.Until(idle)
			if remaining <= 0 {
				break
			}
			grace = graceTimer.arm(remaining)
		}
		select {
		case message := <-messages:
			switch message.kind {
			case programData:
				s.out.sendBulk(&Frame{Kind: FrameEvent, ID: id, JSON: Object{"kind": "output", "stream": names[message.stream]}, Payload: message.data}, nil)
				if exited {
					idle = time.Now().Add(processExitGrace)
				}
			case programEOF:
				ended[message.stream] = true
			case programExit:
				exited, exitCode = true, message.exit
				idle = time.Now().Add(processExitGrace)
			case programWaitFailed:
				exited, waitErr = true, message.err
				idle = time.Now()
			}
		case <-cancelled:
			cancelled = nil
			aborted = true
			kill()
		case <-killRequested:
			killRequested = nil
			kill()
		case <-grace:
		}
		graceTimer.stop()
	}
	// Closing the pipes ends readers and the writer, also when a descendant still holds a pipe; removing the program ends a
	// writer waiting for input.
	close(done)
	s.mu.Lock()
	delete(s.processes, id)
	s.mu.Unlock()
	input.close()
	program.closePipes()
	helpers.Wait()
	if aborted {
		return nil, newFailure("aborted", "aborted")
	}
	switch {
	case waitErr != nil:
		return nil, newFailure("spawn_error", waitErr.Error())
	case !exited:
		return nil, newFailure("spawn_error", "process did not report its exit")
	}
	return Object{"exitCode": exitCode}, nil
}
