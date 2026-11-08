package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	chordtelemetry "github.com/MichaelKinsy/PiG/chord/next/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next/host/host.ts: hosts (§3). `createHost`, loading facets and the start barrier, links to
// children, the parent (connected again after a drop) and routes, the origins it relays holds for, getting and holding
// references as a principal, the heartbeat of every link, revocation (§4), presence (§19) and keep-alive counts, registries
// and root scopes (slice 8), closing; one batch per host, the backoff map and the endings new instances wait for.
//
// Forced Go differences. The type parameters `P` (principal) and `R` (revocation) are type-level (owner class a): a
// principal is a data.Principal, a revocation a delta.JsonValue. An async method is a blocking call returning (value,
// error); every synchronous part of it runs as a section of the host's loop (enter), which also holds the batch's delivery
// scheduler, as one outermost section is one frame. Promises the host stores and several parties await (`#closing`, the
// endings, the publication) are once-only done-channel promises. `setInterval` is a timer re-armed by its own callback.

// HostOptions is what CreateHost takes.
type HostOptions struct {
	// Address is this host's address (`Session({ id })`): it serves only objects placed there. Without one, a client.
	Address *contracts.Address
	// Principal is who this host acts as for its own requests; a host without one (a client) has none.
	Principal data.Principal
	// PrincipalKey is the key of a principal in identities; L1's canonical JSON by default. The same function on every
	// host.
	PrincipalKey *data.PrincipalKeyFunction
	// Telemetry is the telemetry adapter; none is the no-op one.
	Telemetry telemetry.TelemetryContext
	// PrincipalAttributes are the span attributes recorded for a principal: synchronous and cheap; caching, if any, is
	// the hook's own. Without it a span records `chord.principal`, the principal's key. The same function on every host.
	PrincipalAttributes PrincipalAttributes
	// OnError is everything that fails in the background; every app handles it.
	OnError func(err HostError)
	// CloseTimeout is how long a native's close, and an ending object's running calls, are waited for; 30 s by default.
	CloseTimeout *time.Duration
	// ReadyTimeout is how long an activation attempt, and a `get`, may take until the object is live; 60 s by default.
	ReadyTimeout *time.Duration
	// ReloadDrainTimeout is how long a reload's restart keeps its old attempt open for old work (its calls, routed calls, its
	// successor's requests becoming ready): at most CloseTimeout, 5 s by default. Work still running then is cut (`released`).
	ReloadDrainTimeout *time.Duration
	// Heartbeat is the heartbeat of every link.
	Heartbeat *HeartbeatOptions
	// RevokedBy is what revokes a principal (§4): `host.Revoke(r)` ends the objects and closes the child links of every
	// principal `p` whose RevokedBy(p) holds `r` (compared as canonical JSON). The same function on every host.
	RevokedBy RevokedBy[delta.JsonValue]
	// Compression is a synchronous compressor (`nodeCompression` for Node): JSON parts above 16 KiB this host originates
	// are deflated once, and deflated parts it owns inflated in place. Without one (a browser), nothing is compressed and
	// deflated parts are inflated asynchronously, later messages of their link waiting in order.
	Compression link.Compression
	// Store is the store persisted documents and tables live in (`openStore`); the app owns it and closes it after the
	// host. One open host per store. A `storage_failed` of the store is reported once and closes the host.
	Store store.Store

	// loop is the host's event loop; tests give one with virtual timers. NewLoop by default.
	loop sectionLoop
}

// HeartbeatOptions is the heartbeat of every link: a link silent for Ping (15 s by default) sends a `ping`; one silent for
// Close (30 s by default), its handshake included, is closed.
type HeartbeatOptions struct {
	Ping  *time.Duration
	Close *time.Duration
}

// LinkOptions is what Accept and Connect take: the principal of the host at the other end, as this host stamps it;
// Trusted skips the message check on the link (IPC to the host's own processes; never grants the principal fields); the
// Codec both ends of the link use (link.BinaryCodec by default); and the Limits per part of what arrives on it (Q2: none
// on a trusted link, 16 MiB on the wire and 64 MiB decoded otherwise, unless set).
type LinkOptions struct {
	Principal data.Principal
	Trusted   bool
	Codec     link.Codec
	Limits    link.PartLimits
}

// PrincipalAttributes are the span attributes recorded for a principal (`HostOptions.principalAttributes`): none, its key,
// or more.
type PrincipalAttributes func(principal data.Principal) telemetry.SpanAttributes

// HostFacet is a plugin's facet as `Load` and `Reload` take it: its name (its `package.json` name) and its facet. Pi's
// `PluginFacet`.
type HostFacet struct {
	Name  string
	Facet Facet
}

// PluginFailure is a plugin left out at `Load`, or whose new code failed at a reload (it kept its old code, if it had
// any), and why.
type PluginFailure struct {
	Plugin string
	Error  error
}

// ReloadOptions is what `Reload` takes besides the plugin set: the plugins whose new code failed before it (a build, an
// evaluation).
type ReloadOptions struct {
	Failed []PluginFailure
}

// LoadReport is what `Load` did (decision "Per plugin at startup and reload"): the plugins it left out.
type LoadReport struct {
	Failed []PluginFailure
}

// PluginChanges are a reload's plugin names by change.
type PluginChanges struct {
	Added   []string
	Changed []string
	Removed []string
}

// ReloadedObjectKind is one object's fate in a reload.
type ReloadedObjectKind string

const (
	// ReloadRestarted: its old attempt drained, sealed and closed with reason `reload`, a new attempt on the new code.
	ReloadRestarted ReloadedObjectKind = "restarted"
	// ReloadEnded: its type is gone.
	ReloadEnded ReloadedObjectKind = "ended"
	// ReloadParked: its new code waits for a failed dependency (Cause).
	ReloadParked ReloadedObjectKind = "parked"
	// ReloadRetried: it was failed, parked or activating: a new attempt runs on the new code.
	ReloadRetried ReloadedObjectKind = "retried"
)

// ReloadedObject is one object's fate in a reload, by its label; Cause is set only for ReloadParked.
type ReloadedObject struct {
	Object string
	Kind   ReloadedObjectKind
	Cause  data.FailureCause
}

// ReloadReport is what a reload did (slice 7b section 1): its plugins by change (those applied), the plugins whose new
// code failed, and what every affected object did (every live, activating, parked or failed object of a changed or removed
// plugin, by its label).
type ReloadReport struct {
	Plugins PluginChanges
	Failed  []PluginFailure
	Objects []ReloadedObject
}

// CommittedReload is what a reload's commit applied, by plugin name: the `details.committed` of the `aborted` error of a
// reload whose context aborted after its commit (the commit stands; only the wait for the report ended).
type CommittedReload struct {
	Added   []string
	Changed []string
	Removed []string
	// Failed are the plugins whose new code failed: they kept their old code (an added one was left out).
	Failed []string
}

// Host is a host.
type Host interface {
	// Incarnation is this run of the host, sent in every handshake (R20): a link's peer sees it as its calls'
	// `ctx.Incarnation()` (Pi's `readonly incarnation`).
	Incarnation() string
	// PrincipalAttributes are the attributes this host's spans record for principal: its PrincipalAttributes hook's, else
	// `chord.principal`, its principal key; none without a principal (Pi's `principalAttributes(principal, context)`).
	PrincipalAttributes(principal data.Principal, ctx context.Context) telemetry.SpanAttributes
	// Load runs every facet's setup and publishes what they declared in one batch (the types and registries served, the
	// registries composed once, the contributions); requests that waited for the publication are placed after that batch.
	// Then activates every served type with empty params and returns when each has had its first outcome (the start
	// barrier, decision (a)): live, failed by its own cause, parked with no blocker here still without its first outcome,
	// or ended (failures go to `OnError`). A plugin whose setup fails or is refused at declaration (its name, a type or
	// registry another plugin serves, a remote type here without principal or address, `checkAccepts`) is left out and
	// reported (LoadReport.Failed); it never closes the host. Runs once.
	Load(facets []HostFacet, ctx context.Context) (LoadReport, error)
	// Reload reloads this host's plugins (slice 7b): facets is its complete new plugin set; a plugin in it and not loaded
	// is added, one loaded with another Facet (another DefineFacet) is changed (the same Facet, as Pi's `===`, is
	// unchanged: untouched; every copy of a DefineFacet's value is the same), one loaded and not in it is removed.
	// Staging runs every new setup and
	// every affected object's new factory against a recording `self`, per plugin: a plugin whose new code fails there keeps
	// its old code (an added one is left out), and so does a changed plugin whose recorded requests would not bind because
	// of it (the closure rule); both are reported (ReloadReport.Failed); options.Failed names plugins the caller passes as
	// their loaded facet because their new code failed before (a build, an evaluation): the same for the closure rule and
	// the report. Then it commits in one batch (types, registries, contributions; a publication waking the requests for
	// the names it serves) and every object of a changed or removed plugin takes its fate: restarted (its old attempt
	// finishes its old work, then a new attempt runs on the new code), retried, or ended (its type removed). Returns the
	// report once every restart started and every retry checked its dependencies; it does not wait for objects to go
	// live. Reloads run one after another. It fails before
	// `Load` returned; fails `released/closed` after `Close`; ctx's abort before the commit fails `aborted` and changes
	// nothing, after it only ends the wait (`aborted`, its details.committed naming what the commit applied:
	// CommittedReload).
	Reload(facets []HostFacet, ctx context.Context, options ReloadOptions) (ReloadReport, error)
	// Get is a reference once its object is live (and hydrated). It acts as the host's principal and goes where its type's
	// host address puts it: here, over a route, or over the parent link (`destination`). It waits while the reference is
	// `loading` (before the host's publication too), `failed { transient }` or `failed { waiting }` until its
	// `ReadyTimeout`, then returns the last cause and releases the reference; a broken failure returns at once.
	Get(ctx context.Context, typ *contracts.Contract, params any, options GetOptions) (*Reference, error)
	// Hold is a reference at once, following its object until released: loading, live, failed (retried as its object is)
	// and loading again while its link is down; it never fails. It returns an error at once only for invalid params (also
	// before `Load`). A type not served where it goes is `loading` before the host's publication and `failed { waiting }`
	// after it (bound when a publication serves it); a `hostAddress` or route that refuses fails it broken.
	Hold(ctx context.Context, typ *contracts.Contract, params any, options GetOptions) (*Reference, error)
	// Accept is a link from a child: this host serves its remote types over it, every hold and call arriving on it acting
	// as options.Principal. ctx is the parent of the work arriving on it (its cancellation is dropped).
	Accept(link link.Link, options LinkOptions, ctx context.Context) error
	// Connect is this host's parent link: remote types not served here and not routed are held over it, every such
	// reference held again on a new one; holds arriving on it may name whom they act for. It fails while the previous one
	// is open; once it closed (a drop), Connect takes the next. options.Principal is who the parent is.
	Connect(link link.Link, options LinkOptions, ctx context.Context) error
	// Route is how this host reaches hosts of kind (called before `Load`, once per kind): Authorize admits every hold
	// routed over it, Resolve makes the link of an address. Holds for an address wait while it resolves and again after
	// its link drops once; a rejected Resolve fails its holders `failed { waiting }` (they stay on its path), with no timer.
	// ctx is the parent of the route's work, aborted for it when this host closes. It returns the RouteHandle: the app's
	// controls of the route's addresses.
	Route(kind *contracts.HostKind, options RouteOptions, ctx context.Context) (RouteHandle, error)
	// Delegate is the route of this host's own address kind (kind must be it; called before `Load`, once): the remote types
	// placed on this host that it does not serve itself go over it, as a route's holds do (Authorize admits them, Resolve makes
	// the link, which is re-resolved once after a drop; a rejected Resolve fails its holders `failed { waiting }`), so the host
	// serves the union of its own types and those the delegate serves, as one host. What this host serves stays here: the
	// decision is taken at placement by the published table, not by an object's liveness, so a restarting or parked type never
	// moves to the delegate. A hold the delegate's own link asks for and no host serves is not sent back to it
	// (`unavailable`, not served here). pig divergence (D605): Pi's server runs its plugins in its own host; the Go kit's plugins
	// run in a Node subprocess behind this host.
	Delegate(kind *contracts.HostKind, options RouteOptions, ctx context.Context) (RouteHandle, error)
	// Serves is whether this host serves a type under name (its published table, by which Delegate decides: a restarting or
	// parked type is still served). The delegate's host asks it to hand this host the types placed on its own address that
	// it does not serve itself (D605: the delegation goes both ways, as Pi's one host serves the union).
	Serves(name string) bool
	// Revoke revokes revocation (§4, R10, R11), synchronously: every remote object whose principal it revokes (`RevokedBy`)
	// ends at once, whatever references remain (holders get `forbidden`, running calls are aborted, an activation is
	// abandoned); every child link whose principal it revokes closes (never the parent, never this host's own principal);
	// and every route link is sent `revoke`, which the host there applies with its own `RevokedBy`. Nothing is replayed to
	// links made later.
	Revoke(revocation delta.JsonValue, ctx context.Context) error
	// References is the references to objects of typ on this host (§19), one entry per reference: the object's params, the
	// principal it acts as (remote types) and its origin, the incarnation of the host it was made for (two tabs are two
	// origins). Changes in the batch where a reference binds or ends; a link drop is a departure.
	References(typ *contracts.Contract) state.SourceHandle
	// Activity is the work keeping this host busy, for the app's supervisor: holds arriving over links and served here,
	// method calls running, running jobs and lingering objects; published at most once per microtask turn with changes.
	Activity() state.SourceHandle
	// Registry is registry r's composed value as r's version sees it, for code outside objects, once it is usable: waits for
	// the host's publication, then while it has no value (`unavailable/undeclared` without a provider, a first `compose`
	// that failed, a version not served here: `contract_mismatch`) until its `ReadyTimeout`, then returns the last error.
	Registry(ctx context.Context, r registries.Registry[any, any], options GetOptions) (*registries.RegistryValueSource, error)
	// Address is this host's address (Pi's `host.address`); nil for a client.
	Address() *contracts.Address
	// Root runs fn with a call context for code outside objects (a Durable tool, a CLI): the host's principal (or
	// options.Principal), this host's incarnation, ctx's telemetry and signal, `ctx.Get`. When fn settles or ctx aborts, the
	// context is sealed: its signal aborted, `ctx.Get` refused `released`, every reference it took released; an abort returns
	// at once. `host.Close` seals the open ones; after it, Root returns `released`.
	Root(fn func(ctx Ctx) (any, error), ctx context.Context, options RootOptions) (any, error)
	// Close closes its links, seals its root scopes, then ends every object at once, then drains and closes them one by one,
	// the last to go live first (reason `close`). It returns an error joining the closes that failed.
	Close(ctx context.Context) error
}

// Serving is what a hold placed here binds to (`serving`): its Object, or why it waits unbound (Kind ServingWaiting,
// Cause: not served, another version, or placed elsewhere by the provider's code).
type Serving struct {
	Kind   ServingKind
	Object *chordObject
	Cause  data.FailureCause
}

// ServingKind tags a Serving.
type ServingKind string

const (
	// ServingObject: the hold binds to Object.
	ServingObject ServingKind = "object"
	// ServingWaiting: the hold waits unbound for Cause.
	ServingWaiting ServingKind = "waiting"
)

// CreateHost creates a host. It fails when the store is attached to another open host.
func CreateHost(options HostOptions) (Host, error) {
	return createHost(options)
}

// noChain is no activation chain (Q5).
var noChain []string

const (
	defaultCloseTimeout = 30 * time.Second
	defaultReadyTimeout = 60 * time.Second
	// defaultReloadDrainTimeout is the reload drain bound unless ReloadDrainTimeout says otherwise (at most CloseTimeout).
	defaultReloadDrainTimeout = 5 * time.Second
	defaultPing               = 15 * time.Second
	defaultHeartbeat          = 30 * time.Second
)

// hostState is where a host stands.
type hostState int

const (
	hostNew hostState = iota
	hostLoading
	hostLoaded
	hostClosed
)

// hostTimeouts are the host's `closeTimeout` and `readyTimeout` as defaulted.
type hostTimeouts struct {
	close time.Duration
	ready time.Duration
	// reload is the reload drain bound: at most close.
	reload time.Duration
}

// closePromise is the one closing, which every `Close` waits for: settled once.
type closePromise struct {
	done chan struct{}
	err  error
}

// endings are teardowns still running (Pi's `Promise<void>`): a keyedPromise[struct{}], settled once inside a section.

// relayMap is Pi's `Map<number, ProviderSide>`: the downstream origins this host relays holds for, in insertion order.
type relayMap struct {
	order []int
	byKey map[int]*providerSide
}

func (m *relayMap) get(origin int) (*providerSide, bool) {
	side, ok := m.byKey[origin]
	return side, ok
}

func (m *relayMap) set(origin int, side *providerSide) {
	if m.byKey == nil {
		m.byKey = map[int]*providerSide{}
	}
	if _, ok := m.byKey[origin]; !ok {
		m.order = append(m.order, origin)
	}
	m.byKey[origin] = side
}

func (m *relayMap) delete(origin int) {
	if _, ok := m.byKey[origin]; !ok {
		return
	}
	delete(m.byKey, origin)
	m.order = slices.DeleteFunc(m.order, func(each int) bool { return each == origin })
}

func (m *relayMap) values() []*providerSide {
	sides := make([]*providerSide, 0, len(m.order))
	for _, origin := range m.order {
		sides = append(sides, m.byKey[origin])
	}
	return sides
}

// heartbeatTimer is the host's one heartbeat timer (Pi's `setInterval`), running while the host has a link. Like Pi's, it is
// a real timer, whatever clock the host's loop has: vitest's `fakeTimers()` fakes `setTimeout` and `Date`, not
// `setInterval`; its ticks read the loop's clock. A loop of a test that fakes `setInterval` too (intervalLoop) drives it.
type heartbeatTimer struct {
	timer   Timer
	real    *dropTimer
	stopped bool
}

// intervalLoop is a loop that fakes `setInterval` as well as `setTimeout` (a test's `toFake` with "setInterval"): the host
// arms its heartbeat on it. ok false is a loop whose intervals are real.
type intervalLoop interface {
	AfterInterval(d time.Duration, job func()) (timer Timer, ok bool)
}

// chordHost is the host: its served types, its objects by identity, its batch, backoff and endings.
type chordHost struct {
	batch     *state.Batch
	events    sectionLoop
	scheduler *chord.DeliveryScheduler
	timeouts  hostTimeouts
	// oldWork is its old work during reloads (whether a context runs under it): see host_old_work.go.
	oldWork oldWork
	// reloadCycles are the waits of old work on objects that are not live, cut at once when they cycle through their own drain.
	reloadCycles reloadCycles
	backoff      *backoff
	address      *contracts.Address
	principal    data.Principal
	// telemetry is the telemetry adapter (root spans start on it, carriers from links are extracted with it), spanHost
	// the `chord.host` attribute of this host's spans, principalHook the options' PrincipalAttributes.
	telemetry telemetry.TelemetryContext
	// metrics are Chord's instruments on the adapter's metrics: the host's own counts (links, holds, calls, commits).
	metrics  *chordtelemetry.Instruments
	spanHost string
	// counting is the telemetry of this host's work under a context without a span (counted); nil without an adapter that
	// records.
	counting      telemetry.TelemetrySpan
	principalHook PrincipalAttributes
	// incarnation is this run of the host, sent in every handshake (R20).
	incarnation string
	compression link.Compression
	// store is the host's store, if it has one.
	store *store.ChordStore
	// unplaced are remote references failed for a part past a link's limits: on no link's path (no reconnect holds them,
	// no route resolves for them) until `ref.retry()` places them anew; ended at `close` like the others.
	unplaced *path
	// relays are the downstream origins this host relays holds for, by their number on upstream links (never reused).
	relays *relayMap

	activity       *activity
	principalKey   *data.PrincipalKeyFunction
	revocationKeys *revocationKeys
	// refs is the presence list of each type someone asked about (`references`), by type name.
	refs    map[string]*references
	onError func(err HostError)
	// types are the served types by name: the published plugin set's (a reload's commit replaces it).
	types *namedMap[*served]
	// plugins are the published plugins by name: their facet (an unchanged one is the same), staged setup and contributions.
	plugins *namedMap[*loadedPlugin]
	// reloading is the last reload's end, which the next one waits for (slice 7b section 1); nil before the first.
	reloading *keyedPromise[struct{}]
	// objects are by identity, in the order they went live (the others where they were created).
	objects     map[string]*chordObject
	objectOrder []string
	// endings are teardowns still running, by identity: the next instance of an identity waits for its previous ones.
	endings map[string]*keyedPromise[struct{}]
	// unbound are the requests that cannot bind yet, by name: before the publication all of them, then those left unbound.
	unbound *unboundIndex
	// clock reports the requests waiting unbound after the publication (in the index, and registry readers) after 5 s.
	clock *unboundClock
	// publication is closed at the host's first publication (`load`'s batch), or its close.
	publication   chan struct{}
	publishedOnce bool
	published_    bool
	// links is every open link: children, routes and the parent.
	links []*hostLink
	// parent is where holds go that are not served here and not routed: the parent link, across reconnects.
	parent      *path
	routes      map[string]*route
	routeKinds  []string
	destination *destination
	nextOrigin  int
	// routing is aborted when the host closes: the context of every route's `resolve`, and the end of waiting
	// `host.registry`s.
	routing       context.Context
	routingCancel func(cause error)
	// routeCancels abort the contexts of the routes' work when the host closes.
	routeCancels  []func(cause error)
	heartbeat     struct{ ping, close time.Duration }
	registries    map[string]*registries.RegistryRecord
	registryOrder []string
	roots         *rootScopes
	// ticking is the one heartbeat timer.
	ticking *heartbeatTimer
	state   hostState
	// closer is the one closing, which every `close` returns.
	closer *closePromise
}

var _ Host = (*chordHost)(nil)

func createHost(options HostOptions) (*chordHost, error) {
	var adapter telemetry.TelemetryContext = telemetry.NoopTelemetryContext
	// A nil pointer in the interface (an unset adapter field) is none.
	if value := reflect.ValueOf(options.Telemetry); options.Telemetry != nil && (value.Kind() != reflect.Pointer || !value.IsNil()) {
		adapter = options.Telemetry
	}
	events := options.loop
	if events == nil {
		events = NewLoop()
	}
	h := &chordHost{
		events:        events,
		scheduler:     chord.NewDeliveryScheduler(),
		address:       options.Address,
		principal:     options.Principal,
		principalKey:  options.PrincipalKey,
		telemetry:     adapter,
		spanHost:      chordtelemetry.HostAttribute(options.Address),
		principalHook: options.PrincipalAttributes,
		onError:       options.OnError,
		incarnation:   uuidv7(),
		compression:   options.Compression,
		refs:          map[string]*references{},
		types:         newNamedMap[*served](),
		plugins:       newNamedMap[*loadedPlugin](),
		objects:       map[string]*chordObject{},
		endings:       map[string]*keyedPromise[struct{}]{},
		publication:   make(chan struct{}),
		relays:        &relayMap{},
		routes:        map[string]*route{},
		registries:    map[string]*registries.RegistryRecord{},
		nextOrigin:    1,
	}
	var metrics telemetry.TelemetryMetrics
	if options.Telemetry != nil {
		metrics = options.Telemetry.Metrics()
	}
	h.clock = newUnboundClock(events, func(long unboundLong) {
		h.report(HostError{Kind: ErrUnboundLong, WaitingFor: long.waitingFor, Error: long.err, Since: long.since})
	})
	h.unbound = newUnboundIndex(h.clock)
	h.batch = state.NewBatch(func(err state.BatchError) { h.report(BatchHostError(err)) }, metrics).
		UseScheduler(h.scheduler).UseMicrotasks(events.Queue).UseSection(events.Run, onLoopOf(events))
	h.metrics = chordtelemetry.Metrics(metrics)
	if !chordtelemetry.Untraced(adapter) && metrics != nil {
		h.counting = chordtelemetry.CountingOnly(metrics)
	}
	h.activity = newActivity(events, h.batch)
	h.backoff = newBackoff(events, h.due)
	// `revokedBy` is called with this host's principals only, the ones its links stamp.
	h.revocationKeys = newRevocationKeys(options.RevokedBy.erased())
	closeTimeout := durationOr(options.CloseTimeout, defaultCloseTimeout)
	h.timeouts = hostTimeouts{close: closeTimeout, ready: durationOr(options.ReadyTimeout, defaultReadyTimeout), reload: min(closeTimeout, durationOr(options.ReloadDrainTimeout, defaultReloadDrainTimeout))}
	h.heartbeat.ping, h.heartbeat.close = defaultPing, defaultHeartbeat
	if options.Heartbeat != nil {
		h.heartbeat.ping = durationOr(options.Heartbeat.Ping, defaultPing)
		h.heartbeat.close = durationOr(options.Heartbeat.Close, defaultHeartbeat)
	}
	h.routing, h.routingCancel = chordctx.WithCancel(chordctx.Background())
	h.parent = newPath(func(pathEvent) {})
	h.unplaced = newPath(func(pathEvent) {})
	h.destination = newDestination(h.address, h.routes, h.parent, func(name string) bool { return h.served(name) != nil })
	h.roots = newRootScopes(h)
	if options.Store != nil {
		// Chord's own: `openStore` made it.
		chordStore, ok := options.Store.(*store.ChordStore)
		if !ok {
			return nil, fmt.Errorf("host: the store %T was not made by openStore", options.Store)
		}
		if err := chordStore.Attach(store.Attachment{Failed: h.storageFailed, Batch: h.batch}); err != nil {
			return nil, err
		}
		h.store = chordStore
	}
	return h, nil
}

// durationOr is Pi's `value ?? fallback` of a millisecond option.
func durationOr(value *time.Duration, fallback time.Duration) time.Duration {
	if value == nil {
		return fallback
	}
	return *value
}

// onLoopOf is how loop tells whether the caller runs one of its sections: nil for a loop that cannot tell (a test's
// virtual-time loop).
func onLoopOf(loop sectionLoop) func() bool {
	if inside, ok := loop.(interface{ onLoop() bool }); ok {
		return inside.onLoop
	}
	return nil
}

// loop is the host's event loop.
func (h *chordHost) loop() sectionLoop { return h.events }

// enter runs work as one section of the host's loop; the batch's delivery scheduler is held meanwhile, so everything an
// outermost section changes is one frame.
func (h *chordHost) enter(work func()) {
	h.events.Run(func() {
		release := h.scheduler.Hold()
		defer release()
		work()
	})
}

// Incarnation implements Host.
func (h *chordHost) Incarnation() string { return h.incarnation }

// Load implements Host.
func (h *chordHost) Load(facets []HostFacet, ctx context.Context) (_ LoadReport, loadErr error) {
	var failure error
	var report LoadReport
	// Its `chord.load` span under the caller's, when something records: the setups, the publication and the start barrier.
	var span *chordtelemetry.OpenSpan
	defer func() {
		if span == nil {
			return
		}
		if loadErr != nil {
			chordtelemetry.Ended(span.Span(), "load_failed", loadErr, ctx)
		} else {
			span.Span().SetAttributes(telemetry.SpanAttributes{telemetry.Attr("failed", float64(len(report.Failed)))})
		}
		span.End()
	}()
	var outcomes []*keyedPromise[struct{}]
	h.enter(func() {
		if failure = h.batch.AssertOutside("load"); failure != nil {
			return
		}
		if failure = h.assertOpen(); failure != nil {
			return
		}
		if h.state != hostNew {
			failure = errors.New("load runs once per host")
			return
		}
		if failure = checkPluginNames(facets); failure != nil {
			return
		}
		h.state = hostLoading
		if parent := chordctx.Telemetry(ctx); !chordtelemetry.Untraced(parent) {
			span = chordtelemetry.NewOpenSpan(parent, "chord.load", telemetry.SpanAttributes{
				telemetry.Attr("chord.host", h.spanHost),
				telemetry.Attr("plugins", float64(len(facets))),
			})
			ctx = chordctx.WithTelemetry(ctx, span.Span())
		}
		table := emptyStaged()
		loaded := newNamedMap[Facet]()
		// A name given twice is refused the second time whether or not the first staged.
		given := map[string]struct{}{}
		// In the given order: of two plugins serving one name, the later is left out.
		for _, plugin := range facets {
			_, twice := given[plugin.Name]
			given[plugin.Name] = struct{}{}
			var err error
			if twice {
				err = loadedTwice(plugin.Name)
			} else {
				err = h.stageInto(table, plugin)
			}
			if err == nil {
				loaded.set(plugin.Name, plugin.Facet)
			} else {
				report.Failed = append(report.Failed, PluginFailure{Plugin: plugin.Name, Error: err})
			}
		}
		h.types = table.types
		// The publication: the types and registries served, the facets' contributions and the first composition of every
		// served registry, in one batch, before any object activates.
		if failure = h.batch.Commit(func() error {
			if err := h.publishPlugins(table, loaded); err != nil {
				return err
			}
			for _, name := range h.registryOrder {
				if err := h.registries[name].ComposeServed(); err != nil {
					return err
				}
			}
			return nil
		}, ctx); failure != nil {
			return
		}
		// After the batch: what waited for the publication is placed (the gate lasted only for the setups).
		h.published_ = true
		h.publish()
		h.reportUndeclared()
		h.unbound.wakeAll()
		for _, name := range h.types.keys() {
			entry, _ := h.types.get(name)
			if entry.contract.Kind == contracts.KindLocal && entry.hostLifetime {
				outcomes = append(outcomes, h.objectFor(entry, emptyParams, nil, ctx, noChain, nil).firstOutcome())
			}
		}
	})
	if failure != nil {
		return LoadReport{}, failure
	}
	for _, outcome := range outcomes {
		_, _ = outcome.await(ctx)
	}
	h.enter(func() {
		if h.state == hostLoading {
			h.state = hostLoaded
		}
	})
	return report, nil
}

// emptyParams are the params of a type with empty params, as checked: nothing, and the empty key.
var emptyParams = checkedParams{params: delta.NewJsonObject(0), key: ""}

// served is the type served under name, nil when none is.
func (h *chordHost) served(name string) *served {
	entry, _ := h.types.get(name)
	return entry
}

// loadedPlugin is a published plugin: its facet (an unchanged reload passes the same one), what its setup staged (a reload
// that keeps it uses it again, its setup not run) and its contributions in the registries' tables.
type loadedPlugin struct {
	facet         Facet
	staged        *stagedPlugin
	contributions []*registries.RegistryContribution
}

// loadedTwice is the error of a plugin name given twice: the second is refused.
func loadedTwice(name string) error { return fmt.Errorf("plugin %q is loaded twice", name) }

// stageInto stages plugin's new code into table: the error that refuses it (its setup, a declaration, a name served twice).
func (h *chordHost) stageInto(table *staged, plugin HostFacet) error {
	sp, err := stagePlugin(plugin, h)
	if err != nil {
		return err
	}
	return addStaged(table, sp)
}

// publishPlugins publishes the staged plugins of facets (by name), inside the publication's batch: the registries they
// serve served, their facet contributions entered into the tables (the types are the staged table's, set by the caller).
func (h *chordHost) publishPlugins(table *staged, facets *namedMap[Facet]) error {
	for _, name := range facets.keys() {
		facet, _ := facets.get(name)
		plugin, _ := table.plugins.get(name)
		for _, registryName := range plugin.registries.keys() {
			entry, _ := plugin.registries.get(registryName)
			h.registryRecord(registryName).Serve(entry.registry.Version, entry.provider)
		}
		contributions := make([]*registries.RegistryContribution, 0, len(plugin.contributions))
		for _, each := range plugin.contributions {
			contribution, err := contributionOf(h.registryRecord(each.registry.Name), registries.Contributor{Plugin: name}, each.registry.Version, each.value)
			if err != nil {
				return err
			}
			contributions = append(contributions, contribution)
		}
		for _, contribution := range contributions {
			contribution.Record.Add(contribution)
		}
		h.plugins.set(name, &loadedPlugin{facet: facet, staged: plugin, contributions: contributions})
	}
	return nil
}

// Get implements Host.
func (h *chordHost) Get(ctx context.Context, typ *contracts.Contract, params any, options GetOptions) (*Reference, error) {
	binding, err := h.acquire(typ, params, h.own(), ctx, ctx, options)
	if err != nil {
		return nil, err
	}
	// The binding was built from typ's members.
	return binding.reference, nil
}

// Hold implements Host.
func (h *chordHost) Hold(ctx context.Context, typ *contracts.Contract, params any, options GetOptions) (*Reference, error) {
	var held *binding
	var err error
	h.enter(func() {
		if err = h.batch.AssertOutside("hold"); err != nil {
			return
		}
		held, err = h.holdBinding(typ, params, h.own(), ctx, options)
	})
	if err != nil {
		return nil, err
	}
	// The binding was built from typ's members; it may be unbound, so its variant may be undefined.
	return held.reference, nil
}

// Accept implements Host.
func (h *chordHost) Accept(l link.Link, options LinkOptions, ctx context.Context) error {
	var err error
	h.enter(func() {
		if err = h.assertOpen(); err != nil {
			return
		}
		h.linkOf(l, linkRoleChild, options, ctx)
	})
	return err
}

// Connect implements Host.
func (h *chordHost) Connect(l link.Link, options LinkOptions, ctx context.Context) error {
	var err error
	h.enter(func() {
		if err = h.assertOpen(); err != nil {
			return
		}
		if h.parent.link() != nil {
			err = errors.New("connect: this host has an open parent link")
			return
		}
		parent := h.linkOf(l, linkRoleParent, options, ctx)
		if parent.open() {
			h.parent.connected(parent)
		}
	})
	return err
}

// Route implements Host.
func (h *chordHost) Route(kind *contracts.HostKind, options RouteOptions, ctx context.Context) (RouteHandle, error) {
	return h.addRoute("route", kind, options, ctx, false)
}

// Delegate implements Host.
func (h *chordHost) Delegate(kind *contracts.HostKind, options RouteOptions, ctx context.Context) (RouteHandle, error) {
	return h.addRoute("delegate", kind, options, ctx, true)
}

// addRoute is Route and Delegate: a delegate is the route of the host's own address kind, which its destination consults for the
// types the host does not serve.
func (h *chordHost) addRoute(what string, kind *contracts.HostKind, options RouteOptions, ctx context.Context, delegate bool) (RouteHandle, error) {
	var err error
	var handle RouteHandle
	h.enter(func() {
		if err = h.assertOpen(); err != nil {
			return
		}
		if h.state != hostNew {
			err = fmt.Errorf("%s: routes are declared before load", what)
			return
		}
		if delegate && (h.address == nil || h.address.Kind != kind.Kind) {
			err = fmt.Errorf("delegate: %s is not this host's address kind", kind.Kind)
			return
		}
		if _, ok := h.routes[kind.Kind]; ok {
			err = fmt.Errorf("%s: %s is routed already", what, kind.Kind)
			return
		}
		// The parent of the route's work: its `resolve`s and the holds and calls on its links, aborted when it closes
		// (synchronously, as `AbortSignal.any` aborts: runClose cancels it itself, not through a goroutine's listener).
		routing, cancelRouting := chordctx.WithCancel(ctx)
		h.routeCancels = append(h.routeCancels, cancelRouting)
		made, makeErr := newRoute(kind, options, h, routing)
		if makeErr != nil {
			err = makeErr
			return
		}
		h.routes[kind.Kind] = made
		h.routeKinds = append(h.routeKinds, kind.Kind)
		if delegate {
			h.destination.delegating = true
		}
		handle = RouteHandle{
			Retry: func(address contracts.Address) bool {
				started := false
				h.enter(func() { started = made.retry(address.Key) })
				return started
			},
			Suspend: func(address contracts.Address) { h.enter(func() { made.suspend(address.Key) }) },
		}
	})
	return handle, err
}

// Revoke implements Host.
func (h *chordHost) Revoke(revocation delta.JsonValue, ctx context.Context) error {
	var failure error
	count := 0
	h.enter(func() {
		key := data.CanonicalJson(revocation, ctx)
		revoked := func(principal data.Principal) bool {
			if failure != nil || h.isOwn(principal, ctx) {
				return false
			}
			revokes, err := h.revocationKeys.revokes(principal, key, ctx)
			if err != nil {
				failure = err
				return false
			}
			return revokes
		}
		revokedError := func(label string) *data.ChordError {
			return data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Reason: data.ReasonRevoked, Message: label + ": revoked"}, nil)
		}
		for _, object := range h.objectList() {
			if object.principal != nil && revoked(object.principal) {
				object.end(revokedError(object.label()))
				count++
			}
		}
		// Requests not bound (waiting for the publication or a provider, refused, waiting for their link) end too: nothing
		// revives them.
		unbound := revokedError("an unbound request")
		h.unbound.revoke(revoked, unbound)
		for _, each := range slices.Clone(h.links) {
			each.revokeUnbound(revoked, unbound)
		}
		// This host's own requests over its paths, linked or not (a route still resolving, a link down): a later link would
		// hold them again (fix round 1).
		for _, holder := range h.pathHolders() {
			remote, ok := holder.(*remoteObject)
			if !ok {
				continue
			}
			if principal := remote.caller.principal; principal != nil && revoked(principal) {
				remote.revoke(revokedError(remote.label()))
			}
		}
		// Down the route links before the child links close: a worker ends the principal's objects with the revocation's
		// error before the closed origins release their holds.
		for _, each := range slices.Clone(h.links) {
			if each.role == linkRoleRoute {
				each.send(&link.RevokeMessage{Revocation: revocation})
			}
		}
		for _, each := range slices.Clone(h.links) {
			if each.role == linkRoleChild && revoked(each.principal) {
				each.close()
				count++
			}
		}
	})
	// Recorded after the work: a revocation is synchronous, its span only says what it ended.
	if failure == nil {
		if parent := chordctx.Telemetry(ctx); !chordtelemetry.Untraced(parent) {
			revoke := chordtelemetry.NewOpenSpan(parent, "chord.revoke", telemetry.SpanAttributes{telemetry.Attr("chord.host", h.spanHost)})
			revoke.Span().SetAttributes(telemetry.SpanAttributes{telemetry.Attr("count", float64(count))})
			revoke.End()
		}
	}
	return failure
}

// Activity implements Host.
func (h *chordHost) Activity() state.SourceHandle { return h.activity }

// References implements Host.
func (h *chordHost) References(typ *contracts.Contract) state.SourceHandle {
	var found *references
	h.enter(func() {
		found = h.refs[typ.Name]
		if found != nil {
			return
		}
		found = newReferences(h.batch)
		for _, object := range h.objectList() {
			if object.served.contract.Name != typ.Name {
				continue
			}
			for _, holder := range object.holders() {
				if holder.listed() {
					found.record(object, holder)
				}
			}
		}
		h.refs[typ.Name] = found
	})
	return found
}

// Address implements Host.
func (h *chordHost) Address() *contracts.Address { return h.address }

// Registry implements Host.
func (h *chordHost) Registry(ctx context.Context, r registries.Registry[any, any], options GetOptions) (*registries.RegistryValueSource, error) {
	var (
		err      error
		waiting  linkedAbort
		timer    Timer
		stopWake func() bool
		last     error
	)
	timeout := durationOr(options.ReadyTimeout, h.timeouts.ready)
	h.enter(func() {
		if err = h.batch.AssertOutside("registry"); err != nil {
			return
		}
		if err = h.assertOpen(); err != nil {
			return
		}
		if err = h.assertLoadCalled("registry " + r.Name); err != nil {
			return
		}
		expired, expire := chordctx.WithCancel(chordctx.Background())
		waiting = withLinkedAbortSignal(expired, ctx)
		timer = h.events.AfterFunc(timeout, func() {
			if last != nil {
				expire(last)
				return
			}
			expire(notLive("registry "+r.Name, timeout))
		})
		// The host's close ends the wait at once, `released/closed` (its timer with it).
		stopWake = context.AfterFunc(h.routing, func() { expire(h.closedError()) })
	})
	if err != nil {
		return nil, err
	}
	finish := func() {
		h.enter(func() {
			timer.Stop()
			stopWake()
			waiting.release()
		})
	}
	defer finish()
	parking.Park(ctx)
	select {
	case <-h.publication:
	case <-waiting.context.Done():
		return nil, context.Cause(waiting.context)
	}
	var value *registries.RegistryValueSource
	h.enter(func() {
		if err = h.assertOpen(); err != nil {
			return
		}
		value = h.registryRecord(r.Name).Value(r.Version)
	})
	if err != nil {
		return nil, err
	}
	usable := func() bool {
		start := value.Start()
		if start.Kind == state.StartValue || start.HasLast {
			return true
		}
		last = start.Error
		return false
	}
	var ready bool
	h.enter(func() { ready = usable() })
	if !ready {
		if err := untilUsable(h.events, value, usable, waiting.context); err != nil {
			return nil, err
		}
	}
	return value, nil
}

// Root implements Host.
func (h *chordHost) Root(fn func(ctx Ctx) (any, error), ctx context.Context, options RootOptions) (any, error) {
	var err error
	h.enter(func() {
		if err = h.batch.AssertOutside("root"); err != nil {
			return
		}
		err = h.assertOpen()
	})
	if err != nil {
		return nil, err
	}
	principal := options.Principal
	if options.Anonymous {
		principal = nil
	} else if principal == nil {
		principal = h.principal
	}
	// The scope's ctx acts as principal.
	trace := options.Trace
	if trace == nil || chordtelemetry.Untraced(h.telemetry) {
		return h.roots.run(fn, principal, h.counted(ctx))
	}
	// In a new root span of this host's adapter: a trace of its own.
	attributes := make(telemetry.SpanAttributes, 0, len(trace.Attributes)+1)
	attributes = append(attributes, telemetry.Attr("chord.host", h.spanHost))
	attributes = append(attributes, trace.Attributes...)
	var result any
	err = h.telemetry.StartSpan(telemetry.SpanOptions{Name: trace.Name, Attributes: attributes}, func(span telemetry.TelemetrySpan) error {
		value, runErr := h.roots.run(fn, principal, chordctx.WithTelemetry(ctx, span))
		if runErr != nil {
			chordtelemetry.Ended(span, "trace_failed", runErr, ctx)
		}
		result = value
		return runErr
	})
	return result, err
}

// counted is ctx for work this host does under it (a link's, a root scope's): one without a span carries this host's
// adapter's metrics (CountingOnly), so the cost counters of that work reach the adapter (review L6).
func (h *chordHost) counted(ctx context.Context) context.Context {
	if h.counting == nil || chordctx.Telemetry(ctx) != telemetry.TelemetrySpan(telemetry.NoopTelemetryContext) {
		return ctx
	}
	return chordctx.WithTelemetry(ctx, h.counting)
}

// Serves implements Host.
func (h *chordHost) Serves(name string) bool {
	served := false
	h.enter(func() { served = h.served(name) != nil })
	return served
}

// UnservedNames are the names requests here wait for unbound that no plugin here serves (types and registries:
// `not_served`), sorted; a name served at another version (`version`) is not among them.
func (h *chordHost) UnservedNames() []string {
	var names []string
	h.enter(func() {
		for _, name := range h.unbound.Names() {
			if h.types.has(name) {
				continue
			}
			if record := h.registries[name]; record != nil {
				if _, provided := record.ProvidedVersion(); provided {
					continue
				}
			}
			names = append(names, name)
		}
	})
	return names
}

// ActivationsUnderWay is how many objects here have an activation under way (`chordObject.Activating`).
func (h *chordHost) ActivationsUnderWay() int {
	count := 0
	h.enter(func() {
		for _, object := range h.objects {
			if object.Activating() {
				count++
			}
		}
	})
	return count
}

// UndeclaredRegistry is a registry with contributions and no provider, with the newest version contributed.
type UndeclaredRegistry struct {
	Name    string
	Version int
}

// UndeclaredRegistries are the registries with contributions here and no provider, each with the newest version
// contributed, by name.
func (h *chordHost) UndeclaredRegistries() []UndeclaredRegistry {
	var found []UndeclaredRegistry
	h.enter(func() {
		for _, record := range h.registries {
			version, contributed := record.NewestContribution()
			if _, provided := record.ProvidedVersion(); contributed && !provided {
				found = append(found, UndeclaredRegistry{Name: record.Name, Version: version})
			}
		}
	})
	slices.SortFunc(found, func(a, b UndeclaredRegistry) int { return strings.Compare(a.Name, b.Name) })
	return found
}

// registryRecord is the record of registry name on this host, made by its first use.
func (h *chordHost) registryRecord(name string) *registries.RegistryRecord {
	record := h.registries[name]
	if record == nil {
		record = registries.NewRegistryRecord(name, h.batch, func(err registries.RegistryError) { h.report(RegistryHostError(err)) }, h.clock)
		h.registries[name] = record
		h.registryOrder = append(h.registryOrder, name)
	}
	return record
}

// Close implements Host.
func (h *chordHost) Close(_ context.Context) error {
	var closing *closePromise
	var err error
	h.enter(func() {
		if err = h.batch.AssertOutside("close"); err != nil {
			return
		}
		closing = h.closeOnce()
	})
	if err != nil {
		return err
	}
	<-closing.done
	return closing.err
}

// reportUndeclared is, at each publication (`load`'s, a reload's commit), after its batch: every registry with
// contributions and no provider here is reported `registry_undeclared`, naming its contributors; again only once they
// change.
func (h *chordHost) reportUndeclared() {
	for _, name := range h.registryOrder {
		h.registries[name].ReportUndeclared()
	}
}

// published is whether the host published its setups (`load`'s batch): until then requests made here and holds arriving
// wait in the unbound index, placed by the publication.
func (h *chordHost) published() bool { return h.published_ }

// publish resolves the publication (once).
func (h *chordHost) publish() {
	if h.publishedOnce {
		return
	}
	h.publishedOnce = true
	//portlint:allow doubleclose the publishedOnce flag above returns first inside a section, so the close runs once; Pi's `#publication` is a Promise.withResolvers resolved at the first publication (declared host.ts:459, resolved in publish host.ts:532 and #close host.ts:1375)
	close(h.publication)
}

// index: request cannot bind yet: indexed under name (the type or registry it waits for) until woken or ended, waiting for
// the publication (no cause) or for a provider that fits (cause).
func (h *chordHost) index(name string, request unboundRequest, cause *data.FailureCause) {
	h.unbound.add(name, request, cause)
}

// unindex: request left the unbound index's wait for name (it ended, or a retry checks it again).
func (h *chordHost) unindex(name string, request unboundRequest) { h.unbound.delete(name, request) }

// closing is whether the host closes: its objects end through `close`, not through its links.
func (h *chordHost) closing() bool { return h.state == hostClosed }

// acquire is a reference to typ made by from (its principal, its origin's incarnation), once it is live (`host.get`,
// `ctx.get`): a hold, waited for up to the getter's one deadline, across reconnects and through `loading`, `failed
// { transient }` and `failed { waiting }`; a broken failure returns at once, the deadline with the last failure's error. A
// getter that gives up releases its reference. It blocks, off the loop (it enters it for each section).
func (h *chordHost) acquire(typ *contracts.Contract, params any, from caller, ctx, waiter context.Context, options GetOptions) (*binding, error) {
	var (
		held    *binding
		err     error
		waiting linkedAbort
		timer   Timer
		live    *keyedPromise[struct{}]
	)
	timeout := durationOr(options.ReadyTimeout, h.timeouts.ready)
	h.enter(func() {
		if err = h.batch.AssertOutside("get"); err != nil {
			return
		}
		held, err = h.holdBinding(typ, params, from, ctx, options)
		if err != nil {
			return
		}
		expired, expire := chordctx.WithCancel(chordctx.Background())
		waiting = withLinkedAbortSignal(expired, ctx)
		timer = h.events.AfterFunc(timeout, func() {
			if failure := held.lastFailure(); failure != nil {
				expire(failure)
				return
			}
			expire(notLive(held.label(), timeout))
		})
		// Old work's getter whose target's activation waits on that work's own drain fails at once (rule 6).
		live = held.live(waiting.context)
		if goal, ok := held.target().(*chordObject); ok {
			live = h.waitAsOldWork(live, goal, typ, ctx)
		}
	})
	if err != nil {
		return nil, err
	}
	_, liveErr := live.await(waiter)
	h.enter(func() {
		timer.Stop()
		waiting.release()
		if liveErr != nil {
			held.release()
		}
	})
	if liveErr != nil {
		return nil, liveErr
	}
	return held, nil
}

// waitAsOldWork is waiting (a getter's or a gate's wait on target, for contract's version) as ctx makes it: as old work it is
// cut at once with `unavailable/reload_cycle` when it cycles through that work's own drain (`reloadCycles`); otherwise as it
// is. In a section.
func (h *chordHost) waitAsOldWork(waiting *keyedPromise[struct{}], goal *chordObject, contract *contracts.Contract, ctx context.Context) *keyedPromise[struct{}] {
	if !h.oldWork.of(ctx) {
		return waiting
	}
	chain := chainAttempts(ctx)
	if len(chain) == 0 {
		return waiting
	}
	return h.reloadCycles.wait(h.loop(), waiting, goal, contract, chain)
}

// count adds delta to the keep-alive count of kind (`host.activity`), where the work happens.
func (h *chordHost) count(kind activityKind, delta int) { h.activity.count(kind, delta) }

// referenced: holder references object: listed in its type's presence, if someone asked for it.
func (h *chordHost) referenced(object *chordObject, holder holder, ctx context.Context) {
	if !holder.listed() {
		return
	}
	if refs := h.refs[object.served.contract.Name]; refs != nil {
		refs.add(object, holder, ctx)
	}
}

// unreferenced: holders of object ended: gone from its type's presence.
func (h *chordHost) unreferenced(object *chordObject, holders []holder, ctx context.Context) {
	refs := h.refs[object.served.contract.Name]
	if refs == nil {
		return
	}
	refs.remove(holders, ctx)
}

// report reports a background failure to `onError`; a failing `onError` has nowhere left to report to.
func (h *chordHost) report(err HostError) {
	defer func() { _ = recover() }() // upstream: packages/chord/src/host/host.ts:ChordHost.report (a throwing onError is swallowed)
	h.onError(err)
}

// reference is an unbound reference to an object of typ made by from, declared by dependent (nil for `host.get`,
// `host.hold` and `ctx.get`).
func (h *chordHost) reference(typ *contracts.Contract, dependent *chordObject, from caller, options GetOptions) (*binding, error) {
	if err := h.assertOpen(); err != nil {
		return nil, err
	}
	return h.newBindingFor(typ, dependent, from, options), nil
}

// bind binds b to the object for (typ, params) as its principal where its host address puts it (`destination`): the object
// here, created with its first attempt if there is none, or a remote object held over its path. Placed here, the check is
// only against the type served here: not served (or of the other kind) or not at the caller's version and fingerprint, the
// reference waits unbound, `failed { waiting }`, indexed under the type's name. Returns the object it bound to, nil when it
// waits; fails as `destination.reference` does (a broken refusal).
func (h *chordHost) bind(b *binding, typ *contracts.Contract, params checkedParams, ctx context.Context) (target, error) {
	from := b.caller()
	placed, err := h.destination.reference(typ, params.params, from.principal, ctx)
	if err != nil {
		return nil, err
	}
	if placed.kind == placedPath {
		remote := newRemoteObject(h, typ, params, placed.address, from, placed.path, b.dependent(), chordctx.Telemetry(ctx))
		placed.path.attach(remote)
		b.bind(remote)
		return remote, nil
	}
	entry := h.served(typ.Name)
	unbound := unboundCause(entry, typ.Kind, typ.Name, typ.Version, typ.Fingerprint)
	if unbound == nil {
		unbound, err = h.misplacedServed(typ, entry, params.params)
		if err != nil {
			return nil, err
		}
	}
	if unbound != nil {
		// Old work never waits for a code update (rule 2): a version the new code dropped binds the object while its open old
		// attempt still serves it, and fails at once otherwise.
		if unbound.Error.Code == data.CodeContractMismatch && h.oldWork.of(ctx) {
			object := h.objects[h.identityOf(entry, params, from.principal, ctx)]
			if object == nil || !object.oldServes(typ.Version, typ.Fingerprint) {
				return nil, unbound.Error
			}
			b.bind(object)
			return object, nil
		}
		h.waitUnbound(b, typ.Name, unbound, func() { h.placeBinding(b, typ, params, ctx) })
		return nil, nil
	}
	// An anonymous scope (an HTTP request nobody authenticated) is no principal a remote type's factory can serve (Opus's
	// build review F5): refused, never bound as the host.
	if entry.kind != servedLocal && from.principal == nil {
		return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Message: typ.Name + ": an anonymous caller gets no remote object"}, nil)
	}
	// Checked above: served here at the caller's version.
	object := h.objectFor(entry, params, from.principal, ctx, noChain, b.dependent())
	b.bind(object)
	// A getter waiting for a shared activation links to it (decision "Shared work").
	if activation := object.activationSpan(); activation != nil && !chordtelemetry.Untraced(chordctx.Telemetry(ctx)) {
		chordctx.Telemetry(ctx).AddLink(telemetry.SpanLink{Context: activation})
	}
	return object, nil
}

// place is where a hold arriving on l goes (`destination.hold`: by the address it names); `released` once the host closed.
func (h *chordHost) place(request holdRequest, l *hostLink, from caller, ctx context.Context) (placed, error) {
	if err := h.assertOpen(); err != nil {
		return placed{}, err
	}
	return h.destination.hold(request.Address, request.Type, l, from.principal, ctx)
}

// serving is what a hold placed here binds to, acting as principal: the object, or why it waits unbound (`failed
// { waiting }`): a remote type not served here (`not_served`), not at the hold's version and fingerprint, or whose params
// put it on another host (code skew: `version`). It fails for a broken refusal: invalid params, an activation the hold's
// chain waits for (`invalid/cycle`, Q5). A new object keeps the hold's chain.
func (h *chordHost) serving(request holdRequest, params any, principal data.Principal, ctx context.Context) (Serving, error) {
	chain := request.Chain
	entry := h.served(request.Type)
	if unbound := unboundCause(entry, contracts.KindRemote, request.Type, request.Version, request.Fingerprint); unbound != nil {
		return Serving{Kind: ServingWaiting, Cause: *unbound}, nil
	}
	// Checked above: a remote type served here.
	checked, err := checkParams(entry.contract, params, ctx)
	if err != nil {
		return Serving{}, err
	}
	misplaced, err := h.destination.misplaced(entry.contract, checked.params)
	if err != nil {
		return Serving{}, err
	}
	if misplaced != nil {
		return Serving{Kind: ServingWaiting, Cause: data.FailureCause{Kind: data.FailureWaiting, Error: misplaced}}, nil
	}
	// The hold's carrier with this host's adapter: the first trigger of an object it creates.
	created := ctx
	if trigger := chordtelemetry.ParentOf(request.Trace, h.telemetry); !chordtelemetry.Untraced(trigger) {
		created = chordctx.WithTelemetry(ctx, chordtelemetry.AsSpan(trigger))
	}
	object := h.objectFor(entry, checked, principal, created, chain, nil)
	if activation := object.activationId(); activation != "" && slices.Contains(chain, activation) {
		return Serving{}, h.cycle(chain, activation, []string{object.label()})
	}
	return Serving{Kind: ServingObject, Object: object}, nil
}

// pathOf is the path a remote reference of typ with params acting as principal goes over now: `ref.retry()` places a
// reference failed for a part past the limits anew (a route may have forgotten its old path). It fails as
// `destination.reference` does, and `unavailable` for a type placed here now.
func (h *chordHost) pathOf(typ *contracts.Contract, params *delta.JsonObject, principal data.Principal) (*path, error) {
	// A retry has no context of its own: its admission checks count on none.
	placed, err := h.destination.reference(typ, params, principal, chordctx.Background())
	if err != nil {
		return nil, err
	}
	if placed.kind == placedPath {
		return placed.path, nil
	}
	return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: typ.Name + " is placed here now"}, nil)
}

// cycle is the `invalid/cycle` error of a cycle through chain (Q5): its entries from the activation id on, by label where
// the object is on this host ("…" for other hosts'), then closing.
func (h *chordHost) cycle(chain []string, id string, closing []string) *data.ChordError {
	labels := map[string]string{}
	for _, each := range h.objectList() {
		labels[each.activationId()] = each.label()
	}
	cycle := make([]string, 0, len(chain))
	for _, entry := range chain[slices.Index(chain, id):] {
		label, ok := labels[entry]
		if !ok {
			label = "…"
		}
		cycle = append(cycle, label)
	}
	cycle = append(cycle, closing...)
	return data.Invalid(data.ReasonCycle, "dependency cycle: "+joinArrows(cycle))
}

func joinArrows(labels []string) string {
	var joined strings.Builder
	for i, label := range labels {
		if i > 0 {
			joined.WriteString(" → ")
		}
		joined.WriteString(label)
	}
	return joined.String()
}

// relayOrigin is a new origin number for side, which relays holds upstream; registered in `relays` until it closes.
func (h *chordHost) relayOrigin(side *providerSide) int {
	origin := h.nextOrigin
	h.nextOrigin++
	h.relays.set(origin, side)
	return origin
}

// routeLink is a route's link, its work under the route's ctx: registered with the host and its heartbeat; one closed
// already is returned closed.
func (h *chordHost) routeLink(l link.Link, options routeLinkOptions, ctx context.Context) *hostLink {
	limits := link.PartLimits{}
	if options.limits != nil {
		limits = *options.limits
	}
	return h.linkOf(l, linkRoleRoute, LinkOptions{Principal: options.principal, Trusted: options.trusted, Codec: options.codec, Limits: limits}, ctx)
}

// linkOpened: a link completed its handshake: a route's address resolves again once if it drops.
func (h *chordHost) linkOpened(l *hostLink) {
	if l.role != linkRoleRoute {
		return
	}
	for _, kind := range h.routeKinds {
		h.routes[kind].opened(l)
	}
}

// unlink forgets a link once it closed: the origins relaying to it, then the parent's path or its route's address are
// told; the heartbeat may stop.
func (h *chordHost) unlink(l *hostLink) {
	h.links = slices.DeleteFunc(h.links, func(each *hostLink) bool { return each == l })
	for _, relay := range h.relays.values() {
		relay.unlinked(l)
	}
	if len(h.links) == 0 {
		h.stopTicking()
	}
	if l.role == linkRoleParent {
		h.parent.disconnected(l)
	}
	if l.role == linkRoleRoute {
		for _, kind := range h.routeKinds {
			if h.routes[kind].closed(l) {
				break
			}
		}
	}
}

// wentLive moves object behind every object that went live before it (close ends in reverse); its backoff is over.
func (h *chordHost) wentLive(object *chordObject) {
	h.dropObject(object.identity)
	h.objects[object.identity] = object
	h.objectOrder = append(h.objectOrder, object.identity)
	h.backoff.delete(object.identity)
}

// forget drops an ended object (once, before a next instance of its identity exists).
func (h *chordHost) forget(object *chordObject) {
	h.dropObject(object.identity)
	h.backoff.ended(object.identity)
}

// ending records a teardown for identity: its next attempt waits for it and for every earlier one still running.
func (h *chordHost) ending(identity string, teardown *keyedPromise[struct{}]) {
	previous := h.endings[identity]
	ending := teardown
	if previous != nil {
		ending = newKeyedPromise[struct{}](h.events)
		pending := 2
		both := func(struct{}, error) {
			pending--
			if pending == 0 {
				ending.resolve(struct{}{})
			}
		}
		previous.then(both)
		teardown.then(both)
	}
	h.endings[identity] = ending
	ending.then(func(struct{}, error) {
		if h.endings[identity] == ending {
			delete(h.endings, identity)
		}
	})
}

// endingOf is what the next attempt for identity waits for: its previous instances' teardowns, if any still run.
func (h *chordHost) endingOf(identity string) *keyedPromise[struct{}] { return h.endings[identity] }

// holdBinding is a reference to typ made by from for `host.hold` and getters: params checked at once (`invalid/params`,
// also before `load`), then placed (placeBinding); before the host's publication it waits in the unbound index, `loading`,
// and the publication places it.
func (h *chordHost) holdBinding(typ *contracts.Contract, params any, from caller, ctx context.Context, options GetOptions) (*binding, error) {
	if err := h.assertOpen(); err != nil {
		return nil, err
	}
	if err := h.assertLoadCalled(typ.Name); err != nil {
		return nil, err
	}
	checked, err := checkParams(typ, params, ctx)
	if err != nil {
		return nil, err
	}
	made := h.newBindingFor(typ, nil, from, options)
	place := func() { h.placeBinding(made, typ, checked, ctx) }
	if h.published_ {
		place()
	} else {
		h.waitUnbound(made, typ.Name, nil, place)
	}
	return made, nil
}

// placeBinding places a reference (`bind`); where it cannot go (a `hostAddress` or `authorize` that fails or refuses) fails
// it broken, until `ref.retry()` places it again. One woken after the host closed ends as the close ended the others.
func (h *chordHost) placeBinding(b *binding, typ *contracts.Contract, checked checkedParams, ctx context.Context) {
	if h.state == hostClosed {
		b.end(h.closedError())
		return
	}
	if _, err := h.bind(b, typ, checked, ctx); err != nil {
		again := func() { h.placeBinding(b, typ, checked, ctx) }
		b.refuse(data.FailureCause{Kind: data.FailureBroken, Error: data.AsChordError(err, typ.Name)}, again)
	}
}

// misplacedServed is the provider's own host-address check of a remote typ placed here by the consumer's contract, against
// the one served (`invalid/host_address`, `waiting`: code skew); none when both have the same `hostAddress` (the consumer
// computed it with the provider's function) or the type is local. A provider's `hostAddress` that fails fails.
func (h *chordHost) misplacedServed(typ *contracts.Contract, entry *served, params *delta.JsonObject) (*data.FailureCause, error) {
	if entry.contract.Kind != contracts.KindRemote {
		return nil, nil
	}
	mine, err := contracts.ContractDataOf(typ)
	if err != nil {
		return nil, err
	}
	theirs, err := contracts.ContractDataOf(entry.contract)
	if err != nil {
		return nil, err
	}
	if sameHostAddress(mine.HostAddress, theirs.HostAddress) {
		return nil, nil
	}
	misplaced, err := h.destination.misplaced(entry.contract, params)
	if err != nil || misplaced == nil {
		return nil, err
	}
	return &data.FailureCause{Kind: data.FailureWaiting, Error: misplaced}, nil
}

// sameHostAddress is Pi's `===` of two host-address functions: the same function value.
func sameHostAddress(a, b contracts.HostAddress) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return fmt.Sprintf("%p", a) == fmt.Sprintf("%p", b)
}

// waitUnbound leaves b unbound in the index under name: cause (`waiting`), or none before the publication.
func (h *chordHost) waitUnbound(b *binding, name string, cause *data.FailureCause, place func()) {
	b.wait(cause, place, func() { h.unbound.delete(name, b) })
	h.unbound.add(name, b, cause)
}

// newBindingFor is a new reference's binding made by from; its parked calls wait its `readyTimeout`.
func (h *chordHost) newBindingFor(typ *contracts.Contract, dependent *chordObject, from caller, options GetOptions) *binding {
	return newBinding(typ, h, h.batch, dependent, from, durationOr(options.ReadyTimeout, h.timeouts.ready))
}

// own is this host as the maker of its own references: its principal and incarnation.
func (h *chordHost) own() caller { return caller{principal: h.principal, incarnation: h.incarnation} }

// PrincipalAttributes implements Host: the attributes this host's spans record for principal: its PrincipalAttributes hook's, else
// `chord.principal`, its principal key; none without a principal.
func (h *chordHost) PrincipalAttributes(principal data.Principal, ctx context.Context) telemetry.SpanAttributes {
	if principal == nil {
		return nil
	}
	if h.principalHook != nil {
		return h.principalHook(principal)
	}
	return telemetry.SpanAttributes{telemetry.Attr("chord.principal", chordtelemetry.Capped(data.PrincipalKey(principal, ctx, h.principalKey)))}
}

// isOwn is whether principal is this host's own (by principal key): revocation never touches it.
func (h *chordHost) isOwn(principal data.Principal, ctx context.Context) bool {
	own := h.principal
	if own == nil {
		return false
	}
	return data.PrincipalKey(own, ctx, h.principalKey) == data.PrincipalKey(principal, ctx, h.principalKey)
}

// linkOf is a new link of role, registered with the heartbeat while open.
func (h *chordHost) linkOf(l link.Link, role linkRole, options LinkOptions, ctx context.Context) *hostLink {
	codec := options.Codec
	if codec == nil {
		codec = link.BinaryCodec
	}
	limits := link.LinkLimits(options.Trusted, options.Limits)
	made := newHostLink(h, l, hostLinkOptions{role: role, principal: options.Principal, trusted: options.Trusted, codec: codec, limits: limits}, h.counted(chordctx.WithoutAbortSignal(ctx)))
	if !made.open() {
		return made
	}
	h.links = append(h.links, made)
	if h.ticking == nil {
		h.startTicking()
	}
	return made
}

// startTicking runs the heartbeat: every link ticks each tenth of the shorter limit.
func (h *chordHost) startTicking() {
	ticking := &heartbeatTimer{}
	h.ticking = ticking
	ping, shut := h.heartbeat.ping, h.heartbeat.close
	//portlint:allow numbers min(ping, shut) is a time.Duration in whole nanoseconds: the /10 drops under 1 ns, below nodeDelay's whole-millisecond truncation, so the armed delay equals Pi's Math.min(ping, close) / 10 (host.ts:1319)
	interval := min(ping, shut) / 10
	last := h.events.Now()
	tick := func() {
		if ticking.stopped {
			return
		}
		now := h.events.Now()
		// Pi's pause is `Date.now()` time (host.ts:1611); the host clock is whole milliseconds.
		// A tick a whole interval late is time this process did not run (a sleeping laptop, a blocked event loop).
		late := float64(now-last) - float64(interval)/float64(time.Millisecond)
		var paused int64
		if late >= float64(interval)/float64(time.Millisecond) {
			//portlint:allow numbers late is the difference of two finite millisecond counters, so the conversion is of a finite number; truncation to whole milliseconds is the host clock's resolution
			paused = int64(late)
		}
		last = now
		for _, each := range slices.Clone(h.links) {
			each.tick(now, paused, ping, shut)
		}
	}
	var arm func()
	arm = func() {
		if faking, ok := h.events.(intervalLoop); ok {
			if timer, faked := faking.AfterInterval(interval, func() {
				tick()
				if !ticking.stopped {
					arm()
				}
			}); faked {
				ticking.timer = timer
				return
			}
		}
		ticking.real = newDropTimer(nodeDelay(interval), func() {
			h.enter(func() {
				tick()
				if !ticking.stopped {
					arm()
				}
			})
		})
	}
	arm()
}

// stopTicking is `clearInterval` of the heartbeat.
func (h *chordHost) stopTicking() {
	if h.ticking == nil {
		return
	}
	h.ticking.stopped = true
	if h.ticking.timer != nil {
		h.ticking.timer.Stop()
	}
	if h.ticking.real != nil {
		h.ticking.real.Stop()
	}
	h.ticking = nil
}

// identityOf is the in-process identity of the object of entry for params and, for a remote type, principal.
func (h *chordHost) identityOf(entry *served, params checkedParams, principal data.Principal, ctx context.Context) string {
	if entry.contract.Kind == contracts.KindLocal {
		return data.IdentityKey(entry.contract, params.key)
	}
	// A host serving remote types has a principal, which its references act as unless a link stamped its own.
	return data.IdentityKey(entry.contract, params.key, data.PrincipalKey(principal, ctx, h.principalKey))
}

// objectFor is the object for params (checked by L1) and, for a remote type, principal (its key is part of the identity),
// created if there is none; its first attempt starts at once, or when its identity's backoff window ends. A new one keeps
// the activation chain it is created for (Q5): chain (a hold's), or that of dependent's activation when an activation
// declared it, so its own holds over links carry that chain on.
func (h *chordHost) objectFor(entry *served, params checkedParams, principal data.Principal, ctx context.Context, chain []string, dependent *chordObject) *chordObject {
	identity := h.identityOf(entry, params, principal, ctx)
	object := h.objects[identity]
	if object == nil {
		// Shared by every getter: one getter's cancellation does not cancel it.
		var owner data.Principal
		if entry.contract.Kind != contracts.KindLocal {
			owner = principal
		}
		created := chain
		if dependent != nil {
			created = dependent.activationChain()
		}
		object = newChordObject(h, entry, params.params, owner, params.key, identity, chordctx.WithoutAbortSignal(ctx), created)
		h.objects[identity] = object
		h.objectOrder = append(h.objectOrder, identity)
		if !h.backoff.waiting(identity) {
			object.attempt()
		}
	}
	return object
}

// dropObject removes identity from the objects.
func (h *chordHost) dropObject(identity string) {
	if _, ok := h.objects[identity]; !ok {
		return
	}
	delete(h.objects, identity)
	h.objectOrder = slices.DeleteFunc(h.objectOrder, func(each string) bool { return each == identity })
}

// objectList is the objects in Pi's Map order.
func (h *chordHost) objectList() []*chordObject {
	objects := make([]*chordObject, 0, len(h.objectOrder))
	for _, identity := range h.objectOrder {
		objects = append(objects, h.objects[identity])
	}
	return objects
}

// closeOnce is the one closing: started by the first close, which every close returns. It runs inside a section.
func (h *chordHost) closeOnce() *closePromise {
	if h.closer == nil {
		h.closer = h.runClose()
	}
	return h.closer
}

// runClose closes the links, then ends every object at once and tears them down one by one, the last to go live first,
// after the endings already running. Its synchronous part runs now (inside a section), the rest as a task of the loop.
func (h *chordHost) runClose() *closePromise {
	closing := &closePromise{done: make(chan struct{})}
	h.state = hostClosed
	closed := h.closedError()
	h.routingCancel(closed)
	for _, cancel := range h.routeCancels {
		cancel(closed)
	}
	// Links first, children, routes, then the parent: no `gone` goes out for the objects ending below.
	links := slices.Clone(h.links)
	for _, role := range []linkRole{linkRoleChild, linkRoleRoute, linkRoleParent} {
		for _, each := range links {
			if each.role == role {
				each.close()
			}
		}
	}
	// Remote references end now: their parked calls reject `released`, no later link holds them.
	// Code outside objects stops at its next use of Chord: an `fn` waiting in `sleep(ms, ctx)` ends.
	h.roots.close(closed)
	for _, holder := range h.pathHolders() {
		if remote, ok := holder.(*remoteObject); ok {
			remote.lost(closed)
		}
	}
	// At the fence too: requests still waiting for a publication or a provider end as the others did, and gets waiting for
	// a publication that will not come fail now, not after the slow closes below (fix round 1).
	h.unbound.close(closed)
	h.clock.Close()
	h.publish()
	h.backoff.clear()
	// Every object is sealed before any teardown releases a reference, so none ends in the background.
	objects := h.objectList()
	teardowns := make([]func() []*closeFailed, 0, len(objects))
	for _, object := range slices.Backward(objects) {
		teardowns = append(teardowns, object.stop())
	}
	pending := make([]*keyedPromise[struct{}], 0, len(h.endings))
	for _, ending := range h.endings {
		pending = append(pending, ending)
	}
	h.events.Go(func() {
		for _, ending := range pending {
			<-ending.done
		}
		var failures []*closeFailed
		for _, tearDown := range teardowns {
			failures = append(failures, tearDown()...)
		}
		h.enter(func() {
			h.types = newNamedMap[*served]()
			// Their facets keep their factories' closures (which may keep objects' handles).
			h.plugins = newNamedMap[*loadedPlugin]()
			for _, name := range h.registryOrder {
				h.registries[name].Close()
			}
			clear(h.registries)
			h.registryOrder = nil
			if h.store != nil {
				h.store.Detach()
			}
		})
		if len(failures) > 0 {
			errs := make([]error, len(failures))
			for i, failure := range failures {
				errs[i] = failure.Error
			}
			closing.err = fmt.Errorf("host.close: %d native close(s) threw: %w", len(failures), errors.Join(errs...))
		}
		//portlint:allow doubleclose runClose runs once per host: closeOnce starts it only while h.closer is nil (Pi host.ts:984 `this.#closing ??= this.#close()`), and this closePromise is private to that run
		close(closing.done)
	})
	return closing
}

// pathHolders is every holder of the parent path, the unplaced one and every route's paths: this host's requests over
// links.
func (h *chordHost) pathHolders() []upstream {
	var holders []upstream
	holders = append(holders, h.parent.holders()...)
	holders = append(holders, h.unplaced.holders()...)
	for _, kind := range h.routeKinds {
		holders = append(holders, h.routes[kind].holders()...)
	}
	return holders
}

// storageFailed: the store stopped after a backend error (it tells the host once): reported, and the host closes.
func (h *chordHost) storageFailed(err error) {
	h.enter(func() {
		h.report(HostError{Kind: ErrStorageFailed, Error: err})
		// The app's own `close` returns this closing and gets its failures.
		h.closeOnce()
	})
}

// due: a backoff window ended: the identity's object is retried; without one the entry goes.
func (h *chordHost) due(identity string) {
	object := h.objects[identity]
	if object == nil {
		h.backoff.delete(identity)
		return
	}
	object.attempt()
}

func (h *chordHost) assertOpen() error {
	if h.state == hostClosed {
		return h.closedError()
	}
	return nil
}

// assertLoadCalled: a request of this host's own (`get`, `hold`, `registry`, a root's `ctx.get`) for name before its first
// `load` would wait for a publication nobody started: it fails `not_ready` at once (decision "Mistakes that wait are
// reported at once" (a)). Holds arriving on its links before `load` still wait for the publication (a worker connects
// first, W6).
func (h *chordHost) assertLoadCalled(name string) error {
	if h.state != hostNew {
		return nil
	}
	return data.NewChordError(data.ChordErrorInit{
		Code:    data.CodeNotReady,
		Message: name + ": the host has not loaded; call host.load first (a host with nothing to load calls load([]))",
	}, nil)
}

func (h *chordHost) closedError() *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonClosed, Message: "the host is closed"}, nil)
}

// unboundCause is why a reference to a type of kind at version and fingerprint, placed here, cannot bind to entry (the
// type of that name served here): not served, or as the other kind (`unavailable`); another version or fingerprint
// (`contract_mismatch`); nil when it binds. Both wait for a publication: `failed { waiting }`.
func unboundCause(entry *served, kind contracts.Kind, name string, version int, fingerprint string) *data.FailureCause {
	if entry == nil || entry.contract.Kind != kind {
		return &data.FailureCause{Kind: data.FailureWaiting, Error: data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: name + " is not served here"}, nil)}
	}
	contract := entry.contract
	if data.Binds(entry.provider, version, fingerprint) {
		return nil
	}
	message := fmt.Sprintf("%s v%d does not match the served v%d", name, version, contract.Version)
	return &data.FailureCause{Kind: data.FailureWaiting, Error: data.NewChordError(data.ChordErrorInit{Code: data.CodeContractMismatch, Message: message}, nil)}
}

// checkParams is the checked params of typ (`invalid/params`) as the binding and the object take them.
func checkParams(typ *contracts.Contract, params any, ctx context.Context) (checkedParams, error) {
	checked, err := data.CheckParams(typ, params, ctx)
	if err != nil {
		return checkedParams{}, err
	}
	switch values := checked.Params.(type) {
	case *delta.JsonObject:
		return checkedParams{params: values, key: checked.Key}, nil
	case map[string]any:
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		object := delta.NewJsonObject(len(keys))
		for _, key := range keys {
			object.Set(key, values[key])
		}
		return checkedParams{params: object, key: checked.Key}, nil
	}
	return checkedParams{}, fmt.Errorf("host: params of %s are %T", typ.Name, checked.Params)
}

// registryWake is the waiter of an untilUsable: a change wakes it as a microtask, the value's close ends it.
type registryWake struct {
	loop    eventLoop
	changed chan struct{}
	closed  chan error
}

// DependencyMoved implements registries.RegistryWaiter.
func (w *registryWake) DependencyMoved() {
	w.loop.Queue(func() {
		select {
		case w.changed <- struct{}{}:
		default:
		}
	})
}

// Closed implements registries.RegistryWaiter: the value closed with its host.
func (w *registryWake) Closed(err *data.ChordError) {
	select {
	case w.closed <- err:
	default:
	}
}

// untilUsable waits until usable() (a registry value's check, which records its last error) holds, checked at every change
// the value tells its waiters of (after the batch that made it); it fails when ctx aborts or the value closes. It waits on
// the value only while it runs. It blocks, off the loop.
func untilUsable(loop sectionLoop, value *registries.RegistryValueSource, usable func() bool, ctx context.Context) error {
	wake := &registryWake{loop: loop, changed: make(chan struct{}, 1), closed: make(chan error, 1)}
	var release func()
	loop.Run(func() { release = value.Wait(wake) })
	defer loop.Run(func() { release() })
	for {
		var ok bool
		loop.Run(func() { ok = usable() })
		if ok {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		parking.Park(ctx)
		select {
		case <-wake.changed:
		case err := <-wake.closed:
			return err
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// reloadPlan is what a reload staged: the staged set, the facets of the plugins it applies, the plugins by change, those
// that failed, and each affected object's fate.
type reloadPlan struct {
	staged  *staged
	facets  *namedMap[Facet]
	added   []string
	changed []string
	removed []string
	failed  []PluginFailure
	fates   []plannedFate
}

// recordedFactory is a recorded new factory of an object staging planned a fate for: whose new code it is, and what it
// declared.
type recordedFactory struct {
	plugin   string
	recorded Recorded
}

// stagingPass is one staging pass of a reload: its plan, or the plugins it found failing (staging runs again without their
// new code).
type stagingPass struct {
	plan     *reloadPlan
	recorded []recordedFactory
	failed   *namedMap[error]
}

// plannedFate is an affected object's fate, as staging decided it: end (its type is gone), or kind, with its new factory's
// requests as staging recorded them.
type plannedFate struct {
	end      bool
	kind     reloadFate
	object   *chordObject
	served   *served
	requests []request
}

// startedFate is a fate started by a reload's commit, for its report: a retry's new attempt is checked first, a restart
// reports what its readiness found (checked: why the new attempt waits, nil when it does not; nil for an ended object).
type startedFate struct {
	object  string
	kind    ReloadedObjectKind
	checked *keyedPromise[*data.FailureCause]
	// check makes checked once every fate of the reload started.
	check func() *keyedPromise[*data.FailureCause]
}

// reloaded is what a started fate of a reload kind reports.
var reloadedKinds = map[reloadFate]ReloadedObjectKind{reloadRestart: ReloadRestarted, reloadRetry: ReloadRetried}

// reloadAborted is the `aborted` error of a reload whose context aborted before its commit: nothing changed.
func reloadAborted() *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeAborted, Message: "reload: aborted"}, nil)
}

// Reload implements Host: one reload, after the previous one: staging, the commit, the fates, the report.
func (h *chordHost) Reload(facets []HostFacet, ctx context.Context, options ReloadOptions) (_ ReloadReport, reloadErr error) {
	var failure error
	var previous, mine *keyedPromise[struct{}]
	h.enter(func() {
		if failure = h.batch.AssertOutside("reload"); failure != nil {
			return
		}
		if h.state == hostClosed {
			failure = h.closedError()
			return
		}
		if h.state != hostLoaded {
			failure = errors.New("reload runs after load")
			return
		}
		if failure = checkPluginNames(facets); failure != nil {
			return
		}
		previous = h.reloading
		mine = newKeyedPromise[struct{}](h.events)
		h.reloading = mine
	})
	if failure != nil {
		return ReloadReport{}, failure
	}
	// The tail settles to nothing: the next reload waits for this one's end, whatever it was.
	defer h.enter(func() { mine.resolve(struct{}{}) })
	if previous != nil {
		parking.Park(ctx)
		<-previous.done
	}
	var plan *reloadPlan
	var started []startedFate
	// Its `chord.reload` span under the caller's, after the previous reload, when something records: each object's fate an
	// event of it.
	var span *chordtelemetry.OpenSpan
	var reported *ReloadReport
	defer func() {
		if span == nil {
			return
		}
		if reloadErr != nil {
			chordtelemetry.Ended(span.Span(), "reload_failed", reloadErr, ctx)
		} else if reported != nil {
			for _, object := range reported.Objects {
				span.Span().AddEvent("chord.reload.fate", telemetry.SpanAttributes{
					telemetry.Attr("object", chordtelemetry.Capped(object.Object)), telemetry.Attr("fate", string(object.Kind)),
				})
			}
			span.Span().SetAttributes(telemetry.SpanAttributes{
				telemetry.Attr("added", float64(len(reported.Plugins.Added))),
				telemetry.Attr("changed", float64(len(reported.Plugins.Changed))),
				telemetry.Attr("removed", float64(len(reported.Plugins.Removed))),
				telemetry.Attr("failed", float64(len(reported.Failed))),
				telemetry.Attr("fates", float64(len(reported.Objects))),
			})
		}
		span.End()
	}()
	if parent := chordctx.Telemetry(ctx); !chordtelemetry.Untraced(parent) {
		span = chordtelemetry.NewOpenSpan(parent, "chord.reload", telemetry.SpanAttributes{
			telemetry.Attr("chord.host", h.spanHost),
			telemetry.Attr("plugins", float64(len(facets))),
		})
		ctx = chordctx.WithTelemetry(ctx, span.Span())
	}
	h.enter(func() {
		if failure = h.assertOpen(); failure != nil {
			return
		}
		if ctx.Err() != nil {
			failure = reloadAborted()
			return
		}
		plan = h.stageReload(facets, options.Failed, ctx)
		// Aborted while it staged (a setup may abort it synchronously): nothing changed.
		if ctx.Err() != nil {
			failure = reloadAborted()
			return
		}
		started, failure = h.commit(plan, ctx)
	})
	if failure != nil {
		return ReloadReport{}, failure
	}
	plugins := PluginChanges{Added: plan.added, Changed: plan.changed, Removed: plan.removed}
	committed := CommittedReload{Added: plan.added, Changed: plan.changed, Removed: plan.removed}
	for _, each := range plan.failed {
		committed.Failed = append(committed.Failed, each.Plugin)
	}
	objects, err := h.reloadReport(started, committed, ctx)
	if err != nil {
		return ReloadReport{}, err
	}
	report := ReloadReport{Plugins: plugins, Failed: plan.failed, Objects: objects}
	reported = &report
	return report, nil
}

// stageReload is staging (slice 7b §2), synchronous (no request or arriving hold meets it half done), per plugin (decision
// "Per plugin at startup and reload"): a pass stages the new code (stagePass); a plugin whose new code fails there (or
// failed `before`, passed as its loaded facet) keeps its old code (an added one is left out) and staging runs again
// without it; then a changed plugin whose recorded requests would not bind because of such a plugin keeps its old code too
// (closure), until nothing more fails. Bounded by the number of plugins with new code.
func (h *chordHost) stageReload(facets []HostFacet, before []PluginFailure, ctx context.Context) *reloadPlan {
	failed := newNamedMap[error]()
	for _, each := range before {
		failed.set(each.Plugin, each.Error)
	}
	given := newNamedMap[Facet]()
	// A name given twice: the second is refused, the first staged.
	var twice []PluginFailure
	for _, plugin := range facets {
		if given.has(plugin.Name) {
			twice = append(twice, PluginFailure{Plugin: plugin.Name, Error: loadedTwice(plugin.Name)})
		} else {
			given.set(plugin.Name, plugin.Facet)
		}
	}
	for {
		pass := h.stagePass(given, failed, ctx)
		if pass.failed != nil {
			for _, name := range pass.failed.keys() {
				err, _ := pass.failed.get(name)
				failed.set(name, err)
			}
			continue
		}
		kept := h.closure(pass.plan, pass.recorded, failed)
		if kept.len() > 0 {
			for _, name := range kept.keys() {
				err, _ := kept.get(name)
				failed.set(name, err)
			}
			continue
		}
		failures := slices.Clone(twice)
		for _, name := range failed.keys() {
			err, _ := failed.get(name)
			failures = append(failures, PluginFailure{Plugin: name, Error: err})
		}
		// A reload that failed nothing has no failures (Pi's empty array).
		pass.plan.failed = failures
		return pass.plan
	}
}

// stagePass is one staging pass: the loaded entries of the plugins kept as they are (unchanged, or failed: their setups not
// run again) first, then every other plugin's new code in the given order (one refused by a name the table has already
// fails), then the new factory of every object of a changed or removed plugin whose type is still served (an error fails the
// plugin serving it). Its plan, or the plugins that failed.
func (h *chordHost) stagePass(given *namedMap[Facet], failed *namedMap[error], ctx context.Context) stagingPass {
	table := emptyStaged()
	failing := newNamedMap[error]()
	var fresh []HostFacet
	for _, name := range given.keys() {
		facet, _ := given.get(name)
		loaded, ok := h.plugins.get(name)
		if ok && (loaded.facet.same(facet) || failed.has(name)) {
			_ = addStaged(table, loaded.staged)
		} else if !failed.has(name) {
			fresh = append(fresh, HostFacet{Name: name, Facet: facet})
		}
	}
	for _, plugin := range fresh {
		if err := h.stageInto(table, plugin); err != nil {
			failing.set(plugin.Name, err)
		}
	}
	if failing.len() > 0 {
		return stagingPass{failed: failing}
	}
	applied := map[string]struct{}{}
	for _, plugin := range fresh {
		applied[plugin.Name] = struct{}{}
	}
	var removed []string
	for _, name := range h.plugins.keys() {
		if !given.has(name) {
			removed = append(removed, name)
		}
	}
	reloaded := map[string]struct{}{}
	for name := range applied {
		reloaded[name] = struct{}{}
	}
	for _, name := range removed {
		reloaded[name] = struct{}{}
	}
	var fates []plannedFate
	var recorded []recordedFactory
	for _, object := range h.objectList() {
		if _, ok := reloaded[object.served.plugin]; !ok {
			continue
		}
		entry, ok := table.types.get(object.served.contract.Name)
		if !ok || entry.kind != object.served.kind {
			fates = append(fates, plannedFate{end: true, object: object})
			continue
		}
		declared, err := recordFactory(object, entry, ctx)
		if err != nil {
			if !failing.has(entry.plugin) {
				failing.set(entry.plugin, err)
			}
			continue
		}
		recorded = append(recorded, recordedFactory{plugin: entry.plugin, recorded: declared})
		fates = append(fates, plannedFate{kind: object.reloadFate(), object: object, served: entry, requests: declared.requests})
	}
	if failing.len() > 0 {
		return stagingPass{failed: failing}
	}
	facets := newNamedMap[Facet]()
	var added, changed []string
	for _, plugin := range fresh {
		facets.set(plugin.Name, plugin.Facet)
		if h.plugins.has(plugin.Name) {
			changed = append(changed, plugin.Name)
		} else {
			added = append(added, plugin.Name)
		}
	}
	return stagingPass{
		plan:     &reloadPlan{staged: table, facets: facets, added: added, changed: changed, removed: removed, fates: fates},
		recorded: recorded,
	}
}

// closure is the closure rule: the changed plugins (new code applied) of plan with a recorded request that would not bind
// in the staged table because of a plugin that failed in this reload: its name served there by such a plugin's old code, or
// not served at all while some plugin failed (what a failed setup would have served is unknown). Requests to another host
// are not checked (a rollout's rules). Each such plugin with the error it keeps its old code for.
func (h *chordHost) closure(plan *reloadPlan, recorded []recordedFactory, failed *namedMap[error]) *namedMap[error] {
	kept := newNamedMap[error]()
	if failed.len() == 0 {
		return kept
	}
	for _, each := range recorded {
		if kept.has(each.plugin) || !plan.facets.has(each.plugin) {
			continue
		}
		for _, request := range each.recorded.requests {
			if unbound := h.unboundRequest(plan.staged, request, failed); unbound != "" {
				kept.set(each.plugin, fmt.Errorf("plugin %q: %s", each.plugin, unbound))
				break
			}
		}
	}
	return kept
}

// unboundRequest is why request (of a new factory) would not bind in table because of a plugin in failed; empty when it
// binds, is placed on another host, or does not bind for another reason (it waits as any request does).
func (h *chordHost) unboundRequest(table *staged, request request, failed *namedMap[error]) string {
	if request.kind == requestType {
		contract := request.contract
		name := contract.Name
		entry, _ := table.types.get(name)
		provider := ""
		if entry != nil {
			provider = entry.plugin
		}
		if entry != nil && !failed.has(provider) {
			return ""
		}
		// A remote type the object reaches on another host, or the delegate's (what the staged table does not serve): placed by the
		// rollout's rules, not this host's.
		if !h.placedHere(table, contract, request.params) {
			return ""
		}
		if entry != nil && h.wouldBind(entry, contract, request.params) {
			return ""
		}
		if entry == nil {
			return fmt.Sprintf("needs %s v%d, which no plugin serves now (a plugin failed)", name, contract.Version)
		}
		return fmt.Sprintf("needs %s v%d, which plugin %q serves on its old code", name, contract.Version, provider)
	}
	name := request.registry.Name
	entry, ok := table.registries.get(name)
	if ok && !failed.has(entry.plugin) {
		return ""
	}
	if ok && registryServes(entry, request.registry.Version) {
		return ""
	}
	if !ok {
		return fmt.Sprintf("needs registry %s v%d, which no plugin serves now (a plugin failed)", name, request.registry.Version)
	}
	return fmt.Sprintf("needs registry %s v%d, which plugin %q serves on its old code", name, request.registry.Version, entry.plugin)
}

// registryServes reports whether registry provider entry serves readers of version: its own, or an older one its `accepts`
// maps back.
func registryServes(entry stagedRegistry, version int) bool {
	provided := entry.registry.Version
	if provided == version {
		return true
	}
	if provided > version {
		accept, ok := entry.provider.Accepts[version]
		return ok && accept.Value != nil
	}
	return false
}

// wouldBind reports whether a request for typ with params binds to entry as a real bind checks it (`bind`): its kind,
// version and fingerprint, and the provider's own host address; one whose `hostAddress` fails does not.
func (h *chordHost) wouldBind(entry *served, typ *contracts.Contract, params *delta.JsonObject) bool {
	if unboundCause(entry, typ.Kind, typ.Name, typ.Version, typ.Fingerprint) != nil {
		return false
	}
	cause, err := h.misplacedServed(typ, entry, params)
	return err == nil && cause == nil
}

// placedHere reports whether a request for typ with params goes here, by what table serves (the delegate takes what it does not); a
// `hostAddress` that fails: not here (it fails at bind).
func (h *chordHost) placedHere(table *staged, typ *contracts.Contract, params *delta.JsonObject) bool {
	here, err := h.destination.placedHere(typ, params, table.types.has)
	return err == nil && here
}

// commit is the commit (slice 7b §3), one batch: the served types become the staged ones (an unchanged plugin's stay as
// they were), the removed plugins' registry providers go and the reloaded ones' are replaced, their facet contributions
// replaced by plugin, every changed registry composed once; a removed type's presence list goes. Then each object takes its
// fate, host-lifetime objects of new types start, and the publication wakes the requests waiting for the names the reload
// serves.
func (h *chordHost) commit(plan *reloadPlan, ctx context.Context) ([]startedFate, error) {
	table := plan.staged
	published := plan.facets
	if err := h.batch.Commit(func() error {
		for _, name := range slices.Concat(plan.changed, plan.removed) {
			loaded, _ := h.plugins.get(name)
			for _, registry := range loaded.staged.registries.keys() {
				if owner, ok := table.registries.get(registry); !ok || owner.plugin != name {
					h.registryRecord(registry).Unserve()
				}
			}
			for _, contribution := range loaded.contributions {
				contribution.Record.Remove(contribution)
			}
			h.plugins.delete(name)
		}
		h.types = table.types
		if err := h.publishPlugins(table, published); err != nil {
			return err
		}
		for _, name := range published.keys() {
			plugin, _ := table.plugins.get(name)
			for _, registry := range plugin.registries.keys() {
				if err := h.registryRecord(registry).ComposeServed(); err != nil {
					return err
				}
			}
		}
		return nil
	}, ctx); err != nil {
		return nil, err
	}
	h.reportUndeclared()
	for name := range h.refs {
		if !table.types.has(name) {
			delete(h.refs, name)
		}
	}
	// Pi's `names` is a `Set`: a name used both as a type and as a registry is in it once. Here it may be listed twice,
	// which is the same: `unbound.wake` takes a name's requests out of the index at its first occurrence, so the second
	// wakes nothing.
	var names []string
	for _, name := range table.types.keys() {
		if entry, _ := table.types.get(name); published.has(entry.plugin) {
			names = append(names, name)
		}
	}
	for _, name := range table.registries.keys() {
		if entry, _ := table.registries.get(name); published.has(entry.plugin) {
			names = append(names, name)
		}
	}
	started := h.startFates(plan.fates)
	for _, name := range table.types.keys() {
		entry, _ := table.types.get(name)
		if entry.kind == servedLocal && entry.hostLifetime {
			h.objectFor(entry, emptyParams, nil, ctx, noChain, nil)
		}
	}
	h.unbound.wake(names)
	return started, nil
}

// startFates starts fates and returns them in their order, for the report: the removals first (a dependent whose new code
// still needs a removed provider then parks, decision 4), then the restarts and retries; then the restarts' readiness and
// successors, once every object has its new served type (their requests bind against the new code).
func (h *chordHost) startFates(fates []plannedFate) []startedFate {
	started := make([]startedFate, len(fates))
	for index, fate := range fates {
		if fate.end {
			started[index] = h.startFate(fate)
		}
	}
	for index, fate := range fates {
		if !fate.end {
			started[index] = h.startFate(fate)
		}
	}
	for _, fate := range fates {
		if !fate.end && fate.kind == reloadRestart {
			fate.object.startSuccessor()
		}
	}
	// The report's waits, made now that every readiness started.
	for index := range started {
		if started[index].check != nil {
			started[index].checked = started[index].check()
		}
	}
	return started
}

// startFate starts one object's fate after the commit: a removed type's object ends, its holders waiting `not_served`; any
// other restarts or retries on its new served type.
func (h *chordHost) startFate(fate plannedFate) startedFate {
	object := fate.object
	if fate.end {
		err := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: object.served.contract.Name + " is not served here"}, nil)
		label := object.label()
		object.remove(data.FailureCause{Kind: data.FailureWaiting, Error: err})
		return startedFate{object: label, kind: ReloadEnded}
	}
	next := object.reload(fate.served, fate.kind, fate.requests)
	// Why a restarted or retried object's new attempt waits, for the report: what a restart's readiness found, at once (its new
	// attempt's own check follows the old attempt's end), also for a retry of an attempt that still waits for an open old
	// attempt; otherwise its new attempt's first dependency check.
	checked := func() *keyedPromise[*data.FailureCause] {
		if fate.kind == reloadRestart || object.openOldAttempt() != nil {
			now := newKeyedPromise[*data.FailureCause](h.loop())
			now.resolve(object.readinessCause())
			return now
		}
		return next.checked()
	}
	return startedFate{object: object.label(), kind: reloadedKinds[fate.kind], check: checked}
}

// reloadReport is the report, once every retry had its first dependency check (a parked one reports why); a restart reports
// at once what its readiness found (its successor's own check follows the old attempt's end). ctx's abort ends only this wait (`aborted`, its details
// naming what the commit applied: `committed`).
func (h *chordHost) reloadReport(started []startedFate, committed CommittedReload, ctx context.Context) ([]ReloadedObject, error) {
	// `awaitWithContext(Promise.all(checks), context)`: an abort that came first ends the wait.
	if ctx.Err() != nil {
		return nil, committedAborted(committed)
	}
	parking.Park(ctx)
	objects := make([]ReloadedObject, 0, len(started))
	for _, fate := range started {
		if fate.checked == nil {
			objects = append(objects, ReloadedObject{Object: fate.object, Kind: fate.kind})
			continue
		}
		select {
		case <-fate.checked.done:
		case <-ctx.Done():
			return nil, committedAborted(committed)
		}
		cause := fate.checked.value
		if cause == nil {
			objects = append(objects, ReloadedObject{Object: fate.object, Kind: fate.kind})
		} else {
			objects = append(objects, ReloadedObject{Object: fate.object, Kind: ReloadParked, Cause: *cause})
		}
	}
	// Pi's report completes at least three microtask ticks after a check settled (`await checked`, the async map's
	// result, `Promise.all`), so what the same batch delivered first (a watcher of a reference that left `live` aborting
	// the context from its callback) runs its listener's synchronous part before the report: the deliveries scheduled so far
	// have entered (their listener call returned, or went on to a wait the host knows), then the context is read. The wait is
	// not for a listener to finish a wait of its own: a listener may call `Reload` itself (its own delivery is entered and
	// skipped), and ctx's abort ends the wait (`aborted`, as the wait for the checks).
	h.enter(func() {})
	// pig divergence (D224): a listener that blocks on something the host cannot see, without calling chord.DeliveryEntered, holds
	// this wait back; Pi's listener awaits.
	if err := h.scheduler.AwaitEntered(ctx); err != nil {
		return nil, committedAborted(committed)
	}
	h.enter(func() {})
	if ctx.Err() != nil {
		return nil, committedAborted(committed)
	}
	return objects, nil
}

// committedAborted is the `aborted` error of a reload whose context aborted after its commit.
func committedAborted(committed CommittedReload) *data.ChordError {
	names := func(values []string) []any {
		list := make([]any, 0, len(values))
		for _, value := range values {
			list = append(list, value)
		}
		return list
	}
	details := delta.JsonObjectOf("committed", delta.JsonObjectOf(
		"added", names(committed.Added), "changed", names(committed.Changed),
		"removed", names(committed.Removed), "failed", names(committed.Failed),
	))
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeAborted, Message: "reload: aborted after its commit", Details: details, HasDetails: true}, nil)
}

// rewait leaves binding, whose object left it (its type removed, its version no longer bound: slice 7b §6), waiting
// unbound with cause under its type's name; a publication places it again (params checked as it was, under ctx).
func (h *chordHost) rewait(b *binding, typ *contracts.Contract, params checkedParams, cause data.FailureCause, ctx context.Context) {
	h.waitUnbound(b, typ.Name, &cause, func() { h.placeBinding(b, typ, params, ctx) })
}
