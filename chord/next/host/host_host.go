package host

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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
	// OnError is everything that fails in the background; every app handles it.
	OnError func(err HostError)
	// CloseTimeout is how long a native's close, and an ending object's running calls, are waited for; 30 s by default.
	CloseTimeout *time.Duration
	// ReadyTimeout is how long an activation attempt, and a `get`, may take until the object is live; 60 s by default.
	ReadyTimeout *time.Duration
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

// HostFacet is a facet `Load` runs under its plugin name.
type HostFacet struct {
	Name  string
	Facet Facet
}

// Host is a host.
type Host interface {
	// Load runs every facet's setup and publishes what they declared in one batch (the types and registries served, the
	// registries composed once, the contributions); requests that waited for the publication are placed after that batch.
	// Then activates every served type with empty params and returns when each has had its first outcome (the start
	// barrier, decision (a)): live, failed by its own cause, parked with no blocker here still without its first outcome,
	// or ended (failures go to `OnError`). A setup that fails returns its error and closes the host. Runs once.
	Load(facets []HostFacet, ctx context.Context) error
	// Get is a reference once its object is live (and hydrated). It acts as the host's principal and goes where its type's
	// host address puts it: here, over a route, or over the parent link (`destination`). It waits while the reference is
	// `loading` (before the host's publication too), `failed { transient }` or `failed { waiting }` until its
	// `ReadyTimeout`, then returns the last cause and releases the reference; a broken failure returns at once.
	Get(typ *contracts.Contract, params any, ctx context.Context, options GetOptions) (*Reference, error)
	// Hold is a reference at once, following its object until released: loading, live, failed (retried as its object is)
	// and loading again while its link is down; it never fails. It returns an error at once only for invalid params (also
	// before `Load`). A type not served where it goes is `loading` before the host's publication and `failed { waiting }`
	// after it (bound when a publication serves it); a `hostAddress` or route that refuses fails it broken.
	Hold(typ *contracts.Contract, params any, ctx context.Context, options GetOptions) (*Reference, error)
	// Accept is a link from a child: this host serves its remote types over it, every hold and call arriving on it acting
	// as options.Principal. ctx is the parent of the work arriving on it (its cancellation is dropped).
	Accept(link link.Link, options LinkOptions, ctx context.Context) error
	// Connect is this host's parent link: remote types not served here and not routed are held over it, every such
	// reference held again on a new one; holds arriving on it may name whom they act for. It fails while the previous one
	// is open; once it closed (a drop), Connect takes the next. options.Principal is who the parent is.
	Connect(link link.Link, options LinkOptions, ctx context.Context) error
	// Route is how this host reaches hosts of kind (called before `Load`, once per kind): Authorize admits every hold
	// routed over it, Resolve makes the link of an address. Holds for an address wait while it resolves and again after
	// its link drops (a backoff per address, 1 s doubling to 60 s), while they exist. ctx is the parent of the route's work,
	// aborted for it when this host closes.
	Route(kind *contracts.HostKind, options RouteOptions, ctx context.Context) error
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
	Registry(r registries.Registry[any, any], ctx context.Context, options GetOptions) (*registries.RegistryValueSource, error)
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

// pluginName is an npm package name, scoped or not.
var pluginName = regexp.MustCompile(`^(?:@[a-z0-9~-][a-z0-9._~-]*/)?[a-z0-9~-][a-z0-9._~-]*$`)

const (
	defaultCloseTimeout = 30 * time.Second
	defaultReadyTimeout = 60 * time.Second
	defaultPing         = 15 * time.Second
	defaultHeartbeat    = 30 * time.Second
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
	backoff   *backoff
	address   *contracts.Address
	principal data.Principal
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
	types   map[string]*served
	// objects are by identity, in the order they went live (the others where they were created).
	objects     map[string]*chordObject
	objectOrder []string
	// endings are teardowns still running, by identity: the next instance of an identity waits for its previous ones.
	endings map[string]*keyedPromise[struct{}]
	// typeOrder is the served types' names in the order they were served.
	typeOrder []string
	// unbound are the requests that cannot bind yet, by name: before the publication all of them, then those left unbound.
	unbound *unboundIndex
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
	events := options.loop
	if events == nil {
		events = NewLoop()
	}
	h := &chordHost{
		events:       events,
		scheduler:    chord.NewDeliveryScheduler(),
		address:      options.Address,
		principal:    options.Principal,
		principalKey: options.PrincipalKey,
		onError:      options.OnError,
		incarnation:  uuidv7(),
		compression:  options.Compression,
		refs:         map[string]*references{},
		types:        map[string]*served{},
		objects:      map[string]*chordObject{},
		endings:      map[string]*keyedPromise[struct{}]{},
		unbound:      newUnboundIndex(),
		publication:  make(chan struct{}),
		relays:       &relayMap{},
		routes:       map[string]*route{},
		registries:   map[string]*registries.RegistryRecord{},
		nextOrigin:   1,
	}
	var metrics telemetry.TelemetryMetrics
	if options.Telemetry != nil {
		metrics = options.Telemetry.Metrics()
	}
	h.batch = state.NewBatch(func(err state.BatchError) { h.report(BatchHostError(err)) }, metrics).
		UseScheduler(h.scheduler).UseMicrotasks(events.Queue).UseSection(events.Run)
	h.activity = newActivity(events, h.batch)
	h.backoff = newBackoff(events, h.due)
	// `revokedBy` is called with this host's principals only, the ones its links stamp.
	h.revocationKeys = newRevocationKeys(options.RevokedBy.erased())
	h.timeouts = hostTimeouts{close: durationOr(options.CloseTimeout, defaultCloseTimeout), ready: durationOr(options.ReadyTimeout, defaultReadyTimeout)}
	h.heartbeat.ping, h.heartbeat.close = defaultPing, defaultHeartbeat
	if options.Heartbeat != nil {
		h.heartbeat.ping = durationOr(options.Heartbeat.Ping, defaultPing)
		h.heartbeat.close = durationOr(options.Heartbeat.Close, defaultHeartbeat)
	}
	h.routing, h.routingCancel = chordctx.WithCancel(chordctx.Background())
	h.parent = newPath(func() {})
	h.unplaced = newPath(func() {})
	h.destination = newDestination(h.address, h.routes, h.parent)
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

// Incarnation is this run of the host (R20).
func (h *chordHost) Incarnation() string { return h.incarnation }

// Load implements Host.
func (h *chordHost) Load(facets []HostFacet, ctx context.Context) error {
	var failure error
	var setupFailed bool
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
		h.state = hostLoading
		contributions, err := h.setUpFacets(facets)
		if err == nil {
			// The publication: the facets' contributions and the first composition of every served registry, in one
			// batch, before any object activates.
			err = h.batch.Commit(func() error {
				for _, contribution := range contributions {
					contribution.Record.Add(contribution)
				}
				for _, name := range h.registryOrder {
					if err := h.registries[name].ComposeServed(); err != nil {
						return err
					}
				}
				return nil
			}, ctx)
		}
		if err != nil {
			// The host closes as `close` would: its links too. Nothing was activated, so no close can fail.
			failure, setupFailed = err, true
			return
		}
		// After the batch: what waited for the publication is placed (the gate lasted only for the setups).
		h.published_ = true
		h.publish()
		h.unbound.wakeAll()
		for _, name := range h.typeNames() {
			entry := h.types[name]
			if entry.contract.Kind == contracts.KindLocal && entry.hostLifetime {
				outcomes = append(outcomes, h.objectFor(entry, emptyParams, nil, ctx, noChain, nil).firstOutcome())
			}
		}
	})
	if setupFailed {
		var closing *closePromise
		h.enter(func() { closing = h.closeOnce() })
		<-closing.done
	}
	if failure != nil {
		return failure
	}
	for _, outcome := range outcomes {
		_, _ = outcome.await(ctx)
	}
	h.enter(func() {
		if h.state == hostLoading {
			h.state = hostLoaded
		}
	})
	return nil
}

// emptyParams are the params of a type with empty params, as checked: nothing, and the empty key.
var emptyParams = checkedParams{params: delta.NewJsonObject(0), key: ""}

// typeNames are the served types' names in the order they were served.
func (h *chordHost) typeNames() []string { return slices.Clone(h.typeOrder) }

// Get implements Host.
func (h *chordHost) Get(typ *contracts.Contract, params any, ctx context.Context, options GetOptions) (*Reference, error) {
	binding, err := h.acquire(typ, params, h.own(), ctx, ctx, options)
	if err != nil {
		return nil, err
	}
	// The binding was built from typ's members.
	return binding.reference, nil
}

// Hold implements Host.
func (h *chordHost) Hold(typ *contracts.Contract, params any, ctx context.Context, options GetOptions) (*Reference, error) {
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
func (h *chordHost) Route(kind *contracts.HostKind, options RouteOptions, ctx context.Context) error {
	var err error
	h.enter(func() {
		if err = h.assertOpen(); err != nil {
			return
		}
		if h.state != hostNew {
			err = errors.New("route: routes are declared before load")
			return
		}
		if _, ok := h.routes[kind.Kind]; ok {
			err = fmt.Errorf("route: %s is routed already", kind.Kind)
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
	})
	return err
}

// Revoke implements Host.
func (h *chordHost) Revoke(revocation delta.JsonValue, ctx context.Context) error {
	var failure error
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
			}
		}
	})
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
				found.record(object, holder)
			}
		}
		h.refs[typ.Name] = found
	})
	return found
}

// Registry implements Host.
func (h *chordHost) Registry(r registries.Registry[any, any], ctx context.Context, options GetOptions) (*registries.RegistryValueSource, error) {
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
	return h.roots.run(fn, principal, ctx)
}

// registryRecord is the record of registry name on this host, made by its first use.
func (h *chordHost) registryRecord(name string) *registries.RegistryRecord {
	record := h.registries[name]
	if record == nil {
		record = registries.NewRegistryRecord(name, h.batch, func(err registries.RegistryError) { h.report(RegistryHostError(err)) })
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

// published is whether the host published its setups (`load`'s batch): until then requests made here and holds arriving
// wait in the unbound index, placed by the publication.
func (h *chordHost) published() bool { return h.published_ }

// publish resolves the publication (once).
func (h *chordHost) publish() {
	if h.publishedOnce {
		return
	}
	h.publishedOnce = true
	close(h.publication)
}

// index: request cannot bind yet: indexed under name (the type or registry it waits for) until woken or ended.
func (h *chordHost) index(name string, request unboundRequest) { h.unbound.add(name, request) }

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
		live = held.live(waiting.context)
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

// count adds delta to the keep-alive count of kind (`host.activity`), where the work happens.
func (h *chordHost) count(kind activityKind, delta int) { h.activity.count(kind, delta) }

// referenced: holder references object: listed in its type's presence, if someone asked for it.
func (h *chordHost) referenced(object *chordObject, holder holder, ctx context.Context) {
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
	defer func() { _ = recover() }() // upstream: packages/chord/src/next/host/host.ts:ChordHost.report (a throwing onError is swallowed)
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
		remote := newRemoteObject(h, typ, params, placed.address, from, placed.path, b.dependent())
		placed.path.attach(remote)
		b.bind(remote)
		return remote, nil
	}
	entry := h.types[typ.Name]
	unbound := unboundCause(entry, typ.Kind, typ.Name, typ.Version, typ.Fingerprint)
	if unbound == nil {
		unbound, err = h.misplacedServed(typ, entry, params.params)
		if err != nil {
			return nil, err
		}
	}
	if unbound != nil {
		h.waitUnbound(b, typ.Name, unbound, func() { h.placeBinding(b, typ, params, ctx) })
		return nil, nil
	}
	// Checked above: served here at the caller's version.
	object := h.objectFor(entry, params, from.principal, ctx, noChain, b.dependent())
	b.bind(object)
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
	entry := h.types[request.Type]
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
	object := h.objectFor(entry, checked, principal, ctx, chain, nil)
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

// linkOpened: a link completed its handshake: a route's resets its address's backoff.
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
	h.unbound.add(name, b)
}

// newBindingFor is a new reference's binding made by from; its parked calls wait its `readyTimeout`.
func (h *chordHost) newBindingFor(typ *contracts.Contract, dependent *chordObject, from caller, options GetOptions) *binding {
	return newBinding(typ, h, h.batch, dependent, from, durationOr(options.ReadyTimeout, h.timeouts.ready))
}

// own is this host as the maker of its own references: its principal and incarnation.
func (h *chordHost) own() caller { return caller{principal: h.principal, incarnation: h.incarnation} }

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
	made := newHostLink(h, l, hostLinkOptions{role: role, principal: options.Principal, trusted: options.Trusted, codec: codec, limits: limits}, chordctx.WithoutAbortSignal(ctx))
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
	interval := min(ping, shut) / 10
	tick := func() {
		if ticking.stopped {
			return
		}
		now := h.events.Now()
		for _, each := range slices.Clone(h.links) {
			each.tick(now, ping, shut)
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

// objectFor is the object for params (checked by L1) and, for a remote type, principal (its key is part of the identity),
// created if there is none; its first attempt starts at once, or when its identity's backoff window ends. A new one keeps
// the activation chain it is created for (Q5): chain (a hold's), or that of dependent's activation when an activation
// declared it, so its own holds over links carry that chain on.
func (h *chordHost) objectFor(entry *served, params checkedParams, principal data.Principal, ctx context.Context, chain []string, dependent *chordObject) *chordObject {
	var identity string
	if entry.contract.Kind == contracts.KindLocal {
		identity = data.IdentityKey(entry.contract, params.key)
	} else {
		// A host serving remote types has a principal, which its references act as unless a link stamped its own.
		identity = data.IdentityKey(entry.contract, params.key, data.PrincipalKey(principal, ctx, h.principalKey))
	}
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
	for _, kind := range h.routeKinds {
		h.routes[kind].close()
	}
	for _, holder := range h.pathHolders() {
		if remote, ok := holder.(*remoteObject); ok {
			remote.lost(closed)
		}
	}
	// At the fence too: requests still waiting for a publication or a provider end as the others did, and gets waiting for
	// a publication that will not come fail now, not after the slow closes below (fix round 1).
	h.unbound.close(closed)
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
			clear(h.types)
			h.typeOrder = nil
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

// setUpFacets runs every facet's setup, checking the plugin names (npm names, each once), and serves what they declared;
// returns their contributions, which enter the registries' tables once the setups ran.
func (h *chordHost) setUpFacets(facets []HostFacet) ([]*registries.RegistryContribution, error) {
	names := map[string]struct{}{}
	var made []*registries.RegistryContribution
	for _, named := range facets {
		if !pluginName.MatchString(named.Name) {
			return nil, fmt.Errorf("%q is not a valid plugin name", named.Name)
		}
		if _, ok := names[named.Name]; ok {
			return nil, fmt.Errorf("plugin %q is loaded twice", named.Name)
		}
		names[named.Name] = struct{}{}
		set, err := setUp(named.Name, named.Facet, h.References)
		if err != nil {
			return nil, err
		}
		for _, entry := range set.served {
			if err := h.serveEntry(named.Name, entry); err != nil {
				return nil, err
			}
		}
		for _, each := range set.contributions {
			contribution, err := h.contribution(named.Name, each)
			if err != nil {
				return nil, err
			}
			made = append(made, contribution)
		}
	}
	return made, nil
}

// contribution is a facet's contribution, checked: in its registry's table while the host lives (its plugin stays loaded).
func (h *chordHost) contribution(plugin string, each facetContribution) (*registries.RegistryContribution, error) {
	// A facet contributes no registry's value (it reads none): no edges of its own.
	record := h.registryRecord(each.registry.Name)
	return contributionOf(record, registries.Contributor{Plugin: plugin}, each.registry.Version, each.value, nil)
}

func (h *chordHost) serveEntry(plugin string, entry servedEntry) error {
	if entry.kind == servedRegistry {
		return h.registryRecord(entry.registry.Name).Serve(entry.registry.Version, entry.provider)
	}
	contract := entry.contract
	if _, ok := h.types[contract.Name]; ok {
		return fmt.Errorf("%s is served twice", contract.Name)
	}
	// A client serves only local types (§3): a remote object is owned by a principal of the host serving it.
	if contract.Kind == contracts.KindRemote && h.principal == nil {
		return fmt.Errorf("%s: a host without a principal serves only local types", contract.Name)
	}
	// A remote object lives on the host its `hostAddress` names: only a host with an address can be that host.
	if contract.Kind == contracts.KindRemote && h.address == nil {
		return fmt.Errorf("%s: a host without an address serves only local types", contract.Name)
	}
	provider, err := data.BindProvider(contract, nil)
	if err != nil {
		return err
	}
	contractData, err := contracts.ContractDataOf(contract)
	if err != nil {
		return err
	}
	var local LocalFactory
	var remote RemoteFactory
	kind := servedLocal
	if contract.Kind == contracts.KindLocal {
		factory, ok := entry.factory.(LocalFactory)
		if !ok {
			return fmt.Errorf("%s: a local type is served with a LocalFactory, not %T", contract.Name, entry.factory)
		}
		local = factory
	} else {
		factory, ok := entry.factory.(RemoteFactory)
		if !ok {
			return fmt.Errorf("%s: a remote type is served with a RemoteFactory, not %T", contract.Name, entry.factory)
		}
		kind, remote = servedRemote, factory
	}
	h.types[contract.Name] = &served{
		kind:         kind,
		plugin:       plugin,
		provider:     provider,
		linger:       durationOr(entry.options.Linger, 0),
		contract:     contract,
		local:        local,
		remote:       remote,
		hostLifetime: contract.Kind == contracts.KindLocal && len(contractData.Params.Fields) == 0,
	}
	h.typeOrder = append(h.typeOrder, contract.Name)
	return nil
}

func (h *chordHost) assertOpen() error {
	if h.state == hostClosed {
		return h.closedError()
	}
	return nil
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
	if version == contract.Version && fingerprint == contract.Fingerprint {
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
