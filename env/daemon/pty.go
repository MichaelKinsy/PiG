package daemon

// Ports packages/env/daemon/src/pty.rs
//
// `pty`: a program in a pseudo-terminal, for interactive terminals. Unlike `exec`, output is the terminal's raw bytes,
// and the program reads input: `ptyWrite`, `ptyResize` and `ptyForeground` address the running `pty` request by its
// id.

import (
	"context"
	"os"
	"sync"
	"time"
)

// ptyExitGrace is how much silence ends the collection of output still arriving after the program exited.
const ptyExitGrace = 100 * time.Millisecond

// maxCells is the largest terminal size accepted, in cells.
const maxCells = 4096

type ptyRequest struct {
	argv       []string
	hasArgv    bool
	cwd        string
	env        map[string]string
	inheritEnv bool
	cols, rows uint16
}

func ptySize(request Object, key string) (uint16, *Failure) {
	value, ok := unsignedNumber(request[key])
	if !ok || value < 1 || value > maxCells {
		return 0, newFailure("EINVAL", key+" must be between 1 and 4096")
	}
	return uint16(value), nil
}

func parsePtyRequest(request Object) (*ptyRequest, *Failure) {
	result := &ptyRequest{inheritEnv: true, env: map[string]string{}}
	switch value := request["argv"].(type) {
	case nil:
	case []any:
		for _, item := range value {
			text, isString := item.(string)
			if !isString {
				return nil, newFailure("EINVAL", "argv must be a non-empty array of strings")
			}
			result.argv = append(result.argv, text)
		}
		result.hasArgv = len(result.argv) > 0
		if !result.hasArgv {
			return nil, newFailure("EINVAL", "argv must be a non-empty array of strings")
		}
	default:
		return nil, newFailure("EINVAL", "argv must be a non-empty array of strings")
	}
	cwd, ok := request["cwd"].(string)
	if !ok {
		return nil, newFailure("EINVAL", "pty needs cwd")
	}
	result.cwd = cwd
	if environment, ok := request["env"].(Object); ok {
		for key, value := range environment {
			if text, isString := value.(string); isString {
				result.env[key] = text
			}
		}
	}
	if inherit, ok := request["inheritEnv"].(bool); ok {
		result.inheritEnv = inherit
	}
	var failure *Failure
	if result.cols, failure = ptySize(request, "cols"); failure != nil {
		return nil, failure
	}
	if result.rows, failure = ptySize(request, "rows"); failure != nil {
		return nil, failure
	}
	return result, nil
}

// inputQueue is the unbounded queue of terminal input, written in the order it arrives without holding up other
// requests; it ends with the terminal.
type inputQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	chunks [][]byte
	closed bool
}

func newInputQueue() *inputQueue {
	queue := &inputQueue{}
	queue.cond = sync.NewCond(&queue.mu)
	return queue
}

// push queues input; false once the queue ended.
func (q *inputQueue) push(chunk []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	q.chunks = append(q.chunks, chunk)
	q.cond.Signal()
	return true
}

// next waits for the next input; false once the queue ended.
func (q *inputQueue) next() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.chunks) == 0 && !q.closed {
		q.cond.Wait()
	}
	if q.closed {
		return nil, false
	}
	chunk := q.chunks[0]
	q.chunks = q.chunks[1:]
	return chunk, true
}

func (q *inputQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.chunks = nil
	q.mu.Unlock()
	q.cond.Broadcast()
}

// terminal is what `ptyWrite`, `ptyResize` and `ptyForeground` reach: the terminal of one running `pty` request.
type terminal struct {
	input *inputQueue
	proc  *ptyProcess
}

func (s *server) findTerminal(request Object) (*terminal, *Failure) {
	target, ok := unsignedNumber(request["target"])
	if !ok {
		return nil, newFailure("EINVAL", "missing field target")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	found := s.terminals[uint32(target)]
	if found == nil {
		return nil, newFailure("EBADF", "no running terminal with this id")
	}
	return found, nil
}

// ptyWrite queues input for the terminal.
func (s *server) ptyWrite(request Object, payload []byte) (Object, *Failure) {
	found, failure := s.findTerminal(request)
	if failure != nil {
		return nil, failure
	}
	if !found.input.push(payload) {
		return nil, newFailure("EBADF", "the terminal has ended")
	}
	return Object{}, nil
}

// ptyResize changes the terminal's size; the program receives SIGWINCH.
func (s *server) ptyResize(request Object) (Object, *Failure) {
	found, failure := s.findTerminal(request)
	if failure != nil {
		return nil, failure
	}
	cols, failure := ptySize(request, "cols")
	if failure != nil {
		return nil, failure
	}
	rows, failure := ptySize(request, "rows")
	if failure != nil {
		return nil, failure
	}
	if err := found.proc.setSize(cols, rows); err != nil {
		return nil, ioFailure(err, "ioctl", "pty")
	}
	return Object{}, nil
}

// ptyForeground is the process group in the terminal's foreground when it is not the program's own: a job a shell
// started, such as an editor. `{ foreground: null }` while the program itself is in front (a shell at its prompt).
func (s *server) ptyForeground(request Object) (Object, *Failure) {
	found, failure := s.findTerminal(request)
	if failure != nil {
		return nil, failure
	}
	group, err := found.proc.foreground()
	if err != nil {
		return nil, ioFailure(err, "tcgetpgrp", "pty")
	}
	if group <= 0 || group == found.proc.pid {
		return Object{"foreground": nil}, nil
	}
	var name any
	if text, ok := processName(group); ok {
		name = text
	}
	return Object{"foreground": Object{"pid": group, "name": name}}, nil
}

type ptyMessageKind int

const (
	ptyData ptyMessageKind = iota
	ptyEOF
	ptyExit
	ptyWaitFailed
)

type ptyMessage struct {
	kind ptyMessageKind
	data []byte
	exit int
	err  error
}

// runPty runs the program in a new terminal, sending a `started` event and then its output, until it exits.
func (s *server) runPty(id uint32, request *ptyRequest, ctl *control) (Object, *Failure) {
	if !ptySupported {
		return nil, newFailure("ENOTSUP", "Terminals are not supported on this system yet")
	}
	//portlint:allow ctxbackground a request ends through the client's cancel frame, which control.attach connects (pty.rs:run, exec.rs Control)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctl.attach(cancel)
	if ctl.aborted.Load() {
		return nil, newFailure("aborted", "aborted")
	}
	if !pathExists(request.cwd) {
		return nil, newFailure("spawn_error", "Working directory does not exist: "+request.cwd)
	}
	proc, failure := startPty(request)
	if failure != nil {
		return nil, failure
	}
	input := newInputQueue()
	found := &terminal{input: input, proc: proc}
	s.mu.Lock()
	s.terminals[id] = found
	s.mu.Unlock()
	s.out.sendControl(&Frame{Kind: FrameEvent, ID: id, JSON: Object{"kind": "started", "pid": proc.pid}})

	messages := make(chan ptyMessage, 16)
	// done ends the helpers: output waiting for room, and messages nobody reads any more.
	done := make(chan struct{})
	send := func(message ptyMessage) bool {
		select {
		case messages <- message:
			return true
		case <-done:
			return false
		}
	}
	var helpers sync.WaitGroup
	helpers.Go(func() {
		proc.read(done, func(bytes []byte) bool {
			delivered := send(ptyMessage{kind: ptyData, data: bytes})
			// Like a full pipe: a program that writes faster than the link waits.
			s.out.waitForRoom(done)
			return delivered
		})
		send(ptyMessage{kind: ptyEOF})
	})
	helpers.Go(func() {
		for {
			chunk, ok := input.next()
			if !ok || !proc.writeAll(chunk, done) {
				input.close()
				return
			}
		}
	})
	helpers.Go(func() {
		code, err := proc.wait()
		if err != nil {
			send(ptyMessage{kind: ptyWaitFailed, err: err})
			return
		}
		send(ptyMessage{kind: ptyExit, exit: code})
	})

	var (
		exited, ended bool
		exitCode      int
		waitErr       error
		idleUntil     time.Time
		aborted       bool
	)
	cancelled, killRequested := ctx.Done(), ctl.kill
	var killed bool
	kill := func() {
		if !killed {
			killed = true
			proc.killGroup()
		}
	}
	var graceTimer waitTimer
	defer graceTimer.stop()
	for !exited || !ended {
		var grace <-chan time.Time
		if !idleUntil.IsZero() {
			// A background job may keep the terminal open after the program exited.
			remaining := time.Until(idleUntil)
			if remaining <= 0 {
				break
			}
			grace = graceTimer.arm(remaining)
		}
		select {
		case message := <-messages:
			switch message.kind {
			case ptyData:
				s.out.sendBulk(&Frame{Kind: FrameEvent, ID: id, JSON: Object{"kind": "output"}, Payload: message.data}, nil)
				if exited {
					idleUntil = time.Now().Add(ptyExitGrace)
				}
			case ptyEOF:
				ended = true
			case ptyExit:
				exited, exitCode = true, message.exit
				idleUntil = time.Now().Add(ptyExitGrace)
			case ptyWaitFailed:
				exited, waitErr = true, message.err
				idleUntil = time.Now()
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
	close(done)
	// Ending the input queue ends the writer; closing the terminal ends the reader.
	s.mu.Lock()
	delete(s.terminals, id)
	s.mu.Unlock()
	input.close()
	proc.close()
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

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
