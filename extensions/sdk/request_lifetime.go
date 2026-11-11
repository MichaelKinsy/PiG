package sdk

import (
	"context"
	"fmt"
	"strings"
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
	// signal aborts the request's signal alone (Context.Done and Err), for a signal-only cancel; nil until the request is armed with its signal (armRequestScope).
	signal context.CancelCauseFunc
}

func (c *conn) armParent(id string, runtime, request context.Context) *requestParent {
	parent, _ := c.armParentWithSignal(id, runtime, request, nil)
	return parent
}

// armParentWithSignal registers the request's parent with its signal and takes the cancel frames the read loop kept for it (queueRequest), in one step under pendingMu: a cancel the read loop reads next finds the armed request. An ordinary cancel among them marks the parent cancelled, so its host calls are refused as after the cancel frame.
func (c *conn) armParentWithSignal(id string, runtime, request context.Context, signal context.CancelCauseFunc) (*requestParent, *queuedCancel) {
	parent := &requestParent{conn: c, id: id, runtime: runtime, request: request, signal: signal}
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.requestParents == nil {
		c.requestParents = make(map[string]*requestParent)
	}
	c.requestParents[id] = parent
	queued := c.unarmed[id]
	delete(c.unarmed, id)
	if queued != nil && queued.request {
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

// queuedCancel is what the read loop read of a request's cancel frames while the main loop had not armed the request: the signal-only cancel and the ordinary cancel, each with the cause it carried. armParentWithSignal hands it to the arming, which applies both before the handler starts, in the order the frames came.
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
		c.unarmed = make(map[string]*queuedCancel)
	}
	c.unarmed[id] = &queuedCancel{}
	c.pendingMu.Unlock()
}

// routeCancel is what the read loop does with a cancel frame of request id before it queues the frame for the main loop, and reports whether the frame is spent. A signal-only cancel of an armed request aborts the request's signal alone, the request is not cancelled and none of its host calls ends: the handler finds its signal aborted when it wakes from a call whose result the same loop routes after the frame (the main loop reads the frames of requests in a later turn). A cancel of a request the main loop has not armed yet is kept in unarmed, and the arming applies it, so the handler starts with its request ended or its signal aborted whatever the main loop has taken by then; the frame still goes to the main loop, which applies it again to the armed request. A request that is neither armed nor queued is finished or unknown, and its cancel is the main loop's.
func (c *conn) routeCancel(id string, signalOnly bool, cause error) bool {
	c.pendingMu.Lock()
	var signal context.CancelCauseFunc
	if signalOnly {
		if parent := c.requestParents[id]; parent != nil {
			signal = parent.signal
		}
	}
	if signal == nil {
		if queued := c.unarmed[id]; queued != nil {
			switch {
			case !signalOnly && !queued.request:
				queued.request, queued.requestCause = true, cause
			case signalOnly && !queued.signal:
				queued.signal, queued.signalCause = true, cause
			}
		}
	}
	c.pendingMu.Unlock()
	if signal == nil {
		return false
	}
	signal(cause)
	return true
}
