package env

// Ports packages/env/src/process.ts

import (
	"context"
	"errors"
	"slices"
	"sync"
)

// processWriteChunk is the most input sent in one frame: larger input goes to the daemon in several writes; frames are at
// most 16 MiB.
const processWriteChunk = 1024 * 1024

// ProcessOptions configure OpenProcess.
type ProcessOptions struct {
	// Cwd is the working directory of the program; it must exist.
	Cwd string
	// Argv is the program and its arguments, run directly without a shell.
	Argv []string
	// Env is added to the daemon's environment, or is the whole environment with InheritEnv false.
	Env        map[string]string
	InheritEnv *bool
	// OnStdout receives the program's stdout, raw bytes in order, on the connection's reader.
	OnStdout func(bytes []byte)
	// OnStderr receives the program's stderr, raw bytes in order, on the connection's reader.
	OnStderr func(bytes []byte)
}

// RemoteProcess is a program running with plain pipes in a pi-env daemon, for programs that speak a protocol on stdin and
// stdout. It ends when the program exits, Close kills it, or the connection is lost (the daemon then kills it). When the
// program exits, the daemon kills whatever it started in its process group, so a wrapper such as `npx` that exits leaves
// nothing running.
type RemoteProcess struct {
	// Pid is the program's process id on the daemon's machine.
	Pid int

	connection *Connection
	id         uint32
	session    int
	abort      context.CancelFunc

	// exited delivers how the program ended once; settle latches it so Exited can be called any number of times.
	exited  <-chan ProcessExit
	settle  sync.Once
	settled ProcessExit
}

// ProcessExit is how a program ended: its exit code, or the daemon's error, a RemoteError with code `aborted` after Close.
type ProcessExit struct {
	ExitCode int
	Err      error
}

// NewRemoteProcess is the program of the running `process` request id of a daemon session, as upstream's constructor takes
// it. exited delivers how the request settled, once; abort ends the request. OpenProcess is how a program is started.
func NewRemoteProcess(connection *Connection, id uint32, session, pid int, exited <-chan ProcessExit, abort context.CancelFunc) *RemoteProcess {
	return &RemoteProcess{Pid: pid, connection: connection, id: id, session: session, exited: exited, abort: abort}
}

// Exited blocks until the program ended and returns the exit code. It is upstream's `exited` promise, which may be awaited
// any number of times. It fails with the daemon's error, with code `aborted` after Close.
func (p *RemoteProcess) Exited() (int, error) {
	p.settle.Do(func() { p.settled = <-p.exited })
	return p.settled.ExitCode, p.settled.Err
}

// Write sends input to the program's stdin, a string (sent as UTF-8) or []byte, in order; once stdin is closed or the
// program has ended, input is dropped. Any other value sends nothing, as upstream's `bytes.length` loop does for a value
// that has no length.
func (p *RemoteProcess) Write(data any) {
	var bytes []byte
	switch typed := data.(type) {
	case string:
		bytes = []byte(typed)
	case []byte:
		bytes = typed
	}
	for offset := 0; offset < len(bytes); offset += processWriteChunk {
		p.request("processWrite", bytes[offset:min(offset+processWriteChunk, len(bytes))])
	}
}

// CloseStdin closes the program's stdin after the input written before it.
func (p *RemoteProcess) CloseStdin() { p.request("processCloseStdin", nil) }

// Close kills the program and its process group.
func (p *RemoteProcess) Close() { p.abort() }

// request sends an operation on the program without waiting: stdin already closed, a program that ended or a lost
// connection is reported by Exited. Calls in a row leave in order, so input never swaps.
func (p *RemoteProcess) request(op string, payload []byte) {
	//portlint:allow ctxbackground upstream's request() takes no signal for these operations (process.ts:#request): they are not cancelled
	p.connection.Start(context.Background(), op, Json{"target": p.id}, RequestOptions{Session: p.session, Payload: payload})
}

// OpenProcess starts a program with plain pipes in the daemon behind connection. It returns once the program runs; a
// failure before that, such as a missing directory, is the error, with code `not_supported` when the daemon was released
// before the `process` operation. The program outlives ctx: cancelling ctx only abandons the open, and kills a program that
// was starting.
func OpenProcess(ctx context.Context, connection *Connection, options ProcessOptions) (*RemoteProcess, error) {
	info, err := connection.Info(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(info.Features, "process") {
		return nil, NewRemoteError(Json{"code": "not_supported", "message": "The pi-env daemon cannot run processes; update it"})
	}
	//portlint:allow ctxbackground the program's lifetime is its own AbortController (process.ts:openProcess), which Close aborts
	//portlint:allow ctxmapping the caller's context only abandons the open: openProcess takes no signal, so the request outlives it
	requestContext, cancel := context.WithCancel(context.Background())
	// started carries the program's process id from the connection's reader.
	started := make(chan int, 1)
	var (
		id      uint32
		session int
	)
	body := Json{"cwd": options.Cwd, "argv": options.Argv}
	if options.Env != nil {
		body["env"] = options.Env
	}
	if options.InheritEnv != nil {
		body["inheritEnv"] = *options.InheritEnv
	}
	call := connection.Start(requestContext, "process", body, RequestOptions{
		OnStart: func(requestID uint32, requestSession int) { id, session = requestID, requestSession },
		OnEvent: func(event Json, payload []byte) {
			switch event["kind"] {
			case "output":
				if event["stream"] == "stderr" {
					if options.OnStderr != nil {
						options.OnStderr(payload)
					}
				} else if options.OnStdout != nil {
					options.OnStdout(payload)
				}
			case "started":
				pid, _ := event["pid"].(float64)
				//portlint:allow numbers pid is the daemon's u32 process id (daemon/src/process.rs:run)
				program := int(pid)
				select {
				case started <- program:
				default:
				}
			}
		},
	})
	// The channel carries upstream's `exited` promise: it settles with the request, which a lost connection also settles.
	exited := make(chan ProcessExit, 1)
	//portlint:allow golifetime ends when the call settles, which the reply or the loss of the connection causes, as the promise settles
	go func() {
		reply, err := call.Wait()
		exitCode, _ := reply.JSON["exitCode"].(float64)
		//portlint:allow numbers exitCode is the daemon's i64 exit_code (daemon/src/sys/unix.rs); process.ts passes it through
		exited <- ProcessExit{ExitCode: int(exitCode), Err: err}
	}()
	open := func(pid int) *RemoteProcess { return NewRemoteProcess(connection, id, session, pid, exited, cancel) }
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
		return nil, errors.New("The process ended before it started")
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
}
