package workers

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/src/node/workers/process.ts. A worker process as the server sees it (slice 10 §3, Chord v1's
// `forkDriver`): started as a detached process group (the whole tree can be killed), stdout and stderr piped (the stderr
// tail kept, both mirrored to the server's own when the app takes no output), its IPC link made at the fork so the child's
// `hello` and early holds are queued, never lost; control messages, the link's close and the exit are told as they happen.
//
// Forced Go differences. A worker is an executable, not a Node module: `Entry` is its path and `ExecArgv` its arguments
// (Node's `execArgv` and entry file). The IPC channel is workers_channel.go's. Events are called from the owned goroutines
// that observe them (the channel's reader, the waiter), in the order each source tells them; a handler enters its own
// serialization. `exited` is a closed channel plus the Exit the handler also receives: the handler runs before anyone
// waiting on `Exited` resumes, as Node runs the `exit` listener before the promise's continuations. A start that fails
// (the executable is missing) leaves a process of pid -1 whose exit {null, null} is told like Node's `error` event; only a
// channel that cannot be made fails the constructor, as Node's `fork` throws synchronously for it.

// ForkOptions is how a worker process is started: the bootstrap options of the workers component.
type ForkOptions struct {
	Entry    string
	ExecArgv []string
	Env      map[string]string
	// OnOutput is its output; without it, mirrored to this process's own.
	OnOutput func(stream string, chunk []byte)
}

// Exit is how a worker process ended: its exit code, or the signal that ended it; the other is nil (Node's null).
type Exit struct {
	Code   *int
	Signal *string
}

// ProcessEvents is what a worker process tells as it happens.
type ProcessEvents struct {
	Control func(message FromWorker)
	// LinkLost: its link was closed by this side (the heartbeat, a protocol error) while the child may live.
	LinkLost func()
	// LinkClosed: its link was closed by the child's side (it exits, or it closed its IPC channel and may live on).
	LinkClosed func()
	Exited     func(exit Exit)
}

// stderrTailUnits is how much of a worker's stderr is kept for its failure: 4 KiB (UTF-16 code units, as the string it
// slices).
const stderrTailUnits = 4096

// WorkerProcess is one started worker process.
type WorkerProcess struct {
	Pid       int
	StartedAt int64
	// Link is the link to the child's host, made at the fork.
	Link link.Link

	channel *channel
	cmd     *exec.Cmd
	tree    *processTree
	done    chan struct{}
	// doneOnce closes done once: a second end (Pi's `exit` after `error`) never panics.
	doneOnce sync.Once
	exit     Exit
	// killed: Kill ended the process (its exit is reported by killedExit).
	killed atomic.Bool

	// pumps are the goroutines reading its output; ended joins them.
	pumps sync.WaitGroup

	mu     sync.Mutex
	stderr string
}

// NewWorkerProcess starts the worker process options describe and tells events as they happen.
func NewWorkerProcess(options ForkOptions, events ProcessEvents) (*WorkerProcess, error) {
	//nolint:gosec // G204 (class b): the program is the app's own worker entry and its arguments, with no shell.
	cmd := exec.Command(options.Entry, options.ExecArgv...)
	// Unique to this start: what the worker's NodeHosts leave of their socket directories is named by it (SocketOwnerEnv).
	owner := plugins.NewSocketOwner()
	cmd.Env = append(environment(options.Env), plugins.SocketOwnerEnv+"="+owner)
	detach(cmd)
	parent, afterStart, fail, err := prepareChannel(cmd)
	if err != nil {
		return nil, err
	}
	stdout, stdoutWrite, err := os.Pipe()
	if err != nil {
		fail()
		return nil, err
	}
	stderr, stderrWrite, err := os.Pipe()
	if err != nil {
		_ = stdout.Close()
		_ = stdoutWrite.Close()
		fail()
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = stdoutWrite, stderrWrite
	process := &WorkerProcess{Pid: -1, StartedAt: time.Now().UnixMilli(), channel: parent, cmd: cmd, done: make(chan struct{})}
	process.Link = newTellingLink(link.IpcLink(parent), events.LinkLost, events.LinkClosed)
	parent.OnMessage(func(message any) {
		if control, ok := controlOf[FromWorker](message); ok && events.Control != nil {
			events.Control(control)
		}
	})
	startErr := cmd.Start()
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()
	afterStart()
	if startErr != nil {
		fail()
		_ = stdout.Close()
		_ = stderr.Close()
		// A fork that failed (no exit follows in Node's terms, so the `error` event ends it).
		//portlint:allow golifetime the task ends once it has told the failed start's exit
		go func() { //portlint:allow asyncorder process.ts:81 child.once("error", ...) is an event of the failed fork with no ordering against other work; the exit handler runs inside it
			parent.finish()
			process.ended(Exit{}, events)
		}()
		return process, nil
	}
	process.Pid = cmd.Process.Pid
	if tree, err := treeOf(cmd); err == nil {
		process.tree = tree
	} else {
		// Without its job the worker's tree cannot be killed: the start fails (its exit tells), what failed is in its tail.
		process.keepStderr([]byte("workers: placing the worker in a job object: " + err.Error() + "\n"))
		_ = cmd.Process.Kill()
	}
	process.pump(stdout, "stdout", options.OnOutput)
	process.pump(stderr, "stderr", options.OnOutput)
	parent.start()
	//portlint:allow golifetime the waiter returns when the leader exits
	go func() { //portlint:allow asyncorder process.ts:79 child.once("exit", ...) is an event of the child with no ordering against other work; the exit handler runs inside it
		state, _ := cmd.Process.Wait()
		// The worker's NodeHost, if it was still starting when the worker ended, left its socket directory behind: Pi evaluates
		// plugins in-process and has no such directory, so there is nothing to port, only to clean up (a kill gives the worker
		// no chance to).
		plugins.RemoveSocketDirsOf(tempDirOf(cmd.Env), owner)
		process.ended(exitOf(state), events)
	}()
	return process, nil
}

// tempDirOf is the temporary directory a process with environment env uses (os.TempDir's, for env instead of this process's).
func tempDirOf(env []string) string {
	for _, entry := range slices.Backward(env) {
		if dir, ok := strings.CutPrefix(entry, "TMPDIR="); ok && dir != "" {
			return dir
		}
	}
	return os.TempDir()
}

// environment is this process's environment overlaid with env.
func environment(env map[string]string) []string {
	merged := os.Environ()
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		merged = append(merged, key+"="+env[key])
	}
	return merged
}

// pump reads one of the child's output streams: the stderr tail is kept, and each chunk goes to onOutput, or to this
// process's own stream. It ends with the stream: when every process holding its write end is gone.
func (p *WorkerProcess) pump(reader *os.File, stream string, onOutput func(stream string, chunk []byte)) {
	//portlint:allow golifetime the reader returns at the stream's end, once every holder of its write end is gone; ended joins it
	p.pumps.Go(func() {
		defer func() { _ = reader.Close() }()
		buffer := make([]byte, 32*1024)
		for {
			count, err := reader.Read(buffer)
			if count > 0 {
				chunk := slices.Clone(buffer[:count])
				if stream == "stderr" {
					p.keepStderr(chunk)
				}
				switch {
				case onOutput != nil:
					onOutput(stream, chunk)
				case stream == "stderr":
					_, _ = os.Stderr.Write(chunk)
				default:
					_, _ = os.Stdout.Write(chunk)
				}
			}
			if err != nil {
				return
			}
		}
	})
}

// keepStderr appends chunk to the tail: the last 4096 UTF-16 code units of what it has written.
func (p *WorkerProcess) keepStderr(chunk []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stderr += string(chunk)
	//portlint:allow utf16units a UTF-16 unit count never exceeds the UTF-8 byte count (at most 1 unit per 1-3 bytes, 2 per 4), so bytes <= 4096 implies units <= 4096; the exact unit count follows below (process.ts:62 `.slice(-STDERR_TAIL)`)
	if len(p.stderr) <= stderrTailUnits {
		return
	}
	units := utf16.Encode([]rune(p.stderr))
	if len(units) > stderrTailUnits {
		p.stderr = string(utf16.Decode(units[len(units)-stderrTailUnits:]))
	}
}

// ended is the process's end, however the leader ended: nothing it spawned outlives it, the handler is told, and then
// those waiting for the exit resume.
func (p *WorkerProcess) ended(exit Exit, events ProcessEvents) {
	if p.killed.Load() {
		exit = killedExit(exit)
	}
	p.tree.kill()
	// Everything it wrote is told before its exit, and nothing of it is left running: its output streams end with the last
	// process holding them (the tree is gone), and its channel with it.
	p.pumps.Wait()
	p.channel.finish()
	if events.Exited != nil {
		events.Exited(exit)
	}
	p.tree.close()
	p.exit = exit
	p.doneOnce.Do(func() { close(p.done) })
}

// Exited is closed when the process ended (every process it spawned is killed then); Exit is its result.
func (p *WorkerProcess) Exited() <-chan struct{} { return p.done }

// Exit is how the process ended; it is valid once Exited is closed.
func (p *WorkerProcess) Exit() Exit { return p.exit }

// StderrTail is the last bytes it wrote to stderr; empty when it wrote none.
func (p *WorkerProcess) StderrTail() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stderr
}

// Send sends a control message; it is dropped when the child is gone (its exit tells).
func (p *WorkerProcess) Send(message ToWorker) {
	if !p.channel.Connected() {
		return
	}
	payload, err := envelope(message)
	if err != nil {
		return
	}
	p.channel.SendControl(payload)
}

// Kill kills the child and every process it spawned; it does nothing once the process ended.
func (p *WorkerProcess) Kill() {
	select {
	case <-p.done:
		return
	default:
	}
	p.killed.Store(true)
	if p.tree.kill() || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Kill()
}

// tellingLink is a link that also tells who closed it (linkLost: this side, linkClosed: the child's), whoever listens to it.
type tellingLink struct {
	inner      link.Link
	linkLost   func()
	linkClosed func()

	mu       sync.Mutex
	listener func()
	closed   bool
}

var _ link.Link = (*tellingLink)(nil)

func newTellingLink(inner link.Link, linkLost, linkClosed func()) *tellingLink {
	l := &tellingLink{inner: inner, linkLost: linkLost, linkClosed: linkClosed}
	inner.OnClose(func() {
		l.mu.Lock()
		here := l.closed
		l.closed = true
		listener := l.listener
		l.mu.Unlock()
		if listener != nil {
			listener()
		}
		if !here && l.linkClosed != nil {
			l.linkClosed()
		}
	})
	return l
}

func (l *tellingLink) Send(message []byte) { l.inner.Send(message) }

func (l *tellingLink) OnMessage(listener func(message []byte)) { l.inner.OnMessage(listener) }

func (l *tellingLink) Close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	l.mu.Unlock()
	if l.linkLost != nil {
		l.linkLost()
	}
	l.inner.Close()
}

func (l *tellingLink) OnClose(listener func()) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		listener()
		return
	}
	l.listener = listener
	l.mu.Unlock()
}
