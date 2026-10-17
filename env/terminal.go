package env

// Ports packages/env/src/terminal.ts

import (
	"context"
	"errors"
	"maps"
	"sync"
)

// terminalWriteChunk is the most input sent in one frame: larger input goes to the daemon in several writes; frames
// are at most 16 MiB.
const terminalWriteChunk = 1024 * 1024

// TerminalOptions configure OpenTerminal.
type TerminalOptions struct {
	// Cwd is the working directory of the program; it must exist.
	Cwd string
	// Argv is the program and its arguments. Nil is the user's shell (`SHELL` in Env or the daemon's environment) as a
	// login shell.
	Argv []string
	// Env is added to the daemon's environment, or is the whole environment with InheritEnv false. Set `TERM` here.
	Env        map[string]string
	InheritEnv *bool
	Cols, Rows int
	// OnData receives the terminal's raw output, in order, on the connection's reader.
	OnData func(bytes []byte)
}

// TerminalForeground is the foreground job of a terminal: its process group id, and its leader's name if the system
// tells ("" otherwise).
type TerminalForeground struct {
	Pid  int
	Name string
}

// RemoteTerminal is a program running in a pseudo-terminal of a pi-env daemon. It ends when the program exits, Close
// kills it, or the connection is lost (the daemon then kills it).
type RemoteTerminal struct {
	// Pid is the program's process id on the daemon's machine.
	Pid int

	connection *Connection
	id         uint32
	session    int
	abort      context.CancelFunc

	// exited delivers how the terminal ended once; settle latches it so Exited can be called any number of times.
	exited  <-chan TerminalExit
	settle  sync.Once
	settled TerminalExit
}

// TerminalExit is how a terminal ended: its exit code, or the daemon's error, a RemoteError with code `aborted` after
// Close.
type TerminalExit struct {
	ExitCode int
	Err      error
}

// NewRemoteTerminal is the terminal of the running `pty` request id of a daemon session, as upstream's constructor takes
// it. exited delivers how the request settled, once; abort ends the request. OpenTerminal is how a terminal is started.
func NewRemoteTerminal(connection *Connection, id uint32, session, pid int, exited <-chan TerminalExit, abort context.CancelFunc) *RemoteTerminal {
	return &RemoteTerminal{Pid: pid, connection: connection, id: id, session: session, exited: exited, abort: abort}
}

// Exited blocks until the terminal ended and returns the exit code. It is upstream's `exited` promise, which may be awaited
// any number of times. It fails with the daemon's error, with code `aborted` after Close.
func (t *RemoteTerminal) Exited() (int, error) {
	t.settle.Do(func() { t.settled = <-t.exited })
	return t.settled.ExitCode, t.settled.Err
}

// Write sends input, a string (sent as UTF-8) or []byte; once the terminal has ended, input is dropped. Any other value
// sends nothing, as upstream's `bytes.length` loop does for a value that has no length.
func (t *RemoteTerminal) Write(data any) {
	var bytes []byte
	switch typed := data.(type) {
	case string:
		bytes = []byte(typed)
	case []byte:
		bytes = typed
	}
	for offset := 0; offset < len(bytes); offset += terminalWriteChunk {
		t.request("ptyWrite", Json{}, bytes[offset:min(offset+terminalWriteChunk, len(bytes))])
	}
}

// Resize changes the size; the program receives SIGWINCH.
func (t *RemoteTerminal) Resize(cols, rows int) {
	t.request("ptyResize", Json{"cols": cols, "rows": rows}, nil)
}

// Close kills the program and everything in its session.
func (t *RemoteTerminal) Close() { t.abort() }

// Foreground is the job in the terminal's foreground when it is not the program itself: what a shell runs, such as an
// editor. It is nil while the program is in front (a shell at its prompt), and fails once the terminal has ended.
func (t *RemoteTerminal) Foreground(ctx context.Context) (*TerminalForeground, error) {
	reply, err := t.connection.Request(ctx, "ptyForeground", Json{"target": t.id}, RequestOptions{Session: t.session})
	if err != nil {
		return nil, err
	}
	foreground, ok := reply.JSON["foreground"].(Json)
	if !ok {
		return nil, nil
	}
	pid, _ := foreground["pid"].(float64)
	name, _ := foreground["name"].(string)
	//portlint:allow numbers pid is a process group id the daemon read as a C int (daemon/src/pty.rs:unix::foreground)
	return &TerminalForeground{Pid: int(pid), Name: name}, nil
}

// request sends an operation on the terminal without waiting: a terminal that ended or a lost connection is
// reported by Exited. Calls in a row leave in order, so keystrokes never swap.
func (t *RemoteTerminal) request(op string, request Json, payload []byte) {
	body := Json{"target": t.id}
	maps.Copy(body, request)
	//portlint:allow ctxbackground upstream's request() takes no signal for these operations (terminal.ts:#request): they are not cancelled
	t.connection.Start(context.Background(), op, body, RequestOptions{Session: t.session, Payload: payload})
}

// OpenTerminal starts a program in a new pseudo-terminal of the daemon behind connection. It returns once the program
// runs; a failure before that, such as a missing directory, is the error. The terminal outlives ctx: cancelling ctx
// only abandons the open, and kills a program that was starting.
func OpenTerminal(ctx context.Context, connection *Connection, options TerminalOptions) (*RemoteTerminal, error) {
	//portlint:allow ctxbackground the terminal's lifetime is its own AbortController (terminal.ts:openTerminal), which Close aborts
	//portlint:allow ctxmapping the caller's context only abandons the open: openTerminal takes no signal, so the request outlives it
	requestContext, cancel := context.WithCancel(context.Background())
	// started carries the program's process id from the connection's reader.
	started := make(chan int, 1)
	var (
		id      uint32
		session int
	)
	body := Json{"cwd": options.Cwd, "cols": options.Cols, "rows": options.Rows}
	if options.Argv != nil {
		body["argv"] = options.Argv
	}
	if options.Env != nil {
		body["env"] = options.Env
	}
	if options.InheritEnv != nil {
		body["inheritEnv"] = *options.InheritEnv
	}
	call := connection.Start(requestContext, "pty", body, RequestOptions{
		OnStart: func(requestID uint32, requestSession int) { id, session = requestID, requestSession },
		OnEvent: func(event Json, payload []byte) {
			switch event["kind"] {
			case "output":
				if options.OnData != nil {
					options.OnData(payload)
				}
			case "started":
				pid, _ := event["pid"].(float64)
				//portlint:allow numbers pid is the daemon's u32 process id (daemon/src/pty.rs:run)
				program := int(pid)
				select {
				case started <- program:
				default:
				}
			}
		},
	})
	// The channel carries upstream's `exited` promise: it settles with the request, which a lost connection also settles.
	exited := make(chan TerminalExit, 1)
	//portlint:allow golifetime ends when the call settles, which the reply or the loss of the connection causes, as the promise settles
	go func() {
		reply, err := call.Wait()
		exitCode, _ := reply.JSON["exitCode"].(float64)
		//portlint:allow numbers exitCode is the daemon's i64 exit_code (daemon/src/sys/unix.rs:298-304); terminal.ts:107 passes it through
		exited <- TerminalExit{ExitCode: int(exitCode), Err: err}
	}()
	open := func(pid int) *RemoteTerminal { return NewRemoteTerminal(connection, id, session, pid, exited, cancel) }
	// Failing before it started rejects the open; afterwards, Exited carries it.
	select {
	case pid := <-started:
		return open(pid), nil
	case <-call.done:
		select {
		case pid := <-started:
			return open(pid), nil
		default:
		}
		cancel()
		if _, err := call.Wait(); err != nil {
			return nil, err
		}
		return nil, errors.New("The terminal ended before it started")
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
}
