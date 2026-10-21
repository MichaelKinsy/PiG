package sdk

import (
	"errors"
	"runtime"

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
