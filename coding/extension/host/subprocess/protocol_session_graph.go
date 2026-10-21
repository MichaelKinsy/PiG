package subprocess

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The session graph: the objects an extension builds around a Pi session, held by the host for the extension's connection and named on the wire by an opaque handle. Pi's SessionManager, SettingsManager, ModelRuntime, DefaultResourceLoader, DefaultPackageManager and ProjectTrustStore (core/session-manager.ts, settings-manager.ts, model-runtime.ts, resource-loader.ts, package-manager.ts, project-trust.ts) are classes an extension constructs and passes to createAgentSession. The Node runtime serves Pi's own classes in the extension process; the Go, Python and Rust SDKs hold a handle and ask the host, which owns the object and releases it with the connection.
//
// Every method of a class crosses the wire under its Pi name with the positional arguments Pi's signature has: an argument Pi leaves undefined is JSON null (or omitted at the end). A result Pi returns as undefined is JSON null. A Promise-returning method is a call that settles when Pi's Promise would. Calls run in the order the extension sent them.

// Session graph calls (ext→host).
const (
	// CallSessionGraphNew constructs an object. Args is a SessionGraphNew and the result a SessionGraphHandle.
	CallSessionGraphNew = "sessionGraph.new"
	// CallSessionGraphCall runs a method that returns synchronously in Pi. Args is a SessionGraphCall and the result the method's result in Pi's JSON shape. The call applies in the order the extension sent it, before the extension's next call.
	CallSessionGraphCall = "sessionGraph.call"
	// CallSessionGraphAsync runs a method that returns a Promise in Pi (SessionManager.list, SettingsManager.reload, ModelRuntime.completeSimple). Args and result are those of CallSessionGraphCall. The call starts in the order the extension sent it and completes on its own, so a later call never waits for it; cancelling the call aborts the method's AbortSignal.
	CallSessionGraphAsync = "sessionGraph.async"
	// CallSessionGraphRelease drops the host's reference to an object. Args is a SessionGraphHandle. A session that was created with the object keeps its own reference.
	CallSessionGraphRelease = "sessionGraph.release"
)

// Object kinds, named by Pi's classes.
const (
	SessionGraphSessionManager    = "SessionManager"
	SessionGraphSettingsManager   = "SettingsManager"
	SessionGraphModelRuntime      = "ModelRuntime"
	SessionGraphResourceLoader    = "DefaultResourceLoader"
	SessionGraphPackageManager    = "DefaultPackageManager"
	SessionGraphProjectTrustStore = "ProjectTrustStore"
)

// SessionGraphNew is the CallSessionGraphNew argument. Op is the Pi constructor or static factory (`SessionManager.inMemory` is kind "SessionManager", op "inMemory"; a class constructed with `new` is op "new") and Args its positional arguments.
type SessionGraphNew struct {
	Kind string          `json:"kind"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
}

// SessionGraphHandle names an object of the connection.
type SessionGraphHandle struct {
	Handle string `json:"handle"`
}

// SessionGraphCall is the CallSessionGraphCall and CallSessionGraphAsync argument. Handle names the object whose method runs; a static function (`SessionManager.list`, `hasTrustRequiringProjectResources`) names its class in Kind instead.
type SessionGraphCall struct {
	Handle string          `json:"handle,omitempty"`
	Kind   string          `json:"kind,omitempty"`
	Method string          `json:"method"`
	Args   json.RawMessage `json:"args,omitempty"`
}

// SessionGraphUnknownMessage answers a call that names no live object of the connection.
const SessionGraphUnknownMessage = "Unknown or released session graph object"

// SessionGraphClosedMessage answers a call on a connection that closed.
const SessionGraphClosedMessage = "Extension connection is closed"

// SessionGraphUnavailableMessage answers a call on a host that serves no session graph.
const SessionGraphUnavailableMessage = "The host serves no session graph"

// RequestSessionGraphCallback (host→ext) runs a function an extension passed in an option of a session graph object (DefaultResourceLoader's systemPromptOverride, skillsOverride and the like). Args is a SessionGraphCallbackArgs; the response carries the function's result in Pi's JSON shape, and its error is the function's thrown error. The host sends it while a call of the object (reload) waits, so the extension answers it on another thread of control than the one that made the call.
const RequestSessionGraphCallback = "sessionGraph.callback"

// SessionGraphCallbackArgs is the RequestSessionGraphCallback argument. ID is the name the extension gave the function in the option that carried it.
type SessionGraphCallbackArgs struct {
	ID   string          `json:"id"`
	Args json.RawMessage `json:"args,omitempty"`
}
