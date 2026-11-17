package subprocess

import (
	"encoding/json"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// commandGate counts the command requests running on one extension generation and runs a callback when the last of them returns.
type commandGate struct {
	mu      sync.Mutex
	running int
	idle    []func()
}

func (g *commandGate) begin() {
	g.mu.Lock()
	g.running++
	g.mu.Unlock()
}

func (g *commandGate) end() {
	g.mu.Lock()
	g.running--
	var callbacks []func()
	if g.running == 0 {
		callbacks, g.idle = g.idle, nil
	}
	g.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

// afterIdle runs fn when no command is running and reports true, or reports false at once when the generation is idle and the caller acts itself.
func (g *commandGate) afterIdle(fn func()) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.running == 0 {
		return false
	}
	g.idle = append(g.idle, fn)
	return true
}

// busy reports whether a command request is running on the generation.
func (g *commandGate) busy() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.running > 0
}

// generationRetirement is the stop of a replaced generation that a running command deferred. Whichever of the command's return and Shutdown comes first runs it, once.
type generationRetirement struct {
	once sync.Once
	run  func()
}

func (r *generationRetirement) do() { r.once.Do(r.run) }

// retireGeneration stops a replaced or removed generation. A generation with a command still running keeps running until that command returns: Pi's old extension instance outlives the reload its command awaits (agent-session.ts:3575-3625, one process), so `await ctx.reload()` resolves in it. The stop then runs as a Host task, and Shutdown runs any still pending. Once Shutdown has collected the pending stops, nothing would run a new one, so the generation stops at once. It reports whether the stop was deferred; the caller reaps an immediate stop itself.
func (h *Host) retireGeneration(old *managedExt, stop func()) (deferred bool) {
	retirement := &generationRetirement{run: func() {
		stop()
		old.reap()
	}}
	// The gate takes the callback and the stop becomes visible to Shutdown under one hold of retirementMu, so an immediate stop is never also collected by Shutdown, and a command that returns at once waits here before it starts the task.
	h.retirementMu.Lock()
	if !h.retirementsClosed {
		deferred = old.commands.afterIdle(func() { h.goRetirement(retirement) })
	}
	if deferred {
		if h.retirements == nil {
			h.retirements = make(map[*generationRetirement]struct{})
		}
		h.retirements[retirement] = struct{}{}
		// The reload invalidated the old runner (agent-session.ts:3660-3664), so the running command's captured pi and ctx throw Pi's stale message (runner.ts:721-735) once `await ctx.reload()` resolves, members it answers locally included. The notify is queued before the reply to that reload and, under retirementMu, before the deferred stop's shutdown frame. An immediate stop needs none: the runtime ends with its connection.
		invalidateGeneration(old)
	}
	h.retirementMu.Unlock()
	if !deferred {
		stop()
		return false
	}
	// Pi's reload removes the extension overlays before it reloads and leaves what awaits them pending (interactive-mode.ts:2592-2598 resetExtensionUI, from handleReloadCommand 6628); the
	// generation's process lives on, so its overlays go now, and the ui.custom call of the command that awaits one stays pending, as in Pi, until its component ends it or the stop does.
	if conn := old.connection(); h.uiBridge != nil && conn != nil {
		h.uiBridge.RetireExtensionConn(old.config.Name, conn)
	}
	return true
}

// holdProcessForRunningCommand keeps the process of a packed cell until the retirement of me, a generation with a command still running, stops it. The members of
// one cell share one process, and a reload stops every member it replaces or removes: the stop of a member with no command running, a removed one or one replaced
// first, would end the process under the command that awaits the reload, though Pi's old instance outlives that reload. commitStaged takes the hold before any
// member of the cell stops, when no new command can start on the generation, so a generation busy now is busy or idle when its own retirement looks, and then
// stops at once and gives the hold back (stopManagedSkippingProviders).
func (me *managedExt) holdProcessForRunningCommand() {
	if me.packedProcess == nil || !me.commands.busy() {
		return
	}
	if me.lingerHold.CompareAndSwap(false, true) && !me.packedProcess.holdForLinger() {
		me.lingerHold.Store(false)
	}
}

// goRetirement runs a deferred stop as a task Shutdown joins. Once Shutdown has run the pending stops, nothing is left to do.
func (h *Host) goRetirement(retirement *generationRetirement) {
	h.retirementMu.Lock()
	defer h.retirementMu.Unlock()
	if h.retirementsClosed {
		return
	}
	h.retirementTasks.Go(func() {
		retirement.do()
		h.retirementMu.Lock()
		delete(h.retirements, retirement)
		h.retirementMu.Unlock()
	})
}

// finishRetirements runs every deferred stop that has not run and waits for the running ones. Shutdown calls it before it stops the current generations.
func (h *Host) finishRetirements() {
	h.retirementMu.Lock()
	h.retirementsClosed = true
	pending := make([]*generationRetirement, 0, len(h.retirements))
	for retirement := range h.retirements {
		pending = append(pending, retirement)
	}
	h.retirementMu.Unlock()
	for _, retirement := range pending {
		retirement.do()
	}
	h.retirementTasks.Wait()
}

// invalidateGeneration sends Pi's stale message to a replaced generation's connection.
func invalidateGeneration(old *managedExt) {
	conn := old.connection()
	if conn == nil {
		return
	}
	args, err := json.Marshal(InvalidateArgs{Message: (&inproc.StaleError{}).Error()})
	if err != nil {
		return
	}
	_ = conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyInvalidate, Args: args}})
}
