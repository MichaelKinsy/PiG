package subprocess

import (
	"context"
	"encoding/json"
	"sync"
)

// runSignalNotify is the notify that tells a runtime the state of the active run's signal, which the runtime exposes as ctx.signal.
// Upstream gives an extension `() => agent.signal` (runner.ts:917-920, agent-session.ts:3368): the signal of the run in progress, one object for the whole run, aborted with the run, undefined while no run is active.
// A runtime cannot read the host's context, so the host replicates it: a frame when a run begins or ends (UIBridge.RunSignalChanged), so a timer reading ctx.signal between requests sees the run as it is, a frame before every request the runtime serves, and a frame at the abort, which reaches a handler already in flight.
const runSignalNotify = "run_signal"

// runSignalFrame is the wire state of the run signal. Run numbers the runs of this Host so a runtime can tell a new run from the one it holds; Active is whether a run is in progress; Aborted is whether its signal is cancelled.
type runSignalFrame struct {
	Run     uint64 `json:"run"`
	Active  bool   `json:"active"`
	Aborted bool   `json:"aborted"`
}

// runSignals numbers the run signals the UIBridge reports and watches the current one for its abort.
type runSignals struct {
	mu      sync.Mutex
	current context.Context
	id      uint64
	stop    func() bool
	// endedAborted is whether the run numbered endedID was aborted when it ended or another replaced it.
	endedID      uint64
	endedAborted bool
}

// frame returns the state of signal, which is nil while no run is active, and whether held, the state a runtime holds, is a run that has since been aborted. A signal not seen before begins a run and is watched: abort runs when it is cancelled. frame must be called under the lock of the Conn the frame is for, so a newer state never overtakes an older one on the wire.
func (r *runSignals) frame(signal context.Context, abort func(), held runSignalFrame) (heldAborted bool, frame runSignalFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if signal != r.current {
		if r.stop != nil {
			r.stop()
			r.stop = nil
		}
		if r.current != nil {
			r.endedID, r.endedAborted = r.id, r.current.Err() != nil
		}
		r.current = signal
		if signal != nil {
			r.id++
			r.stop = context.AfterFunc(signal, abort)
		}
	}
	if held.Active && !held.Aborted {
		switch {
		case held.Run == r.id && r.current != nil:
			heldAborted = r.current.Err() != nil
		case held.Run == r.endedID:
			heldAborted = r.endedAborted
		}
	}
	if signal == nil {
		return heldAborted, runSignalFrame{Run: r.id}
	}
	return heldAborted, runSignalFrame{Run: r.id, Active: true, Aborted: signal.Err() != nil}
}

// release stops watching the current run's signal.
func (r *runSignals) release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stop != nil {
		r.stop()
		r.stop = nil
	}
	if r.current != nil {
		r.endedID, r.endedAborted = r.id, r.current.Err() != nil
	}
	r.current = nil
}

// runSignalSync is the last frame a Conn's runtime was sent.
type runSignalSync struct {
	mu   sync.Mutex
	sent runSignalFrame
}

// syncRunSignal brings conn's runtime up to date with the active run's signal. It sends nothing when the runtime already holds the state.
func (h *Host) syncRunSignal(conn *Conn) error {
	if h.uiBridge == nil || conn == nil {
		return nil
	}
	conn.runSignal.mu.Lock()
	defer conn.runSignal.mu.Unlock()
	heldAborted, frame := h.runSignals.frame(h.uiBridge.RunSignal(), h.syncRunSignals, conn.runSignal.sent)
	if frame == conn.runSignal.sent {
		return nil
	}
	// Upstream aborts the signal a handler holds synchronously (agent.ts:336-338). The run may end, or another begin, before the watcher forwards the abort, so a runtime still holding the aborted run first learns of the abort, then of the new state.
	if heldAborted && (frame.Run != conn.runSignal.sent.Run || !frame.Active) {
		if err := h.sendRunSignal(conn, runSignalFrame{Run: conn.runSignal.sent.Run, Active: true, Aborted: true}); err != nil {
			return err
		}
	}
	return h.sendRunSignal(conn, frame)
}

// sendRunSignal queues frame for conn's runtime and records it as sent. The caller holds conn.runSignal.mu.
func (h *Host) sendRunSignal(conn *Conn, frame runSignalFrame) error {
	args, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	// The frame is queued, not awaited: the writer sends frames in queue order, so it still precedes the request or cancel frame queued after it, and a peer that stops reading cannot hold a cancelled request.
	if err := conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: runSignalNotify, Args: args}}); err != nil {
		return err
	}
	conn.runSignal.sent = frame
	return nil
}

// runSignalChanged brings every runtime up to date when a run begins or ends, as upstream's getter reads the run at the moment of the call (runner.ts:917-920). The owner reports the change from the goroutine that ran it, which must not wait for a runtime that has stopped reading, so the frames are queued on a task Shutdown joins. A request still syncs its own runtime first, so the task only reaches runtimes that no request does.
func (h *Host) runSignalChanged() {
	h.goDrainTask(h.syncRunSignals)
}

// syncRunSignals brings every connected runtime up to date with the active run's signal, aborted or not. A runtime that does not answer delays only its own frame.
func (h *Host) syncRunSignals() {
	h.mu.Lock()
	conns := make([]*Conn, 0, len(h.exts))
	for _, me := range h.exts {
		if conn := me.connection(); conn != nil {
			conns = append(conns, conn)
		}
	}
	h.mu.Unlock()
	var wg sync.WaitGroup
	for _, conn := range conns {
		wg.Go(func() { _ = h.syncRunSignal(conn) })
	}
	wg.Wait()
}
