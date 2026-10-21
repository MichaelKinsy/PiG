package jsloop

import (
	"context"
	"slices"
	"sync"
)

// Turns models when Pi's event loop gets to run the callbacks of timers and immediates (D189). Pi runs the agent on the thread that runs the extensions, so a callback an extension queued with setTimeout or setImmediate cannot run while the agent runs a chain of microtasks; it runs when the chain awaits real work (a model response, a tool's I/O, a dialog, a child process), or when the chain ends and the loop is idle.
//
// A chain is the agent's run, from agent_start to the end of agent_settled. A wait is a stretch of the chain that awaits real work: while one is open the loop turns, so a callback runs at once. A turn is one completion of real work, such as a chunk of a model response. A callback that finds the chain running and no wait open is held, in arrival order, until a turn, a wait, or the end of the chain. Outside a chain a callback runs at once.
//
// The extension host holds the host calls an extension runtime marks as made from a timer or an immediate (CallPayload.Timer), because a separate process cannot wait for the host's loop itself.
//
// A nil *Turns holds nothing: callbacks run at once.
type Turns struct {
	mu        sync.Mutex
	chains    int
	waits     int
	held      []heldCall
	commands  int
	observers []func(deferring bool)
	// appliedWaits wait for the runtimes to apply the changes the observers made.
	appliedWaits []func(ctx context.Context)
	deferring    bool
	notifyMu     sync.Mutex
	ticks        []tickCall
	// ticked holds the tool calls whose tick boundary passed in this chain (Ticked); ToolStarted forgets a call that starts again, and the end of the chain forgets all.
	ticked   map[string]struct{}
	flushing bool
}

// tickCall is a callback that waits for the tick boundary of the tool call that queued it (origin), or of any tool call when origin is "".
type tickCall struct {
	fn     func(done func())
	origin string
}

// heldCall is a callback that waits for a turn, and the owner it is held for (MacrotaskDoneFor). turning marks a callback of a loop that already turns (a late tick, see Tick): it runs behind the callbacks held before it as soon as no flush runs, with no wait or tick boundary to await.
type heldCall struct {
	fn      func(done func())
	owner   any
	turning bool
}

// Chain says the running work is a chain of microtasks, and returns the function that ends it. Callbacks wait while any chain runs and no wait is open.
func (t *Turns) Chain() (end func()) {
	if t == nil {
		return func() {}
	}
	t.mu.Lock()
	t.chains++
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			t.chains--
			if t.chains == 0 {
				t.ticked = nil
			}
			t.flushLocked(false)
		})
	}
}

// Command says the running work is the iteration of Pi's loop that handles one stdin command, and returns the function that ends it. While a command runs and no wait is open, a timer or immediate callback an extension runtime queued must not run, since Pi's loop runs nothing else between the command's microtasks and its response; ObserveDefer tells the runtimes. The calls of such a callback cannot be held, because the runtime may wait for their answers.
func (t *Turns) Command() (end func()) {
	if t == nil {
		return func() {}
	}
	t.mu.Lock()
	t.commands++
	t.syncDeferLocked()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			t.commands--
			t.syncDeferLocked()
		})
	}
}

// ObserveDefer registers fn, which runs with true when the timer callbacks of an extension runtime must wait (a command runs and no wait is open) and with false when they may run. fn runs in the order of the changes. applied, when it is not nil, returns once every runtime applied the changes fn made (Arm waits for it).
func (t *Turns) ObserveDefer(fn func(deferring bool), applied func(ctx context.Context)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.observers = append(t.observers, fn)
	if applied != nil {
		t.appliedWaits = append(t.appliedWaits, applied)
	}
	t.mu.Unlock()
}

// Arm opens the Turns command of a stdin command and returns once every extension runtime applied the hold, so the command's first event reaches no runtime that still runs its timer callbacks: Pi's loop runs nothing between reading the command and the end of its microtasks, and the runtimes are other processes. The caller must not hold the loop that runs the command, since a runtime may be waiting for it. A runtime that ended, or ctx's end, stops the wait. end is the function Command returns.
func (t *Turns) Arm(ctx context.Context) (end func()) {
	end = t.Command()
	if t == nil {
		return end
	}
	t.mu.Lock()
	waits := append([]func(context.Context){}, t.appliedWaits...)
	t.mu.Unlock()
	for _, applied := range waits {
		applied(ctx)
	}
	return end
}

// syncDeferLocked tells the observers when the deferring state changed, and unlocks.
func (t *Turns) syncDeferLocked() {
	now := t.commands > 0 && t.waits == 0
	if now == t.deferring {
		t.mu.Unlock()
		return
	}
	t.deferring = now
	observers := append([]func(bool){}, t.observers...)
	t.mu.Unlock()
	t.notifyMu.Lock()
	defer t.notifyMu.Unlock()
	t.mu.Lock()
	now = t.deferring
	t.mu.Unlock()
	for _, fn := range observers {
		fn(now)
	}
}

// Wait says the chain awaits real work, so the loop turns, and returns the function that ends the wait. Opening a wait releases the held callbacks.
func (t *Turns) Wait() (done func()) {
	if t == nil {
		return func() {}
	}
	t.mu.Lock()
	t.waits++
	t.syncDeferLocked()
	t.mu.Lock()
	t.flushLocked(false)
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			t.waits--
			t.syncDeferLocked()
		})
	}
}

// Turn says one completion of real work ran, which is a turn of the loop: the held callbacks run.
func (t *Turns) Turn() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.flushLocked(true)
}

// Macrotask runs fn, the work of a timer or immediate callback, once the loop would run it: at once when no chain runs or a wait is open, and otherwise at the next turn, wait or end of the chain. Callbacks run in the order they arrived. fn must not block: a producer calls Macrotask from the goroutine that read the call.
func (t *Turns) Macrotask(fn func()) {
	t.MacrotaskDone(func(done func()) { fn(); done() })
}

// MacrotaskDone is Macrotask for work whose effect is written after fn returns: fn calls done once it is. The call that releases the callback waits for done before it returns, so the output of the callback precedes whatever Pi's chain does next.
func (t *Turns) MacrotaskDone(fn func(done func())) { t.MacrotaskDoneFor(nil, fn) }

// MacrotaskDoneFor is MacrotaskDone for a callback held for owner: Release(owner) runs it before the loop turns. An owner is a runtime that cannot say whether one of its handlers waits for the callback's call (a Go, Rust or Python extension), so the handler's own wait is a turn the host cannot see.
func (t *Turns) MacrotaskDoneFor(owner any, fn func(done func())) {
	if t == nil {
		fn(func() {})
		return
	}
	t.mu.Lock()
	if t.flushing || len(t.held)+len(t.ticks) > 0 || (t.chains > 0 && t.waits == 0) {
		// A callback never overtakes one that arrived earlier: when the loop is free it runs behind the held ones.
		t.held = append(t.held, heldCall{fn: fn, owner: owner})
		t.flushLocked(false)
		return
	}
	t.mu.Unlock()
	fn(func() {})
}

// Release runs the callbacks held for owner now, in the order they arrived, and returns once their effect is written. The host calls it when a request starts on the owner's connection: the request's handler may await one of those callbacks' calls, which no turn of the loop would run while the handler is pending.
func (t *Turns) Release(owner any) {
	if t == nil || owner == nil {
		return
	}
	t.mu.Lock()
	var mine []heldCall
	kept := t.held[:0:0]
	for _, call := range t.held {
		if call.owner == owner {
			mine = append(mine, call)
		} else {
			kept = append(kept, call)
		}
	}
	if len(mine) == 0 {
		t.mu.Unlock()
		return
	}
	t.held = kept
	t.mu.Unlock()
	var written sync.WaitGroup
	for _, call := range mine {
		written.Add(1)
		call.fn(written.Done)
	}
	written.Wait()
}

// Tick runs fn, which must call done once its effect is written, as the work of a process.nextTick callback, or of a timer callback that runs while a sibling tool awaits, at the next Ticked of origin: Pi runs such a callback right after the tool_execution_end of the tool that queued it, before the chain goes on. origin is the tool call that queued the callback, "" when the callback was not queued by a tool call, which a Ticked of any tool call releases. It runs at once when no chain runs. A callback that waits for Ticked also runs at the next turn, wait or end of the chain, behind the ticks already held.
//
// A callback whose tool call's boundary already passed runs at once: the runtime that made the call says the loop turns (a sibling tool awaits, or the tool's own tick queue runs), and the callback ran in Pi right after the tool_execution_end that came before the call. The runtime is another process, so the call can reach the host after that event, and the time it takes decides nothing about the call's place. It runs behind the callbacks held before it, which a turn of the loop runs with it, and it does not wait for the boundary of another tool call.
func (t *Turns) Tick(origin string, fn func(done func())) {
	if t == nil {
		fn(func() {})
		return
	}
	t.mu.Lock()
	if _, passed := t.ticked[origin]; origin != "" && passed {
		if t.flushing || len(t.held) > 0 {
			t.held = append(t.held, heldCall{fn: fn, turning: true})
			t.flushLocked(false)
			return
		}
		t.mu.Unlock()
		fn(func() {})
		return
	}
	if t.flushing || len(t.ticks) > 0 || t.chains > 0 {
		t.ticks = append(t.ticks, tickCall{fn: fn, origin: origin})
		t.flushLocked(false)
		return
	}
	t.mu.Unlock()
	fn(func() {})
}

// ToolStarted says the tool call toolCallID starts, so a call that reuses its identifier later in the chain has its own tick boundary ahead.
func (t *Turns) ToolStarted(toolCallID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	delete(t.ticked, toolCallID)
	t.mu.Unlock()
}

// Ticked says the tick boundary of the tool call toolCallID passed, which is the written tool_execution_end of the call: the tick callbacks it queued, and those no tool call queued, run. The held timer callbacks keep waiting for a turn, and so do the ticks of a tool call that has not ended.
func (t *Turns) Ticked(toolCallID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if toolCallID != "" {
		if t.ticked == nil {
			t.ticked = make(map[string]struct{})
		}
		t.ticked[toolCallID] = struct{}{}
	}
	if t.flushing || len(t.ticks) == 0 {
		t.mu.Unlock()
		return
	}
	t.flushing = true
	for {
		var batch []tickCall
		kept := t.ticks[:0:0]
		for _, call := range t.ticks {
			if call.origin == "" || call.origin == toolCallID {
				batch = append(batch, call)
			} else {
				kept = append(kept, call)
			}
		}
		if len(batch) == 0 {
			break
		}
		t.ticks = kept
		t.mu.Unlock()
		var written sync.WaitGroup
		for _, call := range batch {
			written.Add(1)
			call.fn(written.Done)
		}
		// The next step of Pi's chain follows the callbacks' output, so the output is written before Ticked returns.
		written.Wait()
		t.mu.Lock()
	}
	t.flushing = false
	t.flushLocked(false)
}

// Held reports how many callbacks wait for a turn or a tick.
func (t *Turns) Held() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.held) + len(t.ticks)
}

// flushLocked runs the held callbacks in order when the loop may turn (always for a turn), and unlocks. Tick callbacks wait for Ticked while a chain runs, whatever waits are open; timer callbacks wait while a chain runs and no wait is open. One goroutine flushes at a time and callbacks that arrive meanwhile queue behind it, so the order of arrival is the order of running.
func (t *Turns) flushLocked(turn bool) {
	if t.flushing {
		t.mu.Unlock()
		return
	}
	for {
		// A late tick (heldCall.turning) is a turn of the loop: the callbacks held before it run with it.
		turning := slices.ContainsFunc(t.held, func(call heldCall) bool { return call.turning })
		open := turn || turning || t.chains == 0 || t.waits > 0
		ticks := turn || t.chains == 0
		if !ticks && !turning && len(t.ticks) > 0 {
			// A timer callback runs behind the tick callbacks that arrived before it, as Pi's tick queue runs before the next timer.
			open = false
		}
		var runTicks []tickCall
		var runHeld []heldCall
		if ticks {
			runTicks, t.ticks = t.ticks, nil
		}
		if open {
			runHeld, t.held = t.held, nil
		}
		if len(runTicks)+len(runHeld) == 0 {
			t.flushing = false
			t.mu.Unlock()
			return
		}
		t.flushing = true
		t.mu.Unlock()
		var written sync.WaitGroup
		for _, call := range runTicks {
			written.Add(1)
			call.fn(written.Done)
		}
		for _, call := range runHeld {
			written.Add(1)
			call.fn(written.Done)
		}
		written.Wait()
		t.mu.Lock()
	}
}

type turnsKey struct{}

// WithTurns returns ctx carrying turns, which the work of the run observes: a completion of real work calls Turn, and a tool's execution is a wait.
func WithTurns(ctx context.Context, turns *Turns) context.Context {
	if turns == nil {
		return ctx
	}
	return context.WithValue(ctx, turnsKey{}, turns)
}

// TurnsFrom returns the Turns ctx carries, or nil.
func TurnsFrom(ctx context.Context) *Turns {
	turns, _ := ctx.Value(turnsKey{}).(*Turns)
	return turns
}
