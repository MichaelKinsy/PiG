package sdk

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The session graph: the classes Pi extensions construct around a session (SessionManager, SettingsManager, ModelRuntime, DefaultResourceLoader, DefaultPackageManager, ProjectTrustStore). The host owns each object for the extension's connection; the value an extension holds names it. Every method is Pi's method under its Go name, with Pi's positional parameters (an optional parameter is a pointer or a variadic option and nil is Pi's undefined) and Pi's result in the JSON shape Pi's types have.

// Wire methods of the session graph (coding/extension/host/subprocess/protocol_session_graph.go).
const (
	graphNew         = "sessionGraph.new"
	graphCall        = "sessionGraph.call"
	graphAsync       = "sessionGraph.async"
	graphReleaseCall = "sessionGraph.release"
)

// graphRelease is what the cleanup of a collected object needs; it holds no reference to the object.
type graphRelease struct {
	context Context
	handle  string
}

// graphObject is the extension's reference to a host object.
type graphObject struct {
	context Context
	handle  string
}

// newGraphObject constructs a host object of Pi's class kind with its static factory op ("new" for a constructor).
func newGraphObject(c Context, kind, op string, args ...any) (*graphObject, error) {
	reply, err := hostDecoded[struct {
		Handle string `json:"handle"`
	}](c, graphNew, map[string]any{"kind": kind, "op": op, "args": args})
	if err != nil {
		return nil, err
	}
	if reply.Handle == "" {
		return nil, errors.New("host returned no session graph handle")
	}
	object := &graphObject{context: c, handle: reply.Handle}
	// Pi frees an object when it is unreachable; the host keeps its reference until the extension's connection closes or the object is released.
	runtime.AddCleanup(object, func(release graphRelease) {
		go func() { _, _ = release.context.callHost(graphReleaseCall, map[string]any{"handle": release.handle}) }()
	}, graphRelease{context: c, handle: reply.Handle})
	return object, nil
}

// Handle is the host's name for the object, which an option that takes the object (createAgentSession) puts on the wire.
func (o *graphObject) Handle() string { return o.handle }

func graphMethodCall[T any](o *graphObject, method string, args ...any) (T, error) {
	return hostDecoded[T](o.context, graphCall, map[string]any{"handle": o.handle, "method": method, "args": args})
}

func graphMethodAsync[T any](o *graphObject, method string, args ...any) (T, error) {
	return hostDecoded[T](o.context, graphAsync, map[string]any{"handle": o.handle, "method": method, "args": args})
}

func graphStatic[T any](c Context, kind, method string, args ...any) (T, error) {
	return hostDecoded[T](c, graphCall, map[string]any{"kind": kind, "method": method, "args": args})
}

func graphStaticAsync[T any](c Context, kind, method string, args ...any) (T, error) {
	return hostDecoded[T](c, graphAsync, map[string]any{"kind": kind, "method": method, "args": args})
}

// graphDone is a method whose Pi result is void.
type graphDone = json.RawMessage

// requestGraphCallback runs a function passed in an option of a session graph object (host protocol RequestSessionGraphCallback).
const requestGraphCallback = "sessionGraph.callback"

// graphCallback is a function of an option: it takes the JSON arguments the host sends and answers the JSON result.
type graphCallback func(args []json.RawMessage) (any, error)

// graphCallbackRegistry names the functions an extension passed to session graph objects. A function stays until the connection ends: the host may run it again on a later reload of the object it was passed to, including after the extension dropped the object.
type graphCallbackRegistry struct {
	mu        sync.Mutex
	next      uint64
	callbacks map[string]graphCallback
}

func (r *graphCallbackRegistry) add(callback graphCallback) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.callbacks == nil {
		r.callbacks = map[string]graphCallback{}
	}
	r.next++
	id := "cb" + strconv.FormatUint(r.next, 10)
	r.callbacks[id] = callback
	return id
}

func (r *graphCallbackRegistry) get(id string) graphCallback {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.callbacks[id]
}

// callbackID names callback for the host. A nil callback is Pi's undefined option and has no name.
func (c Context) callbackID(callback graphCallback) any {
	if callback == nil {
		return nil
	}
	return c.ext.graphCallbacks.add(callback)
}

// dispatchGraphCallback runs the function a RequestSessionGraphCallback names.
func (e *Extension) dispatchGraphCallback(raw json.RawMessage) (any, error) {
	var request struct {
		ID   string            `json:"id"`
		Args []json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, fmt.Errorf("decode session graph callback: %w", err)
	}
	callback := e.graphCallbacks.get(request.ID)
	if callback == nil {
		return nil, fmt.Errorf("unknown session graph callback %s", request.ID)
	}
	return callback(request.Args)
}
