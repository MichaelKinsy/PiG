package subprocess

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// SessionGraphBackend constructs and serves the objects of the session graph (protocol_session_graph.go). The process that owns the coding package implements it; the host only keeps the objects for the connection that built them.
type SessionGraphBackend interface {
	// New constructs the object kind's class builds with op (Pi's `new Class(...)` is op "new", a static factory is its name) from the positional JSON arguments. It never retains ctx past the call; scope is the connection's and lives as long as the object does.
	New(ctx context.Context, scope SessionGraphScope, kind, op string, args json.RawMessage) (SessionGraphObject, error)
	// Static runs a static function of kind's class that constructs no object, and answers its result in Pi's JSON shape. A nil result is JSON null.
	Static(ctx context.Context, scope SessionGraphScope, kind, method string, args json.RawMessage) (any, error)
}

// SessionGraphObject is one object of the session graph.
type SessionGraphObject interface {
	// Call runs the method Pi's class has under that name with the positional JSON arguments, and answers its result in Pi's JSON shape. A nil result is JSON null. A cancelled ctx is the method's aborted AbortSignal.
	Call(ctx context.Context, method string, args json.RawMessage) (any, error)
}

// SessionGraphScope is the connection an object belongs to: the objects it holds and the functions it passed.
type SessionGraphScope interface {
	// Object returns the live object handle names on the connection, which an option that takes an object (a loader's settingsManager) names by handle.
	Object(handle string) (SessionGraphObject, bool)
	// Callback runs the function the extension named id (RequestSessionGraphCallback) and answers its result. A closed connection fails it.
	Callback(ctx context.Context, id string, args json.RawMessage) (json.RawMessage, error)
}

// SessionGraphStreamer is an object that serves a model stream (ModelRuntime.stream and streamSimple). A modelStream call that names the object's handle in its runtime field runs on it, and the call delivers the events as it does for the Session's runtime.
type SessionGraphStreamer interface {
	// StreamModel starts the stream for the model object and request an extension sent. ctx selects streamSimple (extension.ProviderStreamSimpleSelected) and carries the request's callbacks (extension.ModelStreamRequestFromContext).
	StreamModel(ctx context.Context, model map[string]any, request map[string]any) (*ai.AssistantMessageEventStream, error)
}

// SessionGraphReleaser is an object that holds resources past its last reference: the table calls Release once, when the last holder lets go. The extension's handle is one holder (it ends when the extension releases the handle or its connection closes) and every child session created over the object is another (it ends when the session closes), as an object of Pi's lives while anything reaches it.
type SessionGraphReleaser interface {
	Release()
}

type graphResolver struct {
	host *Host
	conn *Conn
}

type graphResolverKey struct{}

// withGraphResolver lets a call's handler find the session graph objects of the connection that made the call.
func withGraphResolver(ctx context.Context, resolver graphResolver) context.Context {
	return context.WithValue(ctx, graphResolverKey{}, resolver)
}

func graphStreamerOf(ctx context.Context, handle string) (SessionGraphStreamer, error) {
	resolver, _ := ctx.Value(graphResolverKey{}).(graphResolver)
	if resolver.host == nil {
		return nil, errors.New(SessionGraphUnavailableMessage)
	}
	object, ok := resolver.host.sessionGraphObjects.lookup(resolver.conn, handle)
	if !ok {
		return nil, errors.New(SessionGraphUnknownMessage)
	}
	streamer, ok := object.(SessionGraphStreamer)
	if !ok {
		return nil, fmt.Errorf("session graph object %s does not stream models", handle)
	}
	return streamer, nil
}

// GraphVirtualModelRoute is the route function of a virtual model whose router is the function the extension named callbackID: it runs on the extension's connection through the scope, with Pi's ModelRouteRequest as JSON.
func GraphVirtualModelRoute(scope SessionGraphScope, callbackID string) extension.ModelRouteFunc {
	return func(ctx context.Context, request extension.ModelRouteRequest) (extension.ModelRoute, error) {
		wire, err := virtualModelRouteRequest(request)
		if err != nil {
			return extension.ModelRoute{}, err
		}
		args, err := json.Marshal([]VirtualModelRouteRequest{wire})
		if err != nil {
			return extension.ModelRoute{}, err
		}
		raw, err := scope.Callback(ctx, callbackID, args)
		if err != nil {
			return extension.ModelRoute{}, err
		}
		var routed VirtualModelRouteResult
		if err := json.Unmarshal(raw, &routed); err != nil || routed.Model == nil {
			return extension.ModelRoute{}, errors.New("the router returned no route")
		}
		provider, _ := routed.Model["provider"].(string)
		id, _ := routed.Model["id"].(string)
		return extension.ModelRoute{Model: &ai.Model{ID: id, ProviderMeta: ai.ProviderMetadata{ProviderID: provider}}, ThinkingLevel: ai.ModelThinkingLevel(routed.ThinkingLevel), State: routeState(request.State, routed, false)}, nil
	}
}

type graphScope struct {
	host *Host
	conn *Conn
}

func (s graphScope) Object(handle string) (SessionGraphObject, bool) {
	return s.host.sessionGraphObjects.lookup(s.conn, handle)
}

func (s graphScope) Callback(ctx context.Context, id string, args json.RawMessage) (json.RawMessage, error) {
	data, err := json.Marshal(SessionGraphCallbackArgs{ID: id, Args: args})
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", RequestSessionGraphCallback, err)
	}
	response, err := s.conn.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: RequestSessionGraphCallback, Args: data}})
	if err != nil {
		return nil, err
	}
	if response.Response != nil && response.Response.Error != nil {
		return nil, response.Response.Error.ToError()
	}
	if response.Response == nil {
		return nil, nil
	}
	return response.Response.Result, nil
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

// SessionGraphObjectOf returns the live object handle names on conn, or false when the connection holds none. The caller holds nothing: a caller that keeps the object past its call takes a hold with the table's retain.
func (h *Host) SessionGraphObjectOf(conn *Conn, handle string) (SessionGraphObject, bool) {
	return h.sessionGraphObjects.lookup(conn, handle)
}

// sessionGraphTable holds the objects of every connection.
type sessionGraphTable struct {
	mu    sync.Mutex
	next  uint64
	conns map[*Conn]*connSessionGraph
	// closing counts the connections whose objects are being dropped, so closeAll can wait for a drop a connection's watcher began. Add runs under mu, when the connection leaves conns.
	closing sync.WaitGroup
}

type connSessionGraph struct {
	objects map[string]*graphHold
}

// graphHold is an object and the number of holders that keep it: the extension's handle, and every session created over it. The last holder to let go releases it.
type graphHold struct {
	object  SessionGraphObject
	holders int
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
		owned = &connSessionGraph{objects: map[string]*graphHold{}}
		t.conns[conn] = owned
		go func() {
			<-conn.Done()
			t.closeConn(conn)
		}()
	}
	t.next++
	handle := "sg" + strconv.FormatUint(t.next, 10)
	owned.objects[handle] = &graphHold{object: object, holders: 1}
	return handle, nil
}

// closeConn lets go of the extension's handles on conn.
func (t *sessionGraphTable) closeConn(conn *Conn) {
	t.mu.Lock()
	owned := t.conns[conn]
	delete(t.conns, conn)
	if owned != nil {
		t.closing.Add(1)
	}
	t.mu.Unlock()
	if owned != nil {
		t.dropAll(owned)
	}
}

// closeAll lets go of the handles of every connection and returns when no drop is still running, including one a connection's watcher began. Host.Shutdown calls it, after the child sessions closed: an object that held a process releases it when its last holder lets go.
func (t *sessionGraphTable) closeAll() {
	t.mu.Lock()
	var all []*connSessionGraph
	for conn, owned := range t.conns {
		delete(t.conns, conn)
		t.closing.Add(1)
		all = append(all, owned)
	}
	t.mu.Unlock()
	for _, owned := range all {
		t.dropAll(owned)
	}
	t.closing.Wait()
}

// dropAll ends the extension's hold of every object of owned, and its count in t.closing.
func (t *sessionGraphTable) dropAll(owned *connSessionGraph) {
	defer t.closing.Done()
	t.mu.Lock()
	holds := owned.objects
	owned.objects = nil
	t.mu.Unlock()
	for _, hold := range holds {
		t.drop(hold)
	}
}

func (t *sessionGraphTable) lookup(conn *Conn, handle string) (SessionGraphObject, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if owned := t.conns[conn]; owned != nil {
		if hold, ok := owned.objects[handle]; ok {
			return hold.object, true
		}
	}
	return nil, false
}

// retain adds a holder to the live object handle names on conn, and returns the object and the function that ends that hold; the function is idempotent. The object is released when its last holder lets go, whether or not the extension has released its handle by then.
func (t *sessionGraphTable) retain(conn *Conn, handle string) (SessionGraphObject, func(), bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	owned := t.conns[conn]
	if owned == nil {
		return nil, nil, false
	}
	hold, ok := owned.objects[handle]
	if !ok {
		return nil, nil, false
	}
	hold.holders++
	var once sync.Once
	return hold.object, func() { once.Do(func() { t.drop(hold) }) }, true
}

func (t *sessionGraphTable) remove(conn *Conn, handle string) {
	t.mu.Lock()
	var hold *graphHold
	if owned := t.conns[conn]; owned != nil {
		hold = owned.objects[handle]
		delete(owned.objects, handle)
	}
	t.mu.Unlock()
	if hold != nil {
		t.drop(hold)
	}
}

// drop ends one hold of hold's object and releases the object when none is left.
func (t *sessionGraphTable) drop(hold *graphHold) {
	t.mu.Lock()
	hold.holders--
	last := hold.holders == 0
	t.mu.Unlock()
	if last {
		releaseGraphObject(hold.object)
	}
}

func releaseGraphObject(object SessionGraphObject) {
	if releaser, ok := object.(SessionGraphReleaser); ok {
		releaser.Release()
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
		object, err := backend.New(ctx, graphScope{h, conn}, request.Kind, request.Op, request.Args)
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
			value, err = backend.Static(ctx, graphScope{h, conn}, request.Kind, request.Method, request.Args)
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
