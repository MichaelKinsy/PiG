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
	// The host answers only the bus and the helpers that need its parsers before the extension registers, and a call it does not answer would wait for ever.
	if c.loadingConn() != nil && !strings.HasPrefix(method, "events.") && method != callParseFrontmatter && method != callParseDate {
		return pendingCall{}, fmt.Errorf("%s: the extension has not registered with the host yet", method)
	}
	if c.parent == nil {
		return connection.beginCallFor(c.requestID, method, args)
	}
	return connection.beginParentCall(c.parent, method, args)
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
