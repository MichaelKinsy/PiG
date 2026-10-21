package subprocess

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// SessionGraphBackend constructs and serves the objects of the session graph (protocol_session_graph.go). The process that owns the coding package implements it; the host only keeps the objects for the connection that built them.
type SessionGraphBackend interface {
	// New constructs the object kind's class builds with op (Pi's `new Class(...)` is op "new", a static factory is its name) from the positional JSON arguments. It never retains ctx past the call.
	New(ctx context.Context, kind, op string, args json.RawMessage) (SessionGraphObject, error)
	// Static runs a static function of kind's class that constructs no object, and answers its result in Pi's JSON shape. A nil result is JSON null.
	Static(ctx context.Context, kind, method string, args json.RawMessage) (any, error)
}

// SessionGraphObject is one object of the session graph.
type SessionGraphObject interface {
	// Call runs the method Pi's class has under that name with the positional JSON arguments, and answers its result in Pi's JSON shape. A nil result is JSON null. A cancelled ctx is the method's aborted AbortSignal.
	Call(ctx context.Context, method string, args json.RawMessage) (any, error)
}

// SetSessionGraph binds the backend that serves the session graph. Without one the calls fail with SessionGraphUnavailableMessage.
func (h *Host) SetSessionGraph(backend SessionGraphBackend) {
	h.sessionGraphMu.Lock()
	h.sessionGraphBackend = backend
	h.sessionGraphMu.Unlock()
}

func (h *Host) sessionGraph() SessionGraphBackend {
	h.sessionGraphMu.Lock()
	defer h.sessionGraphMu.Unlock()
	return h.sessionGraphBackend
}

// SessionGraphObjectOf returns the live object handle names on conn, or false when the connection holds none. It is how another host call (createAgentSession) takes the objects an extension passed to it.
func (h *Host) SessionGraphObjectOf(conn *Conn, handle string) (SessionGraphObject, bool) {
	return h.sessionGraphObjects.lookup(conn, handle)
}

// sessionGraphTable holds the objects of every connection.
type sessionGraphTable struct {
	mu    sync.Mutex
	next  uint64
	conns map[*Conn]*connSessionGraph
}

type connSessionGraph struct {
	objects map[string]SessionGraphObject
}

// add makes object live for conn and names it. A connection that closed refuses it.
func (t *sessionGraphTable) add(conn *Conn, object SessionGraphObject) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	owned := t.conns[conn]
	if owned == nil {
		if conn.closed.Load() {
			return "", errors.New(SessionGraphClosedMessage)
		}
		if t.conns == nil {
			t.conns = map[*Conn]*connSessionGraph{}
		}
		owned = &connSessionGraph{objects: map[string]SessionGraphObject{}}
		t.conns[conn] = owned
		go func() {
			<-conn.Done()
			t.mu.Lock()
			delete(t.conns, conn)
			t.mu.Unlock()
		}()
	}
	t.next++
	handle := "sg" + strconv.FormatUint(t.next, 10)
	owned.objects[handle] = object
	return handle, nil
}

func (t *sessionGraphTable) lookup(conn *Conn, handle string) (SessionGraphObject, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if owned := t.conns[conn]; owned != nil {
		object, ok := owned.objects[handle]
		return object, ok
	}
	return nil, false
}

func (t *sessionGraphTable) remove(conn *Conn, handle string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if owned := t.conns[conn]; owned != nil {
		delete(owned.objects, handle)
	}
}

// handleSessionGraphCall serves the sessionGraph.* calls.
func (h *Host) handleSessionGraphCall(ctx context.Context, conn *Conn, call *CallPayload) (*CallResultPayload, error) {
	backend := h.sessionGraph()
	if backend == nil {
		return nil, errors.New(SessionGraphUnavailableMessage)
	}
	switch call.Method {
	case CallSessionGraphNew:
		var request SessionGraphNew
		if err := json.Unmarshal(call.Args, &request); err != nil {
			return nil, fmt.Errorf("decode %s: %w", call.Method, err)
		}
		object, err := backend.New(ctx, request.Kind, request.Op, request.Args)
		if err != nil {
			return nil, err
		}
		handle, err := h.sessionGraphObjects.add(conn, object)
		if err != nil {
			return nil, err
		}
		result, err := json.Marshal(SessionGraphHandle{Handle: handle})
		return &CallResultPayload{Result: result}, err
	case CallSessionGraphRelease:
		var request SessionGraphHandle
		if err := json.Unmarshal(call.Args, &request); err != nil {
			return nil, fmt.Errorf("decode %s: %w", call.Method, err)
		}
		h.sessionGraphObjects.remove(conn, request.Handle)
		return &CallResultPayload{}, nil
	case CallSessionGraphCall, CallSessionGraphAsync:
		var request SessionGraphCall
		if err := json.Unmarshal(call.Args, &request); err != nil {
			return nil, fmt.Errorf("decode %s: %w", call.Method, err)
		}
		if call.Method == CallSessionGraphAsync {
			// A Promise-returning method starts here, in the extension's order, and its result is awaited off the call lane.
			extension.CallInitiated(ctx)
		}
		var value any
		var err error
		if request.Handle == "" {
			value, err = backend.Static(ctx, request.Kind, request.Method, request.Args)
		} else {
			object, ok := h.sessionGraphObjects.lookup(conn, request.Handle)
			if !ok {
				return nil, errors.New(SessionGraphUnknownMessage)
			}
			value, err = object.Call(ctx, request.Method, request.Args)
		}
		if err != nil {
			return nil, err
		}
		if value == nil {
			return &CallResultPayload{Result: json.RawMessage("null")}, nil
		}
		result, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return &CallResultPayload{Result: result}, nil
	}
	return nil, fmt.Errorf("unknown session graph call %s", call.Method)
}
