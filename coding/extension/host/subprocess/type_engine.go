package subprocess

import (
	"errors"
	"sync"
	"sync/atomic"
)

// TypeEngineSession is what the host asks the TypeBox engine for on behalf of one extension connection: Serve answers its type.engine requests, and Close cancels the work the
// engine keeps parked for it (the walks of Value.Check and Value.Errors that wait for the answer of a refinement of the extension's). internal/typebox/engine.Session is one.
type TypeEngineSession interface {
	Serve(request []byte) []byte
	Close()
}

// typeEngine opens the sessions of the TypeBox type engine the host answers CallTypeEngine with, nil until a program registers one.
var typeEngine atomic.Pointer[func() TypeEngineSession]

// SetTypeEngine registers the engine that answers CallTypeEngine: open makes the session of one connection (internal/typebox/engine.NewSession).
// Linking coding/extension/pigsdk with at least one of the Go, Rust and Python SDKs, the three that call it, registers it, so a program that links the
// host without that package (the coding library, examples/headless) does not link the engine through it.
func SetTypeEngine(open func() TypeEngineSession) {
	typeEngine.Store(&open)
}

// errTypeEngineAbsent is the reply to a CallTypeEngine that arrives where no engine is registered.
var errTypeEngineAbsent = errors.New("type.engine: this build does not link the TypeBox type engine (it embeds none of the Go, Rust and Python SDKs, or the program links the extension host without them)")

// typeEngineTable holds the engine session of each extension connection. The session is made with the connection's first type.engine call and closed when the connection closes (a
// reload, a crash, the end of the session) or the host shuts down, so the walks an extension left parked never outlive it.
type typeEngineTable struct {
	mu    sync.Mutex
	conns map[*Conn]TypeEngineSession
}

// session is the engine session of conn. A connection that has closed gets a closed one, which refuses work that would park; a call with no connection gets a session of its own,
// which the caller closes.
func (t *typeEngineTable) session(conn *Conn, open func() TypeEngineSession) (session TypeEngineSession, own bool) {
	if conn == nil {
		return open(), true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if session, ok := t.conns[conn]; ok {
		return session, false
	}
	session = open()
	if conn.closed.Load() {
		session.Close()
		return session, false
	}
	if t.conns == nil {
		t.conns = map[*Conn]TypeEngineSession{}
	}
	t.conns[conn] = session
	go func() {
		<-conn.Done()
		t.closeConn(conn)
	}()
	return session, false
}

func (t *typeEngineTable) closeConn(conn *Conn) {
	t.mu.Lock()
	session := t.conns[conn]
	delete(t.conns, conn)
	t.mu.Unlock()
	if session != nil {
		session.Close()
	}
}

// closeAll closes the session of every connection. Host.Shutdown calls it.
func (t *typeEngineTable) closeAll() {
	t.mu.Lock()
	sessions := t.conns
	t.conns = nil
	t.mu.Unlock()
	for _, session := range sessions {
		session.Close()
	}
}

func (h *Host) handleTypeEngine(conn *Conn, call *CallPayload) (*CallResultPayload, error) {
	open := typeEngine.Load()
	if open == nil {
		return nil, errTypeEngineAbsent
	}
	session, own := h.typeEngines.session(conn, *open)
	if own {
		defer session.Close()
	}
	return &CallResultPayload{Result: session.Serve(call.Args)}, nil
}
