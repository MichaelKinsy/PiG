package subprocess

import "sync"

// statePushQueue coalesces the pushes of host state changes to one extension: at most one task pushes to it at a time, and every change that arrives while a push runs makes the task push once more, so a runtime that is slow to read delays only its own pushes and never queues more than one behind the running one.
type statePushQueue struct {
	mu      sync.Mutex
	dirty   bool
	running bool
}

// hostStateChanged brings every runtime up to date with host state that changed without an extension call or an event to the extension (UIBridge.HostStateChanged): the model, the thinking level, the session name, the settings, the MCP server list and the theme palette.
// Upstream's context getters read the live Session, the runtime's MCP registry and the global theme at every call (runner.ts:906, loader.ts:411-414 and 495-497, interactive-mode.ts:2665), while a runtime replicates the state, and a state push otherwise precedes only a request, a setModel or appendEntry call, and a status change. A timer or thread that reads between requests therefore saw the state the last request left.
// The owner reports the change from the goroutine that made it, which can be the UI loop, and the push waits for every runtime's connection. So the caller does one atomic swap and queues a fan-out on a task Shutdown joins; the fan-out queues each extension's push on a task of its own, and pushStateTo sends nothing to a runtime whose state is unchanged.
func (h *Host) hostStateChanged() {
	if h.uiBridge == nil || h.hostStatePushQueued.Swap(true) {
		return
	}
	h.goDrainTask(h.fanOutStatePush)
}

// fanOutStatePush queues a state push to every connected extension. It clears the queued flag before it reads the targets, so a change that arrives while it runs queues another fan-out that pushes the newer state.
func (h *Host) fanOutStatePush() {
	h.hostStatePushQueued.Store(false)
	h.mu.Lock()
	targets := make([]*managedExt, 0, len(h.exts))
	for _, me := range h.exts {
		if me.connection() != nil {
			targets = append(targets, me)
		}
	}
	h.mu.Unlock()
	for _, me := range targets {
		h.queueStatePush(me)
	}
}

// queueStatePush asks for a push of the host's state to me, and starts the task that runs it unless one is running, which then pushes once more.
func (h *Host) queueStatePush(me *managedExt) {
	queue := &me.statePushQueue
	queue.mu.Lock()
	queue.dirty = true
	start := !queue.running
	queue.running = true
	queue.mu.Unlock()
	if start {
		h.goDrainTask(func() { h.runStatePushes(me) })
	}
}

// runStatePushes pushes the host's state to me until no change is waiting. A push ends with the Host's lifetime, so Shutdown joins the task.
func (h *Host) runStatePushes(me *managedExt) {
	queue := &me.statePushQueue
	for {
		queue.mu.Lock()
		if !queue.dirty {
			queue.running = false
			queue.mu.Unlock()
			return
		}
		queue.dirty = false
		queue.mu.Unlock()
		if conn := me.connection(); conn != nil {
			_ = h.pushStateTo(h.recoveryContext, me, conn)
		}
	}
}
