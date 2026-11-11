package sdk

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// requestParent separates an active handler's cancellation scope from a retained Context's runtime lifetime. The connection's write mutex orders parent selection with response publication.
// pig additive (D19): retained SDK contexts keep their original connection generation after normal request completion.
type requestParent struct {
	conn      *conn
	id        string
	runtime   context.Context
	request   context.Context
	completed bool
	finished  bool
	cancelled bool
	// signal is the request's signal, which a signal-only cancel aborts alone (Context.Done and Err); nil until the request is armed with its signal (armRequestScope).
	signal *requestSignal
}

// requestSignal is a request's signal: the context Context.Done and Err report, a child of the request's context, which a signal-only cancel (a tool_call's abort, session_before_compact and session_before_tree's event.signal) ends while the request, and the host calls its handler waits on, go on. An ordinary cancel ends the request and with it the signal.
//
// The child context is made when something first asks for it: most requests never read their signal, and a child made at arming costs every tool_call round trip five allocations (the context, its cancel function and their registration with the request's context). A signal-only cancel makes it, then aborts it, so the order of the cancels is kept. requestSignal is itself the context.Context a Context holds, forwarding to the child, so context.Cause and contexts derived from it see the child.
type requestSignal struct {
	// parent is the request's context, set when the request is armed.
	parent context.Context
	mu     sync.Mutex
	child  context.Context
	abort  context.CancelCauseFunc
}

// get returns the signal's context, making it on first use.
func (s *requestSignal) get() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.child == nil {
		s.child, s.abort = context.WithCancelCause(s.parent)
	}
	return s.child
}

// fire aborts the signal with cause (nil is context.Canceled), leaving the request it belongs to running. The first abort of the signal, or the end of its request, sets its cause.
func (s *requestSignal) fire(cause error) {
	s.get()
	s.mu.Lock()
	abort := s.abort
	s.mu.Unlock()
	abort(cause)
}

func (s *requestSignal) Deadline() (time.Time, bool) { return s.get().Deadline() }
func (s *requestSignal) Done() <-chan struct{}       { return s.get().Done() }
func (s *requestSignal) Err() error                  { return s.get().Err() }
func (s *requestSignal) Value(key any) any           { return s.get().Value(key) }

func (c *conn) armParent(id string, runtime, request context.Context) *requestParent {
	parent, _ := c.armParentWithSignal(id, runtime, request, nil)
	return parent
}

// armParentWithSignal registers the request's parent with its signal and takes the cancel frames the read loop kept for it (queueRequest), in one step under pendingMu: a cancel the read loop reads next finds the armed request. An ordinary cancel among them marks the parent cancelled, so its host calls are refused as after the cancel frame.
func (c *conn) armParentWithSignal(id string, runtime, request context.Context, signal *requestSignal) (*requestParent, queuedCancel) {
	parent := &requestParent{conn: c, id: id, runtime: runtime, request: request, signal: signal}
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.requestParents == nil {
		c.requestParents = make(map[string]*requestParent)
	}
	c.requestParents[id] = parent
	queued := c.unarmed[id]
	delete(c.unarmed, id)
	if queued.request {
		parent.cancelled = true
	}
	return parent, queued
}

// dropQueuedRequest forgets the read loop's queue entry of a request served without arming: while the extension loads, the factory pump answers it and the main loop never sees it.
func (c *conn) dropQueuedRequest(id string) {
	c.pendingMu.Lock()
	delete(c.unarmed, id)
	c.pendingMu.Unlock()
}

// loadingConn is the connection of a Context the factory's own emit made for a listener, while the extension has not connected: afterwards the Context is an ordinary one that rides the connection Run adopted, and this returns nil.
func (c Context) loadingConn() *conn {
	if c.loading == nil {
		return nil
	}
	c.ext.eventMu.Lock()
	defer c.ext.eventMu.Unlock()
	if c.ext.loading != c.loading {
		return nil
	}
	return c.loading
}

func (c Context) hostConnection() *conn {
	if c.parent != nil {
		return c.parent.conn
	}
	if c.loading != nil {
		return c.loading
	}
	return c.ext.conn
}

func (c Context) beginHostCall(method string, args any) (pendingCall, error) {
	connection := c.hostConnection()
	if connection == nil {
		return pendingCall{}, errBusNotConnected
	}
	// The host answers only the bus and the pure helpers before the extension registers, and a call it does not answer would wait for ever.
	if c.loadingConn() != nil && !strings.HasPrefix(method, "events.") && !hostServesBeforeRegister(method) {
		return pendingCall{}, fmt.Errorf("%s: the extension has not registered with the host yet", method)
	}
	// A tool handler's host call, of any kind, is an await of Pi's handler: it orders the handler among the calls of its batch (toolStartOrder). Only a dialog call
	// carries the response slot.
	c.start.beforeCall()
	defer c.start.afterCall()
	var slot *settleSlot
	if c.start != nil && isDialogMethod(method) {
		slot = c.start.responseSlot()
	}
	if c.parent == nil {
		return connection.beginCallForSlot(c.requestID, method, args, slot)
	}
	return connection.beginParentCallFor(c.parent, method, args, slot)
}

func (p *requestParent) lifetime() (context.Context, bool) {
	p.conn.pendingMu.Lock()
	defer p.conn.pendingMu.Unlock()
	return p.runtime, p.completed
}

func (p *requestParent) report(state, reason string) {
	c := p.conn
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.pendingMu.Lock()
	active := !p.finished && (!p.cancelled || state == "progress")
	c.pendingMu.Unlock()
	if active {
		_ = c.sendLocked(envelope{Type: msgRequestState, RequestState: &requestStateMsg{RequestID: p.id, State: state, Reason: reason}})
	}
}

func (c *conn) parentIDLocked(parent *requestParent) (string, error) {
	if parent.cancelled {
		return "", fmt.Errorf("host call cancelled with its parent request %s", parent.id)
	}
	if err := parent.runtime.Err(); err != nil {
		return "", err
	}
	if parent.completed {
		return "", nil
	}
	return parent.id, nil
}

// hostServesBeforeRegister reports whether the host answers method on a connection whose extension has not registered: the pure helpers that read no session state (loadingBusCall).
func hostServesBeforeRegister(method string) bool {
	switch method {
	case callParseFrontmatter, callParseDate, callGenerateDiffString, callGenerateUnifiedPatch, callCopyToClipboard, callConvertToPng, callResizeImage, callHighlightCode, callTypeEngine, callLoadTheme, callBuiltinToolDefinition:
		return true
	}
	return false
}

// queuedCancel is what the read loop read of a request's cancel frames while the main loop had not armed the request: the signal-only cancel and the ordinary cancel, each with the cause it carried. armParentWithSignal hands it to the arming, which applies both before the handler starts, in the order the frames came. The read loop keeps it by value, so queueing a request costs no allocation.
type queuedCancel struct {
	signal       bool
	signalCause  error
	request      bool
	requestCause error
}

// queueRequest notes that the read loop passed request id to the main loop, which has not armed it yet, so a cancel frame that follows is kept for the arming (routeCancel).
func (c *conn) queueRequest(id string) {
	c.pendingMu.Lock()
	if c.unarmed == nil {
		c.unarmed = make(map[string]queuedCancel)
	}
	c.unarmed[id] = queuedCancel{}
	c.pendingMu.Unlock()
}

// routeCancel is what the read loop does with a cancel frame of request id before it queues the frame for the main loop, and reports whether the frame is spent. A signal-only cancel of an armed request aborts the request's signal alone, the request is not cancelled and none of its host calls ends: the handler finds its signal aborted when it wakes from a call whose result the same loop routes after the frame (the main loop reads the frames of requests in a later turn). A cancel of a request the main loop has not armed yet is kept in unarmed, and the arming applies it, so the handler starts with its request ended or its signal aborted whatever the main loop has taken by then; the frame still goes to the main loop, which applies it again to the armed request. A request that is neither armed nor queued is finished or unknown, and its cancel is the main loop's.
func (c *conn) routeCancel(id string, signalOnly bool, cause error) bool {
	c.pendingMu.Lock()
	var signal *requestSignal
	if signalOnly {
		if parent := c.requestParents[id]; parent != nil {
			signal = parent.signal
		}
	}
	if signal == nil {
		if queued, ok := c.unarmed[id]; ok {
			switch {
			case !signalOnly && !queued.request:
				queued.request, queued.requestCause = true, cause
				c.unarmed[id] = queued
			case signalOnly && !queued.signal:
				queued.signal, queued.signalCause = true, cause
				c.unarmed[id] = queued
			}
		}
	}
	c.pendingMu.Unlock()
	if signal == nil {
		return false
	}
	signal.fire(cause)
	return true
}
