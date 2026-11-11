package runtime

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/src/runtime/server.ts: locals, registries and natives the server host provides to plugins (Chord v2),
// and the server's core facet.
//
// Forced Go differences. Node's `IncomingMessage`/`ServerResponse`/`Duplex` are net/http's request and response writer
// (an upgrade handler hijacks the writer); a handler that may throw returns an error; `undefined` is nil. The server's
// natives are a struct of Go values, contributed through a single-native registry.

// ServerInfoSchema is what a server tells facets about itself.
var ServerInfoSchema = schema.Obj(schema.Properties{
	// The p2 app directory (`apps/p2`): its sources, docs and vendored files, for facets that start or describe them.
	schema.Prop("app", schema.Str()),
	schema.Prop("dataDir", schema.Str()),
	schema.Prop("fake", schema.Bool()),
	// Hosts reachable through a trusted HTTPS tunnel besides loopback (`P2_ALLOWED_HOSTS`).
	schema.Prop("allowedHosts", schema.Arr(schema.Str())),
	// The listening port.
	schema.Prop("port", schema.Num()),
})

// ServerInfo is the server's start data (`Server.info`).
type ServerInfo struct {
	App          string
	DataDir      string
	Fake         bool
	AllowedHosts []string
	Port         int
}

func (info ServerInfo) json() *delta.JsonObject {
	hosts := make([]any, len(info.AllowedHosts))
	for i, each := range info.AllowedHosts {
		hosts[i] = each
	}
	return delta.JsonObjectOf("app", info.App, "dataDir", info.DataDir, "fake", info.Fake, "allowedHosts", hosts, "port", float64(info.Port))
}

// ServerLocal is the server process (lifetime "host", `Server` upstream): its configuration (constant), and revocation.
// `revokeGuests` ends every object of one guest's principals, here and in workers: their references end
// `forbidden/revoked`, and the server closes their sockets.
var ServerLocal = mustContract(contracts.Local("p2.server", contracts.LocalDeclaration{
	Version: 2,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("info", Contract(ServerInfoSchema)),
		// Ends remote owners' live links and objects (one device, or every remote owner).
		contracts.Mem("revokeOwners", contracts.NewMethod(Contract(schema.Obj(schema.Properties{schema.Opt("device", schema.Str())})))),
		contracts.Mem("revokeGuests", contracts.NewMethod(Contract(schema.Obj(schema.Properties{schema.Prop("id", schema.Str())})))),
	},
}))

// ServerResourceOptions are overrides of p2's Resources (tests only): CreateModels is a fixture provider.
type ServerResourceOptions struct {
	CreateModels func(credentials ai.CredentialStore, modelsStore ai.ModelsStore) (*ai.MutableModels, error)
}

// RelocationSteps are the lifecycle steps of a running worker that the server calls over its route (`WorkerBoundary`).
type RelocationSteps struct {
	// Prepare fences the worker; a non-empty refusal says why it cannot move.
	Prepare func(ctx context.Context) (refusal string, err error)
	Update  func(ctx context.Context) error
	Move    func(ctx context.Context) error
	Cancel  func(ctx context.Context) error
}

// SessionWorkerControl is the session workers (the server's `SessionWorkers`): their generations, and the lifecycle steps
// plugins run.
type SessionWorkerControl interface {
	// Generation is the generation of the session's latest worker (0: never started).
	Generation(session string) int
	// Running is the sessions whose worker is running now.
	Running() []string
	// Relocate moves a session, serialized with starts, stops and pauses: a stopped worker only gets Update; a running
	// one is fenced (Prepare), updated, then follows in place (Move), restarted from the catalog when it cannot.
	Relocate(ctx context.Context, session string, steps RelocationSteps) error
	// Pause runs work while the session's worker is stopped (its store closed), restarting one that ran afterwards.
	Pause(ctx context.Context, session string, prepare func(ctx context.Context) (refusal string, err error), work func(ctx context.Context) error, doing string) error
	// Retry is the owner-requested retry: it stops the previous instance, clears the failure and starts the worker again,
	// resolving with the start's outcome (running, or failed with its redacted error).
	Retry(ctx context.Context, session string) (WorkerState, error)
	// Resume clears the session's failure or suspension (a restore): its holders start a worker, a later hold does too.
	// Nothing is started when nobody holds it.
	Resume(ctx context.Context, session string) error
	// Stop stops the session's worker; its holders start a new one (a restart, e.g. for a changed environment).
	Stop(ctx context.Context, session string) error
	// Suspend stops an archived session's worker and starts none for its holders: they wait (suspended) until Retry
	// (restore) or their release, instead of a start the archived session refuses.
	Suspend(ctx context.Context, session string) error
	// Subscribe calls listener with every change of a worker's state (the dev supervisor's events).
	Subscribe(listener func(HostEvent)) (unsubscribe func())
	// Observe follows every session worker's state (latest generation only) with a synchronous observer: Chord's
	// `self.follow` of `Hosts.workers`.
	Observe(observer host.FollowObserver[any], ctx context.Context) (*host.FollowAttachment[delta.JsonValue], error)
}

// ServerParts are the server's natives, contributed by the core facet from the start.
type ServerParts struct {
	ResourceOptions ServerResourceOptions
	Workers         SessionWorkerControl
	// Root runs fn in a root of the server's own (SERVER) under ctx: code that acts as the server rather than as a
	// method's caller. Inside a method pass chordctx.WithoutAbortSignal(ctx) (the call chain is kept; a caller giving up
	// does not cut off lifecycle steps); outside Chord calls pass the context at hand.
	Root func(fn func(ctx host.Ctx) (any, error), ctx context.Context) (any, error)
}

// ServerNatives is the registry of the server's natives: one contribution, the core's.
var ServerNatives = singleNative[ServerParts]("p2.server-natives")

// singleNative declares a single-native registry: its value is the first contribution, nil while there is none.
func singleNative[V any](name string) registries.Registry[any, any] {
	registry, err := registries.DefineRegistry[any, any](name, registries.DefineRegistryOptions{Version: 1})
	if err != nil {
		panic(err)
	}
	return registry
}

// serveNative serves registry (declared by singleNative of V) on facet: its composed value is the first contribution.
func serveNative[V any](facet host.FacetHost, registry registries.Registry[any, any]) error {
	return facet.Serve(registry, registries.RegistryProvider[*V, *V]{
		Compose: func(table registries.RegistryTable[*V], _ registries.ComposeContext) (*V, error) {
			if len(table) == 0 {
				return nil, nil
			}
			return table[0].Value, nil
		},
	}.Erase(), host.ServeOptions{})
}

// nativeOf reads a single-native registry's value at each use.
func nativeOf[V any](source *registries.RegistryValueSource, what string) func() (*V, error) {
	return func() (*V, error) {
		parts, _ := source.Value().(*V)
		if parts == nil {
			return nil, &missingNativeError{what: what}
		}
		return parts, nil
	}
}

type missingNativeError struct{ what string }

func (err *missingNativeError) Error() string { return err.what }

// ServerOf is the server's natives for a factory (the core contributes them before anything activates): it declares the
// dependency and returns a reader of the natives.
func ServerOf(self host.Self) func() (*ServerParts, error) {
	return nativeOf[ServerParts](self.Registry(ServerNatives), "No server natives (the core facet contributes them)")
}

// ServeServerNatives serves the natives registry (the core facet; test hosts).
func ServeServerNatives(facet host.FacetHost) error {
	return serveNative[ServerParts](facet, ServerNatives)
}

// ContributeServerNatives contributes parts from a facet (the core facet; test hosts).
func ContributeServerNatives(self host.FacetSelf, parts *ServerParts) error {
	return self.Contribute(ServerNatives, parts)
}

// RequestOrigin is where a request came from: loopback (`http://127.0.0.1:<port>`), or an allowed tunnel host (always
// https).
type RequestOrigin struct {
	Origin string
	Remote bool
	Port   int
	// RelayShellOrigin is the Radius shell origin allowed to frame the UI (relayed requests only); everything else may not
	// frame it.
	RelayShellOrigin string
	// RelayUser is a signed-in Radius user other than the owner (relayed requests only). Radius verified who they are and
	// admitted them because a session is shared with their email; authenticators map them to what is shared with them now.
	RelayUser *RelayUser
}

// RelayUser is a person Radius authenticated: their Radius user ID and normalized, verified email.
type RelayUser struct{ ID, Email string }

// RequestAccess is who sent an HTTP request (nil Principal: nobody authenticated, e.g. a guest before joining) and from
// where.
type RequestAccess struct {
	Principal Principal
	Origin    RequestOrigin
}

// RequestResolver lets trusted transports authenticate a marked loopback request as remote, before ordinary Host checks.
// Without a principal, the authenticators decide who the request is (a relayed Radius user: see RequestOrigin.RelayUser).
// nil: not this resolver's request. An error fails the request, as a throwing resolver does in Pi.
type RequestResolver func(request *http.Request, ctx host.Ctx) (*RequestAccess, error)

// RequestResolvers is the registry of request resolvers.
var RequestResolvers = NewKeyedRegistry[RequestResolver]("p2.request-resolvers")

// RouteHandler handles HTTP requests by `METHOD /path`, or `METHOD /prefix/*` for every path below a prefix (the longest
// match wins). The server dispatches each request in a root of its code version; handlers authorize the request
// themselves.
type RouteHandler func(response http.ResponseWriter, request *http.Request, access RequestAccess, ctx host.Ctx) error

// Routes is the registry of HTTP routes.
var Routes = NewKeyedRegistry[RouteHandler]("p2.routes")

// PageCookie gives `Set-Cookie` values for an owner's app page load (`/`, `/s/...`); "" for none. Example: the devices
// plugin renews a local owner session, so a browser in use stays signed in.
type PageCookie func(request *http.Request, access RequestAccess) string

// PageCookies is the registry of page cookies.
var PageCookies = NewKeyedRegistry[PageCookie]("p2.page-cookies")

// UpgradeHandler takes an authorized WebSocket upgrade by exact path (it hijacks the response writer). Plugins own their
// byte channels, not the server host.
type UpgradeHandler func(response http.ResponseWriter, request *http.Request, principal Principal, ctx host.Ctx) error

// Upgrades is the registry of upgrade handlers.
var Upgrades = NewKeyedRegistry[UpgradeHandler]("p2.upgrades")

// FindRoute is the handler for method and path: exact, then `/a/b/*`, `/a/*`, `/*`.
func FindRoute(table *KeyedTable[RouteHandler], method, path string) (RouteHandler, bool) {
	if handler, ok := table.Get(method + " " + path); ok {
		return handler, true
	}
	segments := strings.Split(path, "/")
	for end := len(segments) - 1; end >= 1; end-- {
		if handler, ok := table.Get(method + " " + strings.Join(segments[:end], "/") + "/*"); ok {
			return handler, true
		}
	}
	return nil, false
}

// Authenticator authenticates requests (loopback and tunnel hosts): an owner returned wins, else the first principal
// returned. Example: the sharing plugin maps a guest cookie to its current grant. nil: not authenticated.
type Authenticator func(request *http.Request, origin RequestOrigin, ctx host.Ctx) (Principal, error)

// Authenticators is the registry of authenticators.
var Authenticators = NewKeyedRegistry[Authenticator]("p2.authenticators")

// ShareURL is where people a session is shared with by email open it, through a transport that authenticates them (the
// brain's page in the Radius shell); false while unknown. Without one, sharing with people is unavailable.
type ShareURL func(session string) (string, bool)

// ShareUrls is the registry of share URLs.
var ShareUrls = NewKeyedRegistry[ShareURL]("p2.share-urls")

// SessionAuthorizer says whether a principal other than the owner may reach a session (its worker host, its files): any
// entry saying yes admits. Example: the sharing plugin admits a guest of that session whose grant is current.
type SessionAuthorizer func(principal Principal, session string, ctx host.Ctx) (bool, error)

// SessionAccess is the registry of session authorizers.
var SessionAccess = NewKeyedRegistry[SessionAuthorizer]("p2.session-access")

// MayReach is whether the owner, the server's own code, or a principal some SessionAccess entry admits may reach session.
func MayReach(access *KeyedTable[SessionAuthorizer], principal Principal, session string, ctx host.Ctx) (bool, error) {
	switch principal.(type) {
	case OwnerPrincipal, HostPrincipal, ServerPrincipal:
		return true, nil
	case GuestPrincipal:
	default:
		return false, nil
	}
	for _, key := range access.Keys() {
		authorize, _ := access.Get(key)
		admitted, err := authorize(principal, session, ctx)
		if err != nil {
			return false, err
		}
		if admitted {
			return true, nil
		}
	}
	return false, nil
}

// WorkerInitContribution is a contribution to a worker's spawn parameters (`WorkerInit.values[key]`). Each runs before a
// session worker starts; an error refuses the start (e.g. an archived or unknown session).
type WorkerInitContribution func(session string, ctx context.Context) (delta.JsonValue, error)

// WorkerInitValues is the registry of worker init contributions.
var WorkerInitValues = NewKeyedRegistry[WorkerInitContribution]("p2.worker-init")

// WorkerStateSchema is a session worker's state.
var WorkerStateSchema = schema.Obj(schema.Properties{
	schema.Prop("running", schema.Bool()),
	schema.Prop("generation", schema.Num()),
	schema.Opt("pid", schema.Num()),
	// A failed start is sticky until an explicit retry.
	schema.Opt("error", schema.Str()),
})

// WorkerState is a session worker's state (WorkerStateSchema).
type WorkerState struct {
	Running    bool
	Generation int
	Pid        *int
	Error      *string
}

// JSON is the state as the Hosts member shows it.
func (state WorkerState) JSON() *delta.JsonObject {
	object := delta.JsonObjectOf("running", state.Running, "generation", float64(state.Generation))
	if state.Pid != nil {
		object.Set("pid", float64(*state.Pid))
	}
	if state.Error != nil {
		object.Set("error", *state.Error)
	}
	return object
}

// HostEvent is a change of a worker's state.
type HostEvent struct {
	Session string
	WorkerState
}

// Hosts is the session worker processes: which run (by session ID), and restarting one for its holders (`stop`).
var Hosts = mustContract(contracts.Local("p2.hosts", contracts.LocalDeclaration{
	Version: 2,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("workers", Contract(Record(WorkerStateSchema))),
		contracts.Mem("stop", contracts.NewMethod(Contract(schema.Obj(schema.Properties{schema.Prop("session", schema.Str())})))),
	},
}))

func serverKeyedRegistries() []interface{ Serve(host.FacetHost) error } {
	return []interface{ Serve(host.FacetHost) error }{
		RequestResolvers, Routes, PageCookies, Upgrades, Authenticators, ShareUrls, SessionAccess, WorkerInitValues,
	}
}

// ServeServerRegistries serves the base keyed registries of the server (the core facet; test hosts without one).
func ServeServerRegistries(facet host.FacetHost) error {
	for _, registry := range serverKeyedRegistries() {
		if err := registry.Serve(facet); err != nil {
			return err
		}
	}
	return nil
}

func methodOf(fn func(args *delta.JsonObject, ctx context.Context) error) contracts.MethodFunction {
	return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
		object, _ := args.(*delta.JsonObject)
		return nil, fn(object, ctx)
	}
}

// ServerCore is the server's core facet: its start data and revocation (ServerLocal), its natives, the base registries,
// the workers' states (Hosts) and who the caller is (Access).
func ServerCore(h host.Host, info ServerInfo, parts *ServerParts) host.Facet {
	return host.DefineFacet(func(facet host.FacetHost, self host.FacetSelf) error {
		if err := ServeServerRegistries(facet); err != nil {
			return err
		}
		if err := ServeServerNatives(facet); err != nil {
			return err
		}
		if err := ContributeServerNatives(self, parts); err != nil {
			return err
		}
		if err := facet.Serve(ServerLocal, host.LocalFactory(func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"info": constant(info.json())},
				Methods: map[string]contracts.MethodFunction{
					"revokeOwners": methodOf(func(args *delta.JsonObject, ctx context.Context) error {
						if device, ok := args.Value("device").(string); ok {
							return h.Revoke(RevokeDevice(device), ctx)
						}
						if err := h.Revoke(RevokeRemoteOwners(), ctx); err != nil {
							return err
						}
						return h.Revoke(RevokeLocalOwners(), ctx)
					}),
					"revokeGuests": methodOf(func(args *delta.JsonObject, ctx context.Context) error {
						id, _ := args.Value("id").(string)
						return h.Revoke(RevokeGuest(id), ctx)
					}),
				},
			}}, nil
		}), host.ServeOptions{}); err != nil {
			return err
		}
		if err := facet.Serve(Hosts, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			// The workers component's state, followed: never copied into a document of this object.
			workers := self.Follow(host.FollowAttach[delta.JsonValue](parts.Workers.Observe))
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"workers": workers},
				Methods: map[string]contracts.MethodFunction{
					"stop": methodOf(func(args *delta.JsonObject, ctx context.Context) error {
						session, _ := args.Value("session").(string)
						// Stopping waits for the worker's exit, which the server loop must be free to deliver (this handler would
						// otherwise be a synchronous block of the section that admits its call).
						chordctx.Suspend(ctx)
						return parts.Workers.Stop(ctx, session)
					}),
				},
			}}, nil
		}), host.ServeOptions{}); err != nil {
			return err
		}
		return facet.Serve(Access, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, _ host.Self) (contracts.Implementation, error) {
			who, err := FromChord(principal)
			if err != nil {
				return contracts.Implementation{}, Forbidden("owners and guests only")
			}
			switch typed := who.(type) {
			case OwnerPrincipal:
				return contracts.Implementation{Variant: "owner"}, nil
			case GuestPrincipal:
				// One object per grant revision: the grant never changes within it.
				sessions := delta.NewJsonObject(len(typed.Sessions))
				for _, id := range sortedKeys(typed.Sessions) {
					sessions.Set(id, string(typed.Sessions[id]))
				}
				mode := string(typed.Sessions[typed.Home])
				if mode == "" {
					mode = string(GuestView)
				}
				grant := delta.JsonObjectOf("id", typed.ID, "name", typed.Name, "session", typed.Home, "mode", mode, "sessions", sessions)
				if typed.Email != nil {
					grant.Set("email", *typed.Email)
				}
				return contracts.Implementation{Variant: "guest", Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"grant": constant(grant)},
				}}, nil
			}
			return contracts.Implementation{}, Forbidden("owners and guests only")
		}), host.ServeOptions{})
	})
}

// constant is Chord's `constant(value)` as a state member.
func constant(value delta.JsonValue) chord.Watchable[delta.JsonValue] {
	return host.Expose(state.NewConstant(value))
}

// NoWorkers is a test server's workers: none run, and none can be controlled.
type NoWorkers struct{}

var _ SessionWorkerControl = NoWorkers{}

var errNoWorkers = errors.New("no workers in a test host")

// Generation is 0: no worker ever started.
func (NoWorkers) Generation(string) int { return 0 }

// Running is none.
func (NoWorkers) Running() []string { return nil }

// Relocate fails.
func (NoWorkers) Relocate(context.Context, string, RelocationSteps) error { return errNoWorkers }

// Pause fails.
func (NoWorkers) Pause(context.Context, string, func(context.Context) (string, error), func(context.Context) error, string) error {
	return errNoWorkers
}

// Retry answers a worker that does not run, with the reason.
func (NoWorkers) Retry(context.Context, string) (WorkerState, error) {
	reason := errNoWorkers.Error()
	return WorkerState{Running: false, Generation: 0, Error: &reason}, nil
}

// Resume does nothing.
func (NoWorkers) Resume(context.Context, string) error { return nil }

// Stop does nothing.
func (NoWorkers) Stop(context.Context, string) error { return nil }

// Suspend does nothing.
func (NoWorkers) Suspend(context.Context, string) error { return nil }

// Subscribe never calls listener.
func (NoWorkers) Subscribe(func(HostEvent)) func() { return func() {} }

// Observe follows an empty record.
func (NoWorkers) Observe(host.FollowObserver[any], context.Context) (*host.FollowAttachment[delta.JsonValue], error) {
	return &host.FollowAttachment[delta.JsonValue]{Value: delta.NewJsonObject(0), Detach: func() {}}, nil
}
