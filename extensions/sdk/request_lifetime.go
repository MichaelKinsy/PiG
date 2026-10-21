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
}

func (c *conn) armParent(id string, runtime, request context.Context) *requestParent {
	parent := &requestParent{conn: c, id: id, runtime: runtime, request: request}
	c.pendingMu.Lock()
	if c.requestParents == nil {
		c.requestParents = make(map[string]*requestParent)
	}
	c.requestParents[id] = parent
	c.pendingMu.Unlock()
	return parent
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
	case callParseFrontmatter, callParseDate, callGenerateDiffString, callGenerateUnifiedPatch, callCopyToClipboard, callConvertToPng, callResizeImage, callHighlightCode, callLoadTheme, callBuiltinToolDefinition:
		return true
	}
	return false
}
