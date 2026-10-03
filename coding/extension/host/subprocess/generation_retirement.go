package subprocess

import "sync"

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
	}
	h.retirementMu.Unlock()
	if !deferred {
		stop()
	}
	return deferred
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
