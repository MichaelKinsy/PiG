// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts
package cli

import "sync"

// rpcShutdownGate is rpc-mode.ts shutdownRequested, inputReady, activeCommands and checkShutdownRequested().
//
// Async contract (Pi 1708b4788): ctx.shutdown() sets the request and schedules the check with setImmediate, so the requesting handler finishes first; nobody waits for the check and it cannot fail. The check re-reads its conditions when it runs: a request is pending, input is attached, no command is being handled and the session is idle. Each of those ending runs it again: a command's completion (for a prompt, when it settles), agent_settled and the input's attach. A shutdown that already started is left alone. Cancellation is the mode's: a started shutdown, a stopped mode or a closed executor drops the check.
//
// The check runs as a macrotask of the stdin executor (rpcResponseTurn), which is Pi's check phase: after the microtasks of the stdin iteration that is running. Every caller hands the scheduling to an owned task and returns at once, so that an extension's synchronous host call (a Go, Python, Rust or Lisp ctx.shutdown()) is answered before the check can run on its goroutine and wait for the session_shutdown handlers: Pi's setImmediate is a deferral nobody waits for.
type rpcShutdownGate struct {
	mu         sync.Mutex
	requested  bool
	inputReady bool
	active     int
	// spawn runs fn on an owned task; schedule runs fn on the executor's next tick; idle is session.isIdle; started reports that shutdown() began; fire starts it.
	spawn    func(fn func())
	schedule func(fn func())
	idle     func() bool
	started  func() bool
	fire     func()
}

func newRPCShutdownGate() *rpcShutdownGate { return &rpcShutdownGate{} }

// bind attaches the executor and the session views. A request made before it waits for InputReady, which follows it.
func (g *rpcShutdownGate) bind(spawn, schedule func(func()), idle, started func() bool, fire func()) {
	g.mu.Lock()
	g.spawn, g.schedule, g.idle, g.started, g.fire = spawn, schedule, idle, started, fire
	g.mu.Unlock()
}

// Request is the extension's ctx.shutdown() (rpc-mode.ts shutdownHandler).
func (g *rpcShutdownGate) Request() {
	g.mu.Lock()
	g.requested = true
	g.mu.Unlock()
	g.Check()
}

// InputReady marks the input reader attached and checks a request made during startup.
func (g *rpcShutdownGate) InputReady() {
	g.mu.Lock()
	g.inputReady = true
	g.mu.Unlock()
	g.Check()
}

// Begin counts a command being handled; End uncounts it and checks. A nil gate counts nothing.
func (g *rpcShutdownGate) Begin() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.active++
	g.mu.Unlock()
}

func (g *rpcShutdownGate) End() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.active--
	g.mu.Unlock()
	g.Check()
}

// Check schedules checkShutdownRequested() on the next tick, from an owned task. A nil gate, or one not bound yet, schedules nothing.
func (g *rpcShutdownGate) Check() {
	if g == nil {
		return
	}
	// spawn does not block, so it runs under the lock: after unbind no task starts.
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.spawn != nil {
		schedule := g.schedule
		g.spawn(func() { schedule(g.check) })
	}
}

// unbind stops new checks from starting, for the end of the mode, before the tasks are joined.
func (g *rpcShutdownGate) unbind() {
	g.mu.Lock()
	g.spawn = nil
	g.mu.Unlock()
}

func (g *rpcShutdownGate) check() {
	g.mu.Lock()
	ready := g.requested && g.inputReady && g.active == 0
	idle, started, fire := g.idle, g.started, g.fire
	g.mu.Unlock()
	if !ready || started() || !idle() {
		return
	}
	fire()
}
