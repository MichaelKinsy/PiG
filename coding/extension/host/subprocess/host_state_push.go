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
	if h.uiBridge == nil {
		return
	}
	h.hostStateEpoch.Add(1)
	if h.hostStatePushQueued.Swap(true) {
		return
	}
	h.goDrainTask(h.fanOutStatePush)
}

// hostStateChanging reports that the Session's identity state (the model, the session id, name and file) is about to change, and hostStateSettled that the change was reported through hostStateChanged (UIBridge.HostStateChanging, HostStateSettled). The change becomes visible to a host call, which can read it and reply, before the owner reports it: a reply in that stretch flushes the state first, as one after the report does (replyToCall).
func (h *Host) hostStateChanging() { h.hostStateInflight.Add(1) }

func (h *Host) hostStateSettled() { h.hostStateInflight.Add(-1) }

// behindHostState reports whether a host-side change may not have reached me: one is reported and not yet pushed to it, or one is in flight.
func (h *Host) behindHostState(me *managedExt) bool {
	return me.pushedStateEpoch.Load() < h.hostStateEpoch.Load() || h.hostStateInflight.Load() > 0
}

// statePushedAt records that me received a state that holds every host state change up to epoch.
func (me *managedExt) statePushedAt(epoch uint64) {
	for held := me.pushedStateEpoch.Load(); held < epoch && !me.pushedStateEpoch.CompareAndSwap(held, epoch); held = me.pushedStateEpoch.Load() {
	}
}

// The ready payload is a connection's first state, so the Host writes no state to a connection before it. A runtime reads the frames that precede its ready while its factory still loads, and a state there has no place in its order: the ready's snapshot can be older than the frame and is applied after it. A call the Host answers before the register handshake (loadingBusCall) replies through replyToCall, and a broadcast reaches a restarting extension's new connection, so a push can find a connection in its handshake: it is withheld and owed, and sendReady pays it after the ready.
const (
	// connReady is a connection whose ready is queued, or one that was not adopted through the handshake: it takes state pushes. It is the zero value.
	connReady uint32 = iota
	// connAwaitsReady is a connection the Host adopted whose ready is not sent.
	connAwaitsReady
	// connOwedState is connAwaitsReady after a state push was withheld that the ready's snapshot, taken earlier, may not hold.
	connOwedState
)

// awaitReady marks c as adopted and waiting for its ready. The caller has not published or started c.
func (c *Conn) awaitReady() { c.readyState.Store(connAwaitsReady) }

// withholdState reports whether a state push to c waits for its ready, and records that the push is owed.
func (c *Conn) withholdState() bool {
	for {
		switch c.readyState.Load() {
		case connReady:
			return false
		case connOwedState:
			return true
		}
		if c.readyState.CompareAndSwap(connAwaitsReady, connOwedState) {
			return true
		}
	}
}

// readySent opens c to state pushes once its ready is queued, and reports whether one is owed.
func (c *Conn) readySent() bool { return c.readyState.Swap(connReady) == connOwedState }

// readyStateEpoch begins the snapshot of conn's ready payload and returns the hostStateEpoch it holds: the epoch is read before the snapshot, as pushState reads it, so the snapshot holds every change up to it. A push withheld before now is no longer owed, because the snapshot holds its state too.
func (h *Host) readyStateEpoch(conn *Conn) uint64 {
	conn.readyState.CompareAndSwap(connOwedState, connAwaitsReady)
	return h.hostStateEpoch.Load()
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
