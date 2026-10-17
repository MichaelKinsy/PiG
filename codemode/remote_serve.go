package codemode

import (
	"context"
	"sync"
)

// ServeOptions configures ServeRemote.
//
// Ports packages/codemode/src/runtime/remote.ts (CodemodeServeOptions).
type ServeOptions struct {
	// MaxInterruptBudget is the upper bound for a request's interrupt budget, and the budget when a request names
	// none. Zero means the default.
	MaxInterruptBudget int
	// MaxMemoryLimitBytes is the upper bound for a request's VM memory, and the limit when a request names none. Zero
	// means no bound.
	MaxMemoryLimitBytes uint32
	// Wasm supplies the QuickJS wasm module. Nil means the embedded module (upstream: the `wasm` option).
	Wasm func() ([]byte, error)
	// CacheDir holds wazero's on-disk compilation cache; see SandboxOptions.
	CacheDir string
}

// serveBudget is the interrupt budget of one request: what it asks for, capped at what the remote allows.
func serveBudget(request RemoteRequest, options ServeOptions) int {
	maxBudget := options.MaxInterruptBudget
	if maxBudget == 0 {
		maxBudget = DefaultInterruptBudget
	}
	if request.InterruptBudget == 0 {
		return maxBudget
	}
	return min(request.InterruptBudget, maxBudget)
}

// serveMemoryLimit is the VM memory limit of one request: what it asks for, capped at what the remote allows. Zero
// means none.
func serveMemoryLimit(request RemoteRequest, options ServeOptions) uint32 {
	requested, maxMemory := request.Data.MemoryLimitBytes, options.MaxMemoryLimitBytes
	switch {
	case requested == 0:
		return maxMemory
	case maxMemory == 0:
		return requested
	}
	return min(requested, maxMemory)
}

// serveLoop is the job queue of one served execution. Everything the server does runs from it on the goroutine that
// called ServeRemote, in the order the jobs were queued, as upstream's single event loop does. Callbacks from other
// goroutines (the VM's messages, the client's replies) only queue a job.
type serveLoop struct {
	mu     sync.Mutex
	jobs   []func()
	wake   chan struct{}
	closed bool
}

func newServeLoop() *serveLoop { return &serveLoop{wake: make(chan struct{}, 1)} }

func (l *serveLoop) queue(job func()) {
	l.mu.Lock()
	if !l.closed {
		l.jobs = append(l.jobs, job)
	}
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *serveLoop) next() func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.jobs) == 0 {
		return nil
	}
	job := l.jobs[0]
	l.jobs[0] = nil
	l.jobs = l.jobs[1:]
	return job
}

// remoteServer is the state of one served execution; only the loop's goroutine touches it.
type remoteServer struct {
	ctx      context.Context
	exchange RemoteExchange
	loop     *serveLoop
	channel  vmChannel
	stopOnce sync.Once
	// ended: the execution's outcome is decided and nothing more is reported. resolved: serveRemote may return.
	ended    bool
	resolved bool
	buffered []RemoteMessage
	chars    int
	// keepOutput, when set, receives each output message instead of buffered: the host that runs this server (the Worker module)
	// holds the items itself, so a trap of the module cannot lose them. hosted counts the items it holds.
	keepOutput func(WorkerToHostMessage)
	hosted     int
}

// messageHostedOutput stands, in a batch, for the hosted output items taken at that point; its ID is their number.
const messageHostedOutput = "hosted-output"

// startRemoteServer launches the VM of one served execution and starts delivering its messages. The caller runs the loop's jobs
// until the server resolves: serveRemote on the calling goroutine, the Worker's step driver after each event.
func startRemoteServer(ctx context.Context, request RemoteRequest, exchange RemoteExchange, options ServeOptions, launch func(budget int) vmLauncher) *remoteServer {
	s := &remoteServer{ctx: ctx, exchange: exchange, loop: newServeLoop()}
	data := request.Data
	// The prelude parses both lists as JSON arrays; a request that leaves one out has none.
	if data.Tools == nil {
		data.Tools = []VmStartTool{}
	}
	if data.Globals == nil {
		data.Globals = []VmStartGlobal{}
	}
	data.MemoryLimitBytes = serveMemoryLimit(request, options)

	channel, err := launch(serveBudget(request, options))(ctx, data, vmEvents{
		message: func(message vmMessage) { s.loop.queue(func() { s.onMessage(message) }) },
		failure: func(failure *Error) { s.loop.queue(func() { s.end(failureMessage(failure)) }) },
	})
	if err != nil {
		s.end(failureMessage(&Error{Kind: ErrorSandbox, Message: err.Error()}))
	} else {
		s.channel = channel
		channel.start()
	}
	return s
}

func failureMessage(failure *Error) *RemoteMessage {
	return &RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageFailure}, Failure: failure}
}

// stopChannel stops the VM once, whichever of the loop and a context that ended gets there first.
func (s *remoteServer) stopChannel() {
	if s.channel != nil {
		s.stopOnce.Do(s.channel.stop)
	}
}

func (s *remoteServer) takeBuffer() []RemoteMessage {
	batch := s.buffered
	s.buffered = nil
	if s.hosted > 0 {
		batch = []RemoteMessage{{WorkerToHostMessage: WorkerToHostMessage{Type: messageHostedOutput, ID: s.hosted}}}
		s.hosted = 0
	}
	s.chars = 0
	return batch
}

// resolve stops the VM and lets serveRemote return.
func (s *remoteServer) resolve() {
	s.stopChannel()
	s.resolved = true
}

// end decides the outcome: it delivers the terminal message (when there is one) together with the output buffered
// before it, then stops the VM. Whether the client received the message is not the server's concern: the reply is
// ignored and a rejected exchange is the same as a null reply.
//
// Ports packages/codemode/src/runtime/remote.ts (serveCodemodeRemote: end).
func (s *remoteServer) end(terminal *RemoteMessage) {
	if s.ended {
		return
	}
	s.ended = true
	batch := s.takeBuffer()
	if terminal == nil {
		s.resolve()
		return
	}
	s.exchange(s.ctx, append(batch, *terminal), func(*RemoteReply, error) { s.loop.queue(s.resolve) })
}

// reply handles the client's answer to an exchange.
//
// Ports packages/codemode/src/runtime/remote.ts (serveCodemodeRemote: reply).
func (s *remoteServer) reply(reply *RemoteReply) {
	if s.ended || reply == nil {
		return
	}
	if reply.Type == MessageStop {
		s.end(nil)
		return
	}
	s.channel.post(replyFromWire(reply.HostToWorkerMessage))
}

// send delivers a batch and handles its reply on the loop; a rejected exchange ends the execution.
func (s *remoteServer) send(batch []RemoteMessage) {
	s.exchange(s.ctx, batch, func(reply *RemoteReply, err error) {
		s.loop.queue(func() {
			if err != nil {
				s.end(nil)
				return
			}
			s.reply(reply)
		})
	})
}

// onMessage buffers the VM's output, and sends the buffer with each call or the terminal message.
//
// Ports packages/codemode/src/runtime/remote.ts (serveCodemodeRemote: onMessage).
func (s *remoteServer) onMessage(message vmMessage) {
	if s.ended {
		return
	}
	wire := messageToWire(message)
	switch message.kind {
	case vmMessageOutput:
		if s.keepOutput != nil {
			s.keepOutput(wire)
			s.hosted++
		} else {
			s.buffered = append(s.buffered, RemoteMessage{WorkerToHostMessage: wire})
		}
		s.chars += outputChars(wire.Item)
		if len(s.buffered)+s.hosted >= flushItems || s.chars >= flushChars {
			s.send(s.takeBuffer())
		}
	case vmMessageStarted:
		// Buffered in order with the output, which the host may hold.
		if s.keepOutput != nil {
			s.keepOutput(wire)
			s.hosted++
		} else {
			s.buffered = append(s.buffered, RemoteMessage{WorkerToHostMessage: wire})
		}
	case vmMessageCall, vmMessageIdle:
		// Sent at once: a host replaying a script waits for an idle to see that the script issued no further call.
		s.send(append(s.takeBuffer(), RemoteMessage{WorkerToHostMessage: wire}))
	default:
		s.end(&RemoteMessage{WorkerToHostMessage: wire})
	}
}
