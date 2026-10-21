package jsloop

import (
	"context"
	"sync"
)

// Turns models when Pi's event loop gets to run the callbacks of timers and immediates (D5). Pi runs the agent on the thread that runs the extensions, so a callback an extension queued with setTimeout or setImmediate cannot run while the agent runs a chain of microtasks; it runs when the chain awaits real work (a model response, a tool's I/O, a dialog, a child process), or when the chain ends and the loop is idle.
//
// A chain is the agent's run, from agent_start to the end of agent_settled. A wait is a stretch of the chain that awaits real work: while one is open the loop turns, so a callback runs at once. A turn is one completion of real work, such as a chunk of a model response. A callback that finds the chain running and no wait open is held, in arrival order, until a turn, a wait, or the end of the chain. Outside a chain a callback runs at once.
//
// The extension host holds the host calls an extension runtime marks as made from a timer or an immediate (CallPayload.Timer), because a separate process cannot wait for the host's loop itself.
//
// A nil *Turns holds nothing: callbacks run at once.
type Turns struct {
	mu       sync.Mutex
	chains   int
	waits    int
	held     []func()
	flushing bool
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
			t.flushLocked(false)
		})
	}
}

// Wait says the chain awaits real work, so the loop turns, and returns the function that ends the wait. Opening a wait releases the held callbacks.
func (t *Turns) Wait() (done func()) {
	if t == nil {
		return func() {}
	}
	t.mu.Lock()
	t.waits++
	t.flushLocked(false)
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			t.waits--
			t.mu.Unlock()
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
	if t == nil {
		fn()
		return
	}
	t.mu.Lock()
	if t.flushing || len(t.held) > 0 || (t.chains > 0 && t.waits == 0) {
		// A callback never overtakes one that arrived earlier: when the loop is free it runs behind the held ones.
		t.held = append(t.held, fn)
		t.flushLocked(false)
		return
	}
	t.mu.Unlock()
	fn()
}

// Held reports how many callbacks wait for a turn.
func (t *Turns) Held() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.held)
}

// flushLocked runs the held callbacks in order when the loop may turn (always for a turn), and unlocks. One goroutine flushes at a time and callbacks that arrive meanwhile queue behind it, so the order of arrival is the order of running.
func (t *Turns) flushLocked(turn bool) {
	if t.flushing || len(t.held) == 0 || (!turn && t.chains > 0 && t.waits == 0) {
		t.mu.Unlock()
		return
	}
	t.flushing = true
	for len(t.held) > 0 {
		batch := t.held
		t.held = nil
		t.mu.Unlock()
		for _, fn := range batch {
			fn()
		}
		t.mu.Lock()
	}
	t.flushing = false
	t.mu.Unlock()
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
