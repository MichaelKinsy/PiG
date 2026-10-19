package host

// Ports packages/chord/src/host.ts. Pi's Host interface and HostImpl class are one Go type, HostImpl: CreateHost
// returns it. Methods with a Promise result block until it settles (they must not be called on the host's loop); their
// synchronous part runs on the loop, as upstream's runs before its first await.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Serve registers a factory for a type (Pi's overloaded Serve over remote and local contracts).
type Serve func(typ *Contract, factory Factory, options ServeOptions) error

// FacetApi is what a facet's Setup gets. Serve registers types; Root and Registry give the facet's code that is not a
// Chord method, listener or task access to its own host, so plugins never need a process-global host. Both may be
// kept and used after Setup returns.
type FacetApi struct {
	Serve Serve
	// Name is the host's name (`server`, `session:S1`, ...).
	Name string
	// Signal is cancelled when this run of the facet is no longer installed: a reload that replaced or removed it
	// committed, the reload that loaded it aborted, or the host closed. While a reload prepares, the installed and the
	// candidate run are both live.
	Signal context.Context
	host   *HostImpl
}

// Root starts a root on the facet's host (in its current code version at call time).
func (api FacetApi) Root(fn func(ctx Ctx) (any, error), options RootOptions) (any, error) {
	return api.host.Root(fn, options)
}

// Registry is the facet's host's registry view (Get(key, ctx) reads the table of ctx's code version).
func (api FacetApi) Registry(registry Registry[any]) RegistryView[any] {
	return api.host.Registry(registry)
}

// FacetDefinition is what a facet module exports: the code that serves its types on one host kind.
type FacetDefinition struct {
	// Uses are the contract tokens this facet consumes; checked against providers at reload.
	Uses  []*Contract
	Setup func(api FacetApi) error
}

// Facet is a named facet. Modules are named by their URL; reloading a facet replaces everything it served.
type Facet struct {
	FacetDefinition
	Name string
}

// DefineFacetOptions are DefineFacet's options.
type DefineFacetOptions struct {
	Uses []*Contract
}

// DefineFacet is host.ts defineFacet.
func DefineFacet(setup func(api FacetApi) error, options DefineFacetOptions) FacetDefinition {
	return FacetDefinition{Setup: setup, Uses: options.Uses}
}

// FacetLoader imports facet modules (fresh copies on every call) and names them by module URL.
type FacetLoader func(modules []string) ([]Facet, error)

// PluginRef is a facet object (*Facet), or a module (ModuleRef: a path or URL) whose default export is a
// FacetDefinition.
type PluginRef interface{ isPluginRef() }

func (*Facet) isPluginRef() {}

// ModuleRef is a PluginRef naming a facet module by path or URL.
type ModuleRef string

func (ModuleRef) isPluginRef() {}

// Host kinds.
const (
	KindHost   = "host"
	KindClient = "client"
)

// HostOptions are CreateHost's options. Pi's principal type is a parameter and Chord invents no principals: the app
// supplies the host's own (Self), the one its parent's own requests act as (Upstream, Connect), the ones links act as
// (Accept) and the ones routed hosts act as (Route.Principal).
type HostOptions struct {
	// Name defaults to "host".
	Name string
	// Self is the principal of the host's own code: roots it starts, code inside its objects, and its `lifetime:
	// "host"` objects. Required: CreateHost panics with a TypeError unless Self is set (HasSelf for an explicit null).
	// It is compared by PrincipalKey, so an equal principal from elsewhere reaches the same objects.
	Self    Principal
	HasSelf bool
	// PrincipalKey maps a principal to its object key (should include connection provenance and grant revision).
	// Defaults to CanonicalJSON.
	PrincipalKey func(principal Principal) string
	// Dev validates every cell write and exposed state value against its schemas.
	Dev bool
	// OnError gets errors from background work (disposal, listeners, tasks). Defaults to writing them to stderr.
	OnError func(err error)
	// TaskStopDeadline is how long a reload waits for an object's aborted tasks to finish before it starts their
	// replacements anyway (reported). Nil is Pi's undefined: 5000 ms.
	TaskStopDeadline *time.Duration
	// ReadyTimeout is how long an awaited Get waits for its reference to be ready, and an object waits for the
	// references it holds to be ready and hydrated, before it fails unavailable/ready_timeout. Nil is Pi's undefined:
	// 60000 ms; math.MaxInt64 is Pi's Infinity (no timeout).
	ReadyTimeout *time.Duration
	// Linger keeps a remote reference's binding after its last reference was released (consumer side).
	Linger *LingerOptions
	// Loader is needed for Load/Reload with modules.
	Loader FacetLoader
	// Kind is KindClient (browser: never a reload participant, never carries roots or code versions) or KindHost (default).
	Kind string
	// Upstream is the link to this host's parent (server) and the principal its own requests act as. It can also be set
	// later with Connect.
	Upstream *UpstreamOptions
}

// LingerOptions are HostOptions.Linger: when the last reference to a remote object is released, its binding stays held
// for Types[type name] so a quick re-get reuses the hold and its hydrated state. At most Cap bindings (nil: 4) linger;
// beyond, the least recently released go first. Types not listed do not linger.
type LingerOptions struct {
	Types map[string]time.Duration
	Cap   *int
}

// UpstreamOptions are HostOptions.Upstream: the link to the parent with Connect's options.
type UpstreamOptions struct {
	Link Link
	ConnectOptions
}

// ConnectOptions are Connect's options: the link's, and the principal its parent's own requests act as.
type ConnectOptions struct {
	LinkOptions
	// Principal is what a request from the parent acts as when the parent does not forward its caller's principal (its
	// own code). Required: Connect panics with a TypeError unless it is set (HasPrincipal for an explicit null).
	Principal    Principal
	HasPrincipal bool
}

// Route is where requests for hosts named `prefix…` go (on the server).
type Route struct {
	// Authorize is checked before resolving (or spawning) and again after: grants may change meanwhile.
	Authorize func(principal Principal, hostName string) (bool, error)
	// Resolve is a link to the host, spawned if needed.
	Resolve func(hostName string) (Link, error)
	// Principal is the principal the resolved host acts as when it calls back. Required: Route panics with a TypeError
	// when it is nil.
	Principal func(hostName string) Principal
}

// PrincipalMatch is what InvalidatePrincipal matches: a Predicate (this host only), or a Pattern (fields that must be
// equal; propagates to child hosts). Exactly one is set.
type PrincipalMatch struct {
	Predicate func(principal Principal) bool
	Pattern   PrincipalPattern
}

// PrincipalPattern is host.ts PrincipalPattern<P>: the fields a principal must have, equal (DeepEqual), for an
// InvalidatePrincipal pattern. Pi's mapped type is P's own optional fields; Go's principal is untyped (Principal), so a
// pattern is the JSON object of the fields to match.
type PrincipalPattern = *chordjson.Object

// RootOptions are Root's options.
type RootOptions struct {
	// Principal is nil for the host principal.
	Principal Principal
	// Signal cancels the root; nil never cancels.
	Signal context.Context
	// CodeVersion starts the root in a retained code version (links carry code versions); nil is the current code version.
	CodeVersion *int
}

// LoadOptions are Install's and Load's options.
type LoadOptions struct {
	// AwaitHostObjects awaits the activation of the `lifetime: "host"` objects the facets serve (nil: true). False
	// starts them but leaves readiness to Ready (a child host whose objects need its parent, which admits it only
	// later).
	AwaitHostObjects *bool
}

// CodeVersionState is one retained code version.
type CodeVersionState struct {
	id       int
	services *orderedMap[string, *Service]
	tables   map[string]RegistryTable[any]
	// roots are the running roots with the number of their calls running here.
	roots map[string]int
	// abort is cancelled for every root of this code version (forced retirement).
	abort     context.Context
	abortWith context.CancelCauseFunc
}

func newCodeVersionState(id int, services *orderedMap[string, *Service]) *CodeVersionState {
	//portlint:allow ctxbackground host.ts CodeVersionState `abort: new AbortController()`: the code version's own controller
	abort, abortWith := context.WithCancelCause(context.Background())
	return &CodeVersionState{id: id, services: services, tables: map[string]RegistryTable[any]{}, roots: map[string]int{}, abort: abort, abortWith: abortWith}
}

// declsEntry is one (object, declarations) pair of a registry table build.
type declsEntry struct {
	object *ObjectImpl
	decls  *Decls
}

type registryListener struct {
	fn func(table RegistryTable[any], codeVersion int)
}

// HostImpl is a Chord host.
type HostImpl struct {
	loop

	name      string
	dev       bool
	principal Principal
	// selfKey is principalKey(self): an equal principal is the host's own, whatever object carries it.
	selfKey      string
	inFactory    int
	failed       error
	objects      *orderedMap[string, *ObjectImpl]
	codeVersions *orderedMap[int, *CodeVersionState]
	facets       *orderedMap[string, Facet]
	// hostObjects are the host's own references to its `lifetime: "host"` objects, by type name.
	hostObjects *orderedMap[string, RefCore]
	kind        string
	// connections are the open links, in the order they were added.
	connections *orderedMap[*Connection, struct{}]
	upstream    *Connection
	// bindings are the shared bindings of this host's remote references, by key (re-held after an upstream reconnect).
	bindings *orderedMap[string, *RemoteBinding]
	// lingering are the bindings without references that linger, least recently released first.
	lingering   *orderedMap[string, *RemoteBinding]
	lingerTypes map[string]time.Duration
	lingerCap   int
	// lingerAfter arms a lingering binding's close timer: host.after (setTimeout), replaced only by tests that drive the linger.
	lingerAfter  func(time.Duration, func()) func()
	originSeq    int
	readyTimeout time.Duration
	routes       map[string]Route
	childReloads *ChildReloads
	// remoteModules are child facet modules by host-name prefix, from every committed cluster reload (for reconciling
	// children); remoteRemoved are the removal tombstones that also reconcile children that connected during a
	// transaction.
	remoteModules map[string][]string
	remoteRemoved map[string][]string
	// retireAllowed is the highest code version the parent has retired.
	retireAllowed int
	// foreign are the roots started on another host, by the links they came over.
	foreign *orderedMap[string, *orderedMap[*Connection, struct{}]]
	// local are the roots started on this host that have not ended.
	local       map[string]struct{}
	incarnation string
	// rootLinks are the links each root's calls went out on.
	rootLinks map[string]*orderedMap[*Connection, struct{}]
	current   *CodeVersionState
	previous  *CodeVersionState
	// pending is the code version a running reload will commit; new types activated during prepare install their implementation for
	// it.
	pending *CodeVersionState
	// gating are the types behind a reload gate: new objects of these types are rejected upgrade_conflict until
	// commit/abort. Nil is no gate.
	gating    map[string]bool
	reloading bool
	// taskStopDeadline is how long a reload waits for aborted tasks to finish before it starts their replacements.
	taskStopDeadline time.Duration
	// retirementWatchers are called when a root ends, the parent allows a retirement, or the previous code version retires.
	retirementWatchers *orderedMap[*retirementWatcher, struct{}]
	// deferredHellos are the child hosts whose hello arrived while a reload ran, with the code version they announced: synced or
	// reconciled once it ends.
	deferredHellos *orderedMap[*Connection, int]
	// reloadWaiters are called when the running reload ends.
	reloadWaiters []func()
	// facetRuns are the installed runs of the facets by name: cancelled when the run is replaced, removed, or the host
	// closes.
	facetRuns      *orderedMap[string, *facetRun]
	principalKey   func(principal Principal) string
	onError        func(err error)
	loader         FacetLoader
	dying          map[string]*ObjectImpl
	touches        map[string]*orderedMap[*ObjectImpl, struct{}]
	order          map[string]map[string]int
	subscribers    *orderedMap[string, *orderedMap[*registryListener, struct{}]]
	codeVersionSeq int
	rootSeq        int
}

// CreateHost is host.ts createHost. It panics with a TypeError, as upstream throws one, unless options carry Self.
func CreateHost(options HostOptions) *HostImpl {
	if options.Self == nil && !options.HasSelf {
		panic(typeError("createHost needs `self`: the principal of the host's own code"))
	}
	host := &HostImpl{
		name:               options.Name,
		dev:                options.Dev,
		principal:          options.Self,
		objects:            newOrderedMap[string, *ObjectImpl](),
		codeVersions:       newOrderedMap[int, *CodeVersionState](),
		facets:             newOrderedMap[string, Facet](),
		hostObjects:        newOrderedMap[string, RefCore](),
		connections:        newOrderedMap[*Connection, struct{}](),
		bindings:           newOrderedMap[string, *RemoteBinding](),
		lingering:          newOrderedMap[string, *RemoteBinding](),
		routes:             map[string]Route{},
		remoteModules:      map[string][]string{},
		remoteRemoved:      map[string][]string{},
		foreign:            newOrderedMap[string, *orderedMap[*Connection, struct{}]](),
		local:              map[string]struct{}{},
		incarnation:        newIncarnation(),
		rootLinks:          map[string]*orderedMap[*Connection, struct{}]{},
		retirementWatchers: newOrderedMap[*retirementWatcher, struct{}](),
		deferredHellos:     newOrderedMap[*Connection, int](),
		facetRuns:          newOrderedMap[string, *facetRun](),
		principalKey:       options.PrincipalKey,
		onError:            options.OnError,
		loader:             options.Loader,
		dying:              map[string]*ObjectImpl{},
		touches:            map[string]*orderedMap[*ObjectImpl, struct{}]{},
		order:              map[string]map[string]int{},
		subscribers:        newOrderedMap[string, *orderedMap[*registryListener, struct{}]](),
		codeVersionSeq:     1,
		kind:               options.Kind,
	}
	if host.name == "" {
		host.name = "host"
	}
	if host.principalKey == nil {
		host.principalKey = CanonicalJSON
	}
	host.selfKey = host.principalKey(options.Self)
	if host.onError == nil {
		host.onError = func(err error) { fmt.Fprintln(os.Stderr, "chord:", err) }
	}
	if host.kind == "" {
		host.kind = KindHost
	}
	host.childReloads = newChildReloads(host)
	host.taskStopDeadline = deadlineOr(options.TaskStopDeadline)
	host.readyTimeout = defaultReadyTimeout
	if options.ReadyTimeout != nil {
		host.readyTimeout = *options.ReadyTimeout
	}
	host.lingerCap = defaultLingerCap
	host.lingerAfter = host.after
	if options.Linger != nil {
		host.lingerTypes = options.Linger.Types
		if options.Linger.Cap != nil {
			host.lingerCap = *options.Linger.Cap
		}
	}
	host.current = newCodeVersionState(host.codeVersionSeq, newOrderedMap[string, *Service]())
	host.codeVersions.Set(host.current.id, host.current)
	if options.Upstream != nil {
		host.Connect(options.Upstream.Link, options.Upstream.ConnectOptions)
	}
	return host
}

const (
	// defaultReadyTimeout is Pi's `options.readyTimeout ?? 60_000`.
	defaultReadyTimeout = 60 * time.Second
	// defaultLingerCap is Pi's `options.linger?.cap ?? 4`.
	defaultLingerCap = 4
	// neverReady is a ReadyTimeout that never fires (Pi's Infinity: `Number.isFinite(ms)` is false).
	neverReady = time.Duration(math.MaxInt64)
)

// isSelf reports whether principal is the host's own: the same key as Self.
func (host *HostImpl) isSelf(principal Principal) bool {
	return identical(principal, host.principal) || host.principalKey(principal) == host.selfKey
}

// newIncarnation keeps root ids unique across restarts of a host with the same name (six base-36 digits, as
// Math.random().toString(36).slice(2, 8)).
func newIncarnation() string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	var out [6]byte
	for index := range out {
		out[index] = digits[rand.IntN(len(digits))]
	}
	return string(out[:])
}

// Name is the host's name.
func (host *HostImpl) Name() string { return host.name }

// CodeVersion is the current code version.
func (host *HostImpl) CodeVersion() int {
	var codeVersion int
	host.run(func() { codeVersion = host.current.id })
	return codeVersion
}

// PreviousCodeVersion is the retained previous code version; ok is false when there is none.
func (host *HostImpl) PreviousCodeVersion() (codeVersion int, ok bool) {
	host.run(func() {
		if host.previous != nil {
			codeVersion, ok = host.previous.id, true
		}
	})
	return codeVersion, ok
}

// nextCodeVersionId allocates the next code version id, or adopts one issued by the coordinating server.
func (host *HostImpl) nextCodeVersionId(given *int) (int, error) {
	if given != nil && *given <= host.current.id {
		return 0, fmt.Errorf("code version %d is not newer than %d", *given, host.current.id)
	}
	if given != nil {
		host.codeVersionSeq = *given
	} else {
		host.codeVersionSeq++
	}
	return host.codeVersionSeq, nil
}

func (host *HostImpl) report(err error) { host.onError(err) }

// ─── serving ───

// Serve registers a factory for a type on the current code version.
func (host *HostImpl) Serve(typ *Contract, factory Factory, options ServeOptions) (err error) {
	host.run(func() {
		if host.reloading {
			err = errors.New("host.serve during a reload; serve from a facet passed to reload()")
			return
		}
		var service *Service
		if service, err = makeService(typ, factory, options, ""); err != nil {
			return
		}
		if err = host.serveInto(host.current.services, service); err != nil {
			return
		}
		if options.Lifetime == LifetimeHost {
			service, _ := host.current.services.Get(typ.Name)
			_, err = host.startHostObject(service)
		}
	})
	return err
}

// Install installs facet synchronously, then awaits its host objects unless AwaitHostObjects is false.
func (host *HostImpl) Install(facet Facet, options LoadOptions) error {
	var ready *Promise[struct{}]
	var err error
	host.run(func() { ready, err = host.install(facet, options) })
	if err != nil {
		return err
	}
	//portlint:allow ctxbackground host.ts install has no AbortSignal: `await` of the host objects' readiness, which settles on the loop
	_, err = ready.Await(context.Background())
	return err
}

func (host *HostImpl) install(facet Facet, options LoadOptions) (*Promise[struct{}], error) {
	if host.reloading {
		return nil, errors.New("host.install during a reload")
	}
	if host.facets.Has(facet.Name) {
		return nil, fmt.Errorf("facet %s is already installed; use reload()", facet.Name)
	}
	var added []*Service
	serve := Serve(func(typ *Contract, factory Factory, options ServeOptions) (err error) {
		host.run(func() {
			var service *Service
			if service, err = makeService(typ, factory, options, facet.Name); err != nil {
				return
			}
			if err = host.serveInto(host.current.services, service); err == nil {
				added = append(added, service)
			}
		})
		return err
	})
	run := newFacetRun()
	if err := facet.Setup(host.facetApi(serve, run.signal)); err != nil {
		run.cancel(err)
		return nil, err
	}
	host.facets.Set(facet.Name, facet)
	host.facetRunChanged(facet.Name, run)
	var refs []RefCore
	for _, service := range added {
		if !hostLifetime(service) {
			continue
		}
		ref, err := host.startHostObject(service)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	if options.AwaitHostObjects != nil && !*options.AwaitHostObjects {
		return resolvedPromise(&host.loop, struct{}{}), nil
	}
	return host.hostObjectsReady(refs), nil
}

// Load installs all facets before awaiting their host objects; referenced objects are created when first referenced.
func (host *HostImpl) Load(plugins []PluginRef, options LoadOptions) error {
	facets, err := host.resolve(plugins)
	if err != nil {
		return err
	}
	var ready []*Promise[struct{}]
	// No await between installs: a host object's factory may consume a type served by a later facet.
	host.run(func() {
		for _, facet := range facets {
			promise, installErr := host.install(facet, options)
			if installErr != nil {
				err = installErr
				return
			}
			ready = append(ready, promise)
		}
	})
	if err != nil {
		return err
	}
	//portlint:allow ctxbackground host.ts load has no AbortSignal: `await Promise.all` of the host objects' readiness
	_, err = promiseAll(&host.loop, ready).Await(context.Background())
	return err
}

// Ready awaits the host objects present at call time, including those started by Serve or deferred loads.
func (host *HostImpl) Ready() error {
	var ready *Promise[struct{}]
	host.run(func() { ready = host.hostObjectsReady(host.hostObjects.Values()) })
	//portlint:allow ctxbackground host.ts ready() takes no AbortSignal: `await` of the host objects' readiness
	_, err := ready.Await(context.Background())
	return err
}

func (host *HostImpl) hostObjectsReady(refs []RefCore) *Promise[struct{}] {
	promises := make([]*Promise[struct{}], len(refs))
	for index, ref := range refs {
		promises[index] = ref.readyPromise()
	}
	return promiseAll(&host.loop, promises)
}

// resolve turns plugin references into facets, importing modules through the loader. It blocks on the loader and
// runs off the loop.
func (host *HostImpl) resolve(plugins []PluginRef) ([]Facet, error) {
	var modules []string
	for _, ref := range plugins {
		if module, ok := ref.(ModuleRef); ok {
			modules = append(modules, string(module))
		}
	}
	if len(modules) > 0 && host.loader == nil {
		return nil, errors.New("loading modules needs createHost({ loader }), e.g. nodeLoader from @earendil-works/chord/node")
	}
	var loaded []Facet
	if len(modules) > 0 {
		var err error
		if loaded, err = host.loader(modules); err != nil {
			return nil, err
		}
		if len(loaded) < len(modules) {
			return nil, fmt.Errorf("facet loader returned %d facets for %d modules", len(loaded), len(modules))
		}
	}
	facets := make([]Facet, 0, len(plugins))
	for _, ref := range plugins {
		if facet, ok := ref.(*Facet); ok {
			facets = append(facets, *facet)
			continue
		}
		facets = append(facets, loaded[0])
		loaded = loaded[1:]
	}
	return facets, nil
}

func (host *HostImpl) facetApi(serve Serve, signal context.Context) FacetApi {
	return FacetApi{Serve: serve, Name: host.name, Signal: signal, host: host}
}

// facetRun is one run of a facet's Setup: its signal (FacetApi.Signal) is cancelled once the run is no longer installed.
type facetRun struct {
	signal context.Context
	cancel context.CancelCauseFunc
}

func newFacetRun() *facetRun {
	//portlint:allow ctxbackground host.ts facetApi `const run = new AbortController()`: the facet run's own controller
	signal, cancel := context.WithCancelCause(context.Background())
	return &facetRun{signal: signal, cancel: cancel}
}

// facetRunChanged: the installed run of facet name is now run (nil: none); the run it replaces is cancelled.
func (host *HostImpl) facetRunChanged(name string, run *facetRun) {
	replaced, _ := host.facetRuns.Get(name)
	if run == nil {
		host.facetRuns.Delete(name)
	} else {
		host.facetRuns.Set(name, run)
	}
	if replaced != nil && replaced != run {
		replaced.cancel(NewChordError(CodeGone, fmt.Sprintf("facet %s was replaced or removed", name), ChordErrorOptions{}))
	}
}

func (host *HostImpl) serveInto(services *orderedMap[string, *Service], service *Service) error {
	if services.Has(service.contract.Name) {
		return fmt.Errorf("%s is already served", service.contract.Name)
	}
	if placement := service.contract.Description.Host; placement != nil && !placedOn(placement, host.name) {
		return typeError("%s is declared for %s, but %s serves it", service.contract.Name, placementText(placement), host.name)
	}
	services.Set(service.contract.Name, service)
	return nil
}

func (host *HostImpl) startHostObject(service *Service) (RefCore, error) {
	ref, err := host.acquire(service.contract, map[string]any{}, nil, "host", nil, false)
	if err != nil {
		return nil, err
	}
	host.hostObjects.Set(service.contract.Name, ref)
	ref.readyPromise().then(func(_ struct{}, err error) {
		if err != nil {
			host.report(err)
		}
	})
	return ref, nil
}

// ─── object runtime (upstream HostCore) ───

func (host *HostImpl) currentCodeVersion() int { return host.current.id }

func (host *HostImpl) hasCodeVersion(codeVersion int) bool { return host.codeVersions.Has(codeVersion) }

// retainedCodeVersions are the retained code versions' services, then the pending code version's.
func (host *HostImpl) retainedCodeVersions() func(yield func(int, *orderedMap[string, *Service]) bool) {
	return func(yield func(int, *orderedMap[string, *Service]) bool) {
		for _, state := range host.codeVersions.Values() {
			if !yield(state.id, state.services) {
				return
			}
		}
		if host.pending != nil {
			yield(host.pending.id, host.pending.services)
		}
	}
}

func (host *HostImpl) pendingCodeVersion() (int, bool) {
	if host.pending == nil {
		return 0, false
	}
	return host.pending.id, true
}

// service is the current code for a type; a type a running reload adds is served from the pending code version.
func (host *HostImpl) service(typeName string) *Service {
	if service, ok := host.current.services.Get(typeName); ok {
		return service
	}
	if host.pending != nil {
		service, _ := host.pending.services.Get(typeName)
		return service
	}
	return nil
}

// addressOf is the address of a reference to typ with input for principal.
func (host *HostImpl) addressOf(typ *Contract, input any, principal Principal, candidate bool) (string, error) {
	elsewhere, err := host.elsewhere(typ, input)
	if err != nil {
		return "", err
	}
	if elsewhere != nil {
		return host.remoteKey(typ, elsewhere.input, principal), nil
	}
	service := host.bindingService(typ.Name, candidate)
	if service == nil {
		return "", unknownType(typ.Name, host.name)
	}
	where, err := host.locate(service, input, principal)
	if err != nil {
		return "", err
	}
	return where.address, nil
}

// placed is where an object of a remote type lives when that is another host: its name and the input normalized with
// the caller's contract (the placement is read from it).
type placed struct {
	at    string
	input any
}

// elsewhere is where an object of typ lives when that is another host; nil is here (and every local type).
func (host *HostImpl) elsewhere(typ *Contract, input any) (*placed, error) {
	placement := typ.Description.Host
	if typ.Scope != ScopeRemote || placement == nil {
		return nil, nil
	}
	normalized, err := Normalize(typ.Input, input, typ.Name+" input")
	if err != nil {
		return nil, err
	}
	at, err := resolveHost(typ.Name, placement, normalized)
	if err != nil {
		return nil, err
	}
	if at == host.name {
		return nil, nil
	}
	return &placed{at: at, input: normalized}, nil
}

// located is where an input of a service lives: its address, the normalized input and the principal the object is
// created for.
type located struct {
	address    string
	normalized any
	principal  Principal
}

// locate is the address of input for principal, normalized. A `lifetime: "host"` type has one object: the empty
// input, for the host principal, whoever asks; another input is invalid.
func (host *HostImpl) locate(service *Service, input any, principal Principal) (located, error) {
	name, schema := service.contract.Name, service.contract.Input
	normalized, err := Normalize(schema, input, name+" input")
	if err != nil {
		return located{}, err
	}
	identity := CanonicalIdentity(schema, normalized)
	if hostLifetime(service) {
		empty, err := Normalize(schema, map[string]any{}, name+" input")
		if err != nil {
			return located{}, err
		}
		if identity != CanonicalIdentity(schema, empty) {
			return located{}, Invalid("input", name+` has lifetime "host": its one object has the empty input`, nil)
		}
		principal = host.principal
	}
	return located{address: name + "\x00" + identity + "\x00" + host.principalPart(principal), normalized: normalized, principal: principal}, nil
}

// principalPart is the principal's part of an address: "host" for the host's own.
func (host *HostImpl) principalPart(principal Principal) string {
	if host.isSelf(principal) {
		return "host"
	}
	return "p:" + host.principalKey(principal)
}

// remoteKey is the key of a remote binding: type, input identity (fields equal to their defaults omitted, as for local
// objects), principal key and the consumer's contract fingerprint. No host name: the type and the input decide where it
// lives.
func (host *HostImpl) remoteKey(typ *Contract, normalized any, principal Principal) string {
	return "@" + typ.Name + "\x00" + CanonicalIdentity(typ.Input, normalized) + "\x00" + host.principalPart(principal) + "\x00" + typ.Fingerprint
}

func (host *HostImpl) acquire(typ *Contract, input any, options *GetOptions, owner RefOwner, from *ObjectImpl, candidate bool) (RefCore, error) {
	elsewhere, err := host.elsewhere(typ, input)
	if err != nil {
		return nil, err
	}
	if elsewhere != nil {
		return host.acquireRemote(typ, elsewhere.at, elsewhere.input, options, owner)
	}
	service := host.bindingService(typ.Name, candidate)
	if service == nil {
		return nil, unknownType(typ.Name, host.name)
	}
	if _, err := Bind(service.contract, service.options.Accepts, typ); err != nil {
		return nil, err
	}
	principal := host.principal
	if options != nil && options.Principal != nil {
		principal = options.Principal
	}
	where, err := host.locate(service, input, principal)
	if err != nil {
		return nil, err
	}
	address, normalized, principal := where.address, where.normalized, where.principal
	// The provider's own placement decides, never where a request says it is going (a client picks that).
	if placement := service.contract.Description.Host; placement != nil {
		at, err := resolveHost(typ.Name, placement, normalized)
		if err != nil {
			return nil, err
		}
		if at != host.name {
			return nil, Invalid("placement", fmt.Sprintf("%s with this input lives on %s, not %s", typ.Name, at, host.name), nil)
		}
	}
	object, ok := host.objects.Get(address)
	if from != nil && ok && (object == from || reaches(object, from)) {
		return nil, Invalid("cycle", fmt.Sprintf("%s -> %s would create a ref cycle", from.address, address), nil)
	}
	if !ok {
		if host.gating[typ.Name] {
			return nil, NewChordError(CodeUpgradeConflict, typ.Name+" is being upgraded; retry", ChordErrorOptions{Reason: "gate"})
		}
		dying := host.dying[address]
		if dying != nil && dying.status == "disposed" {
			delete(host.dying, address)
		}
		object = newObjectImpl(host, ObjectIdentity{
			Address:      address,
			TypeName:     typ.Name,
			Input:        normalized,
			Principal:    principal,
			HostLifetime: hostLifetime(service),
		})
		host.objects.Set(address, object)
		// A new object for an address still being disposed waits for that cleanup (exclusive resources).
		var after *Promise[struct{}]
		if dying != nil && dying.status == "disposing" {
			after = dying.dispose(false, nil)
		}
		object.start(after)
	}
	ref := newRefImpl(object, typ, owner)
	object.addRef(ref)
	return ref, nil
}

func (host *HostImpl) forget(object *ObjectImpl) {
	if current, ok := host.objects.Get(object.address); ok && current == object {
		host.objects.Delete(object.address)
	}
	if object.status == "disposing" {
		host.dying[object.address] = object
	}
}

// disposed: object finished its disposal; the dying table no longer holds it.
func (host *HostImpl) disposed(object *ObjectImpl) {
	if host.dying[object.address] == object {
		delete(host.dying, object.address)
	}
}

func (host *HostImpl) touch(rootID string, object *ObjectImpl) {
	touched := host.touches[rootID]
	if touched == nil {
		touched = newOrderedMap[*ObjectImpl, struct{}]()
		host.touches[rootID] = touched
	}
	touched.Set(object, struct{}{})
}

// ─── roots ───

// Root runs fn in a new root of this host, in options.CodeVersion (default: the current code version at call time).
func (host *HostImpl) Root(fn func(ctx Ctx) (any, error), options RootOptions) (any, error) {
	var rootID string
	var codeVersion int
	host.run(func() {
		rootID = host.newRootID()
		codeVersion = host.current.id
		if options.CodeVersion != nil {
			codeVersion = *options.CodeVersion
		}
	})
	return host.joinRoot(rootID, codeVersion, fn, joinOptions{principal: options.Principal, signal: options.Signal})
}

// newRootID makes the id of a new root started here, on the loop.
func (host *HostImpl) newRootID() string {
	host.rootSeq++
	// The incarnation keeps root ids unique across restarts of a host with the same name.
	rootID := host.name + "#" + host.incarnation + ":" + strconv.Itoa(host.rootSeq)
	host.local[rootID] = struct{}{}
	return rootID
}

type joinOptions struct {
	principal Principal
	signal    context.Context
	from      *Connection
}

// joinRoot runs fn inside root rootID of codeVersionID. A root counts as running here (for retirement) while any of its
// calls runs here. A root that started here ends when its last call here settles, even if it went out and came back
// over a link meanwhile. A foreign root (started elsewhere, arriving from a link) keeps its leases on objects until
// every link it came over announced its end (root_end) or closed, so its re-entry stays grandfathered. fn runs on the
// caller's goroutine, off the loop.
func (host *HostImpl) joinRoot(rootID string, codeVersionID int, fn func(ctx Ctx) (any, error), options joinOptions) (value any, err error) {
	var codeVersion *CodeVersionState
	var ctx *CtxImpl
	host.run(func() { codeVersion, ctx, err = host.enterRoot(rootID, codeVersionID, options) })
	if err != nil {
		return nil, err
	}
	defer host.run(func() { host.leaveRoot(codeVersion, ctx) })
	return fn(ctx)
}

// enterRoot is joinRoot's synchronous start, on the loop: it counts the call as running in the root and makes its ctx.
func (host *HostImpl) enterRoot(rootID string, codeVersionID int, options joinOptions) (*CodeVersionState, *CtxImpl, error) {
	codeVersion, ok := host.codeVersions.Get(codeVersionID)
	if !ok {
		delete(host.local, rootID)
		return nil, nil, NewChordError(CodeSuperseded, fmt.Sprintf("code version %d is not retained by %s", codeVersionID, host.name), ChordErrorOptions{})
	}
	if _, local := host.local[rootID]; options.from != nil && !local {
		links, ok := host.foreign.Get(rootID)
		if !ok {
			links = newOrderedMap[*Connection, struct{}]()
			host.foreign.Set(rootID, links)
		}
		links.Set(options.from, struct{}{})
	}
	codeVersion.roots[rootID]++
	// Forced retirement aborts what still runs on an code version before closing what only it used.
	signals := []context.Context{codeVersion.abort}
	if options.signal != nil {
		signals = []context.Context{options.signal, codeVersion.abort}
	}
	principal := options.principal
	if principal == nil {
		principal = host.principal
	}
	return codeVersion, newCtxImpl(host, ctxFields{signals: signals, rootID: rootID, codeVersion: codeVersion.id, principal: principal}), nil
}

// leaveRoot is joinRoot's finally, on the loop: the call settled; the root ends with its last call here.
func (host *HostImpl) leaveRoot(codeVersion *CodeVersionState, ctx *CtxImpl) {
	rootID := ctx.rootID
	ctx.close()
	count, ok := codeVersion.roots[rootID]
	if !ok {
		count = 1
	}
	if count-1 > 0 {
		codeVersion.roots[rootID] = count - 1
		return
	}
	delete(codeVersion.roots, rootID)
	if !host.foreign.Has(rootID) {
		host.endRoot(rootID)
	}
	host.maybeRetire()
	host.retirementChanged()
}

// rootSent remembers that calls of rootID went out on connection, which must hear when the root ends.
func (host *HostImpl) rootSent(rootID string, connection *Connection) {
	links := host.rootLinks[rootID]
	if links == nil {
		links = newOrderedMap[*Connection, struct{}]()
		host.rootLinks[rootID] = links
	}
	links.Set(connection, struct{}{})
}

// endRoot ends a root (here, or announced by its origin): it ends its leases and tells the links it went out on.
func (host *HostImpl) endRoot(rootID string) {
	delete(host.local, rootID)
	host.foreign.Delete(rootID)
	if touched := host.touches[rootID]; touched != nil {
		for _, object := range touched.Keys() {
			object.rootEnded(rootID)
		}
	}
	delete(host.touches, rootID)
	if links := host.rootLinks[rootID]; links != nil {
		for _, connection := range links.Keys() {
			connection.send(RootEndFrame{RootID: rootID})
		}
	}
	delete(host.rootLinks, rootID)
}

// foreignRootEnded references from announcing the end of a foreign root (or closing). It is honored only if the root came
// over from: a peer cannot end leases of roots it never carried. The root ends when no link that brought it remains.
func (host *HostImpl) foreignRootEnded(rootID string, from *Connection) {
	links, ok := host.foreign.Get(rootID)
	if !ok || !links.Delete(from) || links.Len() > 0 {
		return
	}
	running := false
	for _, codeVersion := range host.codeVersions.Values() {
		if _, ok := codeVersion.roots[rootID]; ok {
			running = true
			break
		}
	}
	// Still running here (a call in flight): the lease ends when it settles.
	if running {
		host.foreign.Delete(rootID)
	} else {
		host.endRoot(rootID)
	}
}

// maybeRetire retires the previous code version once no local roots of it remain. A child also waits for its parent to retire
// it: roots span hosts, and an old root on the server may still call in.
func (host *HostImpl) maybeRetire() {
	if host.retirable() {
		host.retire()
	}
}

// retirable: the previous code version has no roots left here and, on a child host, the parent has retired it.
func (host *HostImpl) retirable() bool {
	previous := host.previous
	if previous == nil || len(previous.roots) > 0 {
		return false
	}
	return host.kind == KindClient || host.upstream == nil || previous.id <= host.retireAllowed
}

func (host *HostImpl) allowRetire(codeVersion int) {
	host.retireAllowed = max(host.retireAllowed, codeVersion)
	host.maybeRetire()
	host.retirementChanged()
}

// retirementWatcher is a watchRetirement registration; its pointer is its identity.
type retirementWatcher struct{ fn func() }

// watchRetirement calls watcher (on the loop) whenever a root ends, the parent allows a retirement, or an code version
// retires. The returned function stops it.
func (host *HostImpl) watchRetirement(watcher func()) (stop func()) {
	entry := &retirementWatcher{fn: watcher}
	host.retirementWatchers.Set(entry, struct{}{})
	return func() { host.retirementWatchers.Delete(entry) }
}

func (host *HostImpl) retirementChanged() {
	for _, entry := range host.retirementWatchers.Keys() {
		entry.fn()
	}
}

// announceRetired tells a child that is now in step which code versions this host retired before: every id below the oldest
// one it retains (ids only grow). Without it, a child reconciled after those retirements would never hear a retire
// frame for its own previous code version and would wait for the retire deadline at every reload.
func (host *HostImpl) announceRetired(connection *Connection) {
	oldest := host.current
	if host.previous != nil {
		oldest = host.previous
	}
	if retired := oldest.id - 1; retired > 0 {
		connection.send(RetireFrame{CodeVersion: retired})
	}
}

// retire drops the previous code version: its implementations and the declarations only it used.
func (host *HostImpl) retire() {
	previous := host.previous
	if previous == nil {
		return
	}
	host.previous = nil
	host.codeVersions.Delete(previous.id)
	for _, object := range host.objects.Values() {
		host.retireImplementation(object, previous.id)
	}
	for _, connection := range host.connections.Keys() {
		if connection.role == "downstream" && connection.peer.kind == KindHost {
			connection.send(RetireFrame{CodeVersion: previous.id})
		}
	}
	host.retirementChanged()
}

// retireImplementation drops an object's implementation for code version and what only that implementation declared, in disposal order:
// resources close (reverse open order) before its holds are released. The closes and releases run as an owned task,
// as upstream's unawaited runAll; failures are reported.
func (host *HostImpl) retireImplementation(object *ObjectImpl, codeVersion int) {
	installed, _ := object.implementations.Get(codeVersion)
	object.implementations.Delete(codeVersion)
	if installed != nil {
		// References bind weakly upstream (a WeakMap by service): a retired code version's service, and the factory code it
		// holds, must not be kept alive by its consumers.
		for _, ref := range object.refs.Keys() {
			ref.forgetBindings(installed)
		}
	}
	if installed == nil || installed.decls == object.decls {
		return
	}
	kept := object.allDecls()
	refs := map[RefCore]bool{}
	resources := map[*ResourceImpl]bool{}
	derives := map[*Derived]bool{}
	for _, decls := range kept {
		for _, hold := range decls.refsList() {
			refs[hold] = true
		}
		for _, resource := range decls.resourcesList() {
			resources[resource] = true
		}
		for _, derived := range decls.derivesList() {
			derives[derived] = true
		}
	}
	for _, derived := range installed.decls.derivesList() {
		if !derives[derived] {
			derived.Detach()
		}
	}
	order := object.openOrder
	var closing []*ResourceImpl
	for _, resource := range installed.decls.resourcesList() {
		if !resources[resource] {
			closing = append(closing, resource)
		}
	}
	slices.SortStableFunc(closing, func(a, b *ResourceImpl) int {
		return slices.Index(order, b) - slices.Index(order, a)
	})
	var releasing []RefCore
	for _, hold := range installed.decls.refsList() {
		if !refs[hold] {
			releasing = append(releasing, hold)
		}
	}
	object.refreshSchemas()
	steps := make([]func() error, 0, len(closing)+len(releasing))
	for _, resource := range closing {
		steps = append(steps, func() error { return resource.close(&object.openOrder) })
	}
	for _, hold := range releasing {
		steps = append(steps, func() error {
			hold.Release()
			return nil
		})
	}
	// runAll runs its first step synchronously up to the step's first await (errors.ts:63-67): a resource close's
	// bookkeeping (closeStart) or a hold release happens before retireImplementation returns; the spec's close, which may
	// block, and the remaining steps run as the owned task.
	if len(closing) > 0 {
		steps[0] = closing[0].closeStart(&object.openOrder)
	} else if len(releasing) > 0 {
		err := runStep(steps[0])
		steps[0] = func() error { return err }
	}
	message := fmt.Sprintf("%s: retiring code version %d failed", object.address, codeVersion)
	host.goTask(func() {
		if err := runAll(steps, message); err != nil {
			host.run(func() { host.report(err) })
		}
	})
}

// ─── registries ───

type registryView struct {
	host     *HostImpl
	registry Registry[any]
}

// table is the registry's table of ctx's code version (the current code version for a nil ctx). It runs on the loop.
func (view registryView) table(ctx Ctx) (RegistryTable[any], error) {
	codeVersion := view.host.current
	if ctx != nil {
		var ok bool
		if codeVersion, ok = view.host.codeVersions.Get(ctx.CodeVersion()); !ok {
			return RegistryTable[any]{}, NewChordError(CodeSuperseded, fmt.Sprintf("code version %d is retired", ctx.CodeVersion()), ChordErrorOptions{})
		}
	}
	return codeVersion.tables[view.registry.Name], nil
}

func (view registryView) mustTable(ctx Ctx) RegistryTable[any] {
	var table RegistryTable[any]
	var err error
	view.host.run(func() { table, err = view.table(ctx) })
	if err != nil {
		panic(err)
	}
	return table
}

// Table is the registry's table of ctx's code version; it panics with superseded when that code version is retired.
func (view registryView) Table(ctx Ctx) RegistryTable[any] { return view.mustTable(ctx) }

// Get is the table's value for key.
func (view registryView) Get(key string, ctx Ctx) (any, bool) { return view.mustTable(ctx).Get(key) }

// Subscribe calls listener with the current table at once, then with every changed table of the current code version.
func (view registryView) Subscribe(listener func(table RegistryTable[any], codeVersion int)) func() {
	host := view.host
	entry := &registryListener{fn: listener}
	var set *orderedMap[*registryListener, struct{}]
	host.run(func() {
		var ok bool
		if set, ok = host.subscribers.Get(view.registry.Name); !ok {
			set = newOrderedMap[*registryListener, struct{}]()
			host.subscribers.Set(view.registry.Name, set)
		}
		set.Set(entry, struct{}{})
		table, _ := view.table(nil)
		listener(table, host.current.id)
	})
	return func() { host.run(func() { set.Delete(entry) }) }
}

// Registry is the host's view of registry (Get(key, ctx) reads the table of ctx's code version).
func (host *HostImpl) Registry(registry Registry[any]) RegistryView[any] {
	return registryView{host: host, registry: registry}
}

type registryEntry struct {
	value  any
	object *ObjectImpl
}

// buildTables builds registry tables from (object, declarations) pairs. It fails with registry_conflict.
func (host *HostImpl) buildTables(entries []declsEntry) (map[string]RegistryTable[any], error) {
	byRegistry := newOrderedMap[string, *orderedMap[string, registryEntry]]()
	for _, entry := range entries {
		for _, registration := range entry.decls.registrationsList() {
			registry := registration.Registry
			keyed, ok := byRegistry.Get(registry.Name)
			if !ok {
				keyed = newOrderedMap[string, registryEntry]()
				byRegistry.Set(registry.Name, keyed)
			}
			existing, ok := keyed.Get(registration.Key)
			if ok && existing.object != entry.object && registry.Conflict == "error" {
				return nil, Invalid("registry_conflict", fmt.Sprintf("%s: %s registered by %s and %s", registry.Name, registration.Key, existing.object.address, entry.object.address), nil)
			}
			keyed.Set(registration.Key, registryEntry{value: registration.Value, object: entry.object})
			order := host.order[registry.Name]
			if order == nil {
				order = map[string]int{}
				host.order[registry.Name] = order
			}
			if _, ok := order[registration.Key]; !ok {
				order[registration.Key] = len(order)
			}
		}
	}
	tables := make(map[string]RegistryTable[any], byRegistry.Len())
	for _, name := range byRegistry.Keys() {
		keyed, _ := byRegistry.Get(name)
		order := host.order[name]
		keys := keyed.Keys()
		slices.SortStableFunc(keys, func(a, b string) int { return order[a] - order[b] })
		values := make(map[string]any, len(keys))
		for _, key := range keys {
			entry, _ := keyed.Get(key)
			values[key] = entry.value
		}
		tables[name] = NewRegistryTable(keys, values)
	}
	return tables, nil
}

// bindingService is the service a hold binds against: the current one, or for a prepare rerun (candidate) the running
// reload's pending one, so consumer and provider of a breaking change can reload in one batch.
func (host *HostImpl) bindingService(typeName string, candidate bool) *Service {
	if candidate && host.pending != nil {
		if service, ok := host.pending.services.Get(typeName); ok {
			return service
		}
	}
	return host.service(typeName)
}

// liveDecls are the live objects with their current declarations; objects waiting for a reload's commit only with
// candidates.
func (host *HostImpl) liveDecls(candidates bool) []declsEntry {
	var entries []declsEntry
	for _, object := range host.objects.Values() {
		if object.status == "live" && (candidates || !object.deferredRuntime) {
			entries = append(entries, declsEntry{object: object, decls: object.decls})
		}
	}
	return entries
}

func (host *HostImpl) checkRegistrations(object *ObjectImpl, decls *Decls) error {
	if len(decls.registrationsList()) == 0 {
		return nil
	}
	var entries []declsEntry
	for _, entry := range host.liveDecls(false) {
		if entry.object != object {
			entries = append(entries, entry)
		}
	}
	_, err := host.buildTables(append(entries, declsEntry{object: object, decls: decls}))
	return err
}

// registriesChanged rebuilds the current registry tables from live objects (plus including, which is finishing
// activation).
func (host *HostImpl) registriesChanged(including *ObjectImpl) error {
	var entries []declsEntry
	for _, entry := range host.liveDecls(false) {
		if entry.object != including {
			entries = append(entries, entry)
		}
	}
	if including != nil {
		entries = append(entries, declsEntry{object: including, decls: including.decls})
	}
	tables, err := host.buildTables(entries)
	if err != nil {
		return err
	}
	host.installTables(host.current, tables, false)
	return nil
}

// fail records that a commit failed midway: from now on this host admits no work (calls fail unavailable/host_failed).
func (host *HostImpl) fail(err error) {
	if host.failed == nil {
		host.failed = err
	}
	host.report(err)
}

// installTables installs tables for code version; subscribers hear about changed tables of the current code version (all tables
// when all).
func (host *HostImpl) installTables(codeVersion *CodeVersionState, tables map[string]RegistryTable[any], all bool) {
	before := codeVersion.tables
	codeVersion.tables = tables
	if codeVersion != host.current {
		return
	}
	for _, name := range host.subscribers.Keys() {
		listeners, _ := host.subscribers.Get(name)
		next := tables[name]
		if !all && sameTable(before[name], next) {
			continue
		}
		current := listeners.Keys()
		enqueue(&host.loop, func() {
			for _, listener := range current {
				host.callListener(listener, next, codeVersion.id)
			}
		})
	}
}

func (host *HostImpl) callListener(listener *registryListener, table RegistryTable[any], codeVersion int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			host.report(recoveredError(recovered))
		}
	}()
	listener.fn(table, codeVersion)
}

// recoveredError is the error a recovered panic carries (upstream's caught exception).
func recoveredError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return fmt.Errorf("%v", recovered)
}

// ─── public API ───

// Get holds the object of typ for input and returns its reference once it is ready: live, with every state member of
// its variant (unless options.State is false). It fails with the activation error, aborted when options.Signal is
// cancelled, or unavailable/ready_timeout after HostOptions.ReadyTimeout; the reference is released then.
func (host *HostImpl) Get(typ *Contract, input any, options *GetOptions) (Ref, error) {
	ref, err := host.Hold(typ, input, options)
	if err != nil {
		return nil, err
	}
	var signals []context.Context
	if options != nil && options.Signal != nil {
		signals = append(signals, options.Signal)
	}
	if err := awaitReady(host, ref.(RefCore), signals, host.readyTimeout); err != nil {
		return nil, err
	}
	return ref, nil
}

// Hold is the synchronous form of Get: the reference at once, possibly still loading (Ready waits for it).
func (host *HostImpl) Hold(typ *Contract, input any, options *GetOptions) (ref Ref, err error) {
	host.run(func() {
		var hold RefCore
		if hold, err = host.acquire(typ, input, options, "external", nil, false); err == nil {
			ref = hold
		}
	})
	return ref, err
}

// InvalidatePrincipal force-disposes every object whose principal matches (never the host principal), re-stamps or
// closes matching links, and propagates pattern matches to child hosts.
func (host *HostImpl) InvalidatePrincipal(match PrincipalMatch) error {
	matches := match.Predicate
	if matches == nil {
		matches = func(principal Principal) bool { return matchesPattern(match.Pattern, principal) }
	}
	var matching []*ObjectImpl
	reason := NewChordError(CodeGone, "principal invalidated", ChordErrorOptions{})
	host.run(func() {
		// Links acting as a matching principal are re-stamped (if the acceptor can) or closed.
		for _, connection := range host.connections.Keys() {
			if connection.role != "downstream" || !matches(connection.principal) {
				continue
			}
			// Relays under the old principal close at once, including those still resolving their route.
			var restamped Principal
			keep := false
			if connection.restamp != nil {
				restamped, keep = connection.restamp(connection.principal)
			}
			connection.revoke(restamped, keep)
		}
		// Patterns propagate to child hosts, which dispose their own matching objects (e.g. relayed guests).
		if match.Predicate == nil {
			for _, connection := range host.connections.Keys() {
				if connection.role == "downstream" && connection.peer.kind == KindHost {
					connection.send(InvalidateFrame{Pattern: match.Pattern})
				}
			}
		}
		for _, object := range host.objects.Values() {
			if !host.isSelf(object.principal) && matches(object.principal) {
				matching = append(matching, object)
			}
		}
	})
	return host.disposeHoldersFirst(matching, reason, "invalidatePrincipal: disposal failed")
}

// ─── links ───

// Accept accepts a child link (a worker or a browser); requests on it act as options.Principal.
func (host *HostImpl) Accept(link Link, options AcceptOptions) error {
	kind := options.Kind
	if kind == "" {
		kind = KindClient
	}
	if kind == KindHost && options.Name == nil {
		return &TypeError{Message: "accepting a host link needs its name"}
	}
	var name *string
	if kind == KindHost {
		name = options.Name
	}
	host.run(func() {
		config := connectionConfig{
			role:      "downstream",
			principal: options.Principal,
			peer:      Peer{kind: kind, name: name},
			restamp:   options.Restamp,
			options:   options.LinkOptions,
			maxCalls:  options.MaxCalls,
		}
		// Holds over a client link carry its id as their origin (also when relayed), so hosts can tell clients apart.
		if kind == KindClient {
			host.originSeq++
			config.originID = host.incarnation + ":" + strconv.Itoa(host.originSeq)
		}
		host.addConnection(link, config)
	})
	return nil
}

// Connect sets (or replaces, after a reconnect) the link to this host's parent; remote references re-hold over it.
// The parent's own requests act as options.Principal; requests it relays act as the principal it forwards. It panics
// with a TypeError, as upstream throws one, unless options carry Principal.
func (host *HostImpl) Connect(link Link, options ConnectOptions) {
	if options.Principal == nil && !options.HasPrincipal {
		panic(typeError("connect needs `principal`: what the parent's own requests act as"))
	}
	host.run(func() {
		if host.upstream != nil && host.upstream.open() {
			host.upstream.link.Close("replaced")
		}
		connection := host.addConnection(link, connectionConfig{role: "upstream", principal: options.Principal, peer: Peer{kind: KindHost}, options: options.LinkOptions})
		host.upstream = connection
		// Bindings that lost their link re-hold over the new one (again: they keep their values meanwhile).
		for _, binding := range host.bindings.Values() {
			if binding.hold == nil && !binding.ended() {
				connection.hold(binding, binding.applied)
			}
		}
	})
}

func (host *HostImpl) addConnection(link Link, config connectionConfig) *Connection {
	connection := newConnection(host, link, config)
	// A link that was already closed closed the connection in newConnection: keeping it would route to it forever.
	if connection.open() {
		host.connections.Set(connection, struct{}{})
	}
	return connection
}

// Route sends requests for hosts named prefix… to route. It panics with a TypeError, as upstream throws one, when
// route has no Principal.
func (host *HostImpl) Route(prefix string, route Route) {
	if route.Principal == nil {
		panic(typeError("route %s needs `principal`: what the routed hosts act as when they call back", prefix))
	}
	host.run(func() { host.routes[prefix] = route })
}

// peerHello references a child host announcing its code version. In step: usable. Behind: fenced (nothing is routed to it) and
// reconciled by a reload of its modules to this host's code version, never by renumbering what it runs. Ahead: impossible,
// refused.
func (host *HostImpl) peerHello(connection *Connection, codeVersion int) {
	if codeVersion > host.current.id {
		host.report(NewChordError(CodeInvalid, fmt.Sprintf("%s runs code version %d, ahead of %d", connection.name(), codeVersion, host.current.id), ChordErrorOptions{}))
		connection.link.Close("code version ahead of parent")
		return
	}
	if host.reloading {
		// Not a participant of the running reload (participants are fixed when it starts): in step now, it would miss
		// the commit. Decided when the reload ends. A child already behind is fenced now, so the calls its roots make
		// meanwhile are admitted in this host's code version (never relabeled) instead of being superseded.
		connection.peerCodeVersion = codeVersion
		if codeVersion < host.current.id {
			connection.fencedCodeVersions[codeVersion] = struct{}{}
		}
		host.deferredHellos.Set(connection, codeVersion)
		return
	}
	if codeVersion == host.current.id {
		connection.markSynced(codeVersion)
		host.announceRetired(connection)
		return
	}
	connection.peerCodeVersion = codeVersion
	connection.fencedCodeVersions[codeVersion] = struct{}{}
	host.goTask(func() {
		if err := reconcileChild(host, connection); err != nil {
			host.run(func() {
				host.report(err)
				connection.link.Close("reconciliation failed")
			})
		}
	})
}

// reloadEnded: the host's reload slot was freed; children that said hello meanwhile are synced or reconciled now.
func (host *HostImpl) reloadEnded() {
	host.reloading = false
	deferred := host.deferredHellos.Keys()
	codeVersions := make([]int, len(deferred))
	for index, connection := range deferred {
		codeVersions[index], _ = host.deferredHellos.Get(connection)
	}
	host.deferredHellos.Clear()
	for index, connection := range deferred {
		if connection.open() {
			host.peerHello(connection, codeVersions[index])
		}
	}
	waiters := host.reloadWaiters
	host.reloadWaiters = nil
	for _, resolve := range waiters {
		resolve()
	}
	host.maybeRetire()
}

// whenReloadEnded is closed once no reload runs on this host. It runs on the loop.
func (host *HostImpl) whenReloadEnded() <-chan struct{} {
	ended := make(chan struct{})
	if !host.reloading {
		close(ended)
		return ended
	}
	host.reloadWaiters = append(host.reloadWaiters, func() { close(ended) })
	return ended
}

func (host *HostImpl) connectionClosed(connection *Connection) {
	host.connections.Delete(connection)
	host.deferredHellos.Delete(connection)
	// Foreign roots that came over this link will never announce their end over it.
	for _, rootID := range host.foreign.Keys() {
		if links, ok := host.foreign.Get(rootID); ok && links.Has(connection) {
			host.foreignRootEnded(rootID, connection)
		}
	}
	for _, links := range host.rootLinks {
		links.Delete(connection)
	}
	if host.upstream == connection {
		host.upstream = nil
		host.childReloads.parentLost()
	}
}

// bindingIdle: the last reference to binding was released: it lingers if its type does (HostOptions.Linger), else
// closes.
func (host *HostImpl) bindingIdle(binding *RemoteBinding) {
	ms := host.lingerTypes[binding.contract.Name]
	// A binding that never had a shape has nothing worth keeping.
	if ms <= 0 || binding.ended() || !binding.applied || host.lingerCap <= 0 {
		binding.close()
		return
	}
	binding.lingerStop = host.lingerAfter(ms, binding.close)
	host.lingering.Set(binding.key, binding)
	for _, oldest := range host.lingering.Values() {
		if host.lingering.Len() <= host.lingerCap {
			break
		}
		oldest.close()
	}
}

func (host *HostImpl) forgetBinding(binding *RemoteBinding) {
	if current, ok := host.bindings.Get(binding.key); ok && current == binding {
		host.bindings.Delete(binding.key)
	}
	if current, ok := host.lingering.Get(binding.key); ok && current == binding {
		host.lingering.Delete(binding.key)
	}
}

// childNamed is a downstream host connection by its configured name (never by what a peer announced).
func (host *HostImpl) childNamed(name string) *Connection {
	for _, connection := range host.connections.Keys() {
		if connection.open() && connection.role == "downstream" && connection.peer.kind == KindHost && connection.peer.name != nil && *connection.peer.name == name {
			return connection
		}
	}
	return nil
}

// acquireRemote is a reference to an object on host at: a view onto the binding for its key, created (and held) if
// there is none. input is normalized with typ (the hold carries it so).
func (host *HostImpl) acquireRemote(typ *Contract, at string, input any, options *GetOptions, owner RefOwner) (RefCore, error) {
	var principal Principal
	hasPrincipal := options != nil && options.HasPrincipal && !host.isSelf(options.Principal)
	if hasPrincipal {
		principal = options.Principal
	}
	keyPrincipal := host.principal
	if hasPrincipal {
		keyPrincipal = principal
	}
	key := host.remoteKey(typ, input, keyPrincipal)
	binding, _ := host.bindings.Get(key)
	fresh := binding == nil || binding.ended()
	if fresh {
		binding = newRemoteBinding(host, key, typ, at, input, principal, hasPrincipal)
		host.bindings.Set(key, binding)
	}
	ref := newRemoteRef(binding, owner, options == nil || options.State == nil || *options.State)
	host.lingering.Delete(key)
	binding.addView(ref)
	if fresh {
		host.hold(binding)
	}
	return ref, nil
}

// hold holds binding over the link to its host: the parent's, a child's, or one a route resolves.
func (host *HostImpl) hold(binding *RemoteBinding) {
	bindWhenSynced := func(connection *Promise[*Connection]) {
		connection.then(func(target *Connection, err error) {
			if err != nil {
				binding.gone(err)
				return
			}
			if !binding.ended() && binding.hold == nil {
				target.hold(binding, false)
			}
		})
	}
	direct := host.childNamed(binding.at)
	switch {
	case host.upstream != nil:
		host.upstream.hold(binding, false)
	case direct != nil:
		synced, resolve, reject := newPromise[*Connection](&host.loop)
		direct.whenSynced().then(func(_ struct{}, err error) {
			if err != nil {
				reject(err)
				return
			}
			resolve(direct)
		})
		bindWhenSynced(synced)
	default:
		route, ok := host.routeFor(binding.at)
		if !ok {
			binding.gone(NewChordError(CodeUnavailable, "no route to "+binding.at, ChordErrorOptions{}))
			break
		}
		principal := host.principal
		if binding.hasPrincipal {
			principal = binding.principal
		}
		bindWhenSynced(host.resolveRoute(route, binding.at, principal, nil))
	}
}

// routeFor is the route of the longest prefix of hostName.
func (host *HostImpl) routeFor(hostName string) (Route, bool) {
	best, found := "", false
	for prefix := range host.routes {
		if strings.HasPrefix(hostName, prefix) && (!found || len(prefix) > len(best)) {
			best, found = prefix, true
		}
	}
	return host.routes[best], found
}

// awaitCallback calls a user callback (a route's authorize or resolve) as upstream awaits one: the callback starts
// once the previously started callback entered (returned, or parked in a real wait), and the returned promise settles
// when it returns. Callbacks that return without a real wait therefore settle, and their continuations run, in start
// order, as upstream's microtasks do; one that really waits lets the next go first. On the loop.
func awaitCallback[T any](host *HostImpl, call func() (T, error)) *Promise[T] {
	promise, resolve, reject := newPromise[T](&host.loop)
	host.enter(func() {
		var value T
		var err error
		if thrown := callReporting(func() { value, err = call() }); thrown != nil {
			err = thrown
		}
		if err != nil {
			reject(err)
			return
		}
		resolve(value)
	})
	return promise
}

// resolveRoute authorizes, resolves (maybe spawns) and authorizes again; its promise settles with the synced
// connection to the host (host.ts:849-872). still (nil: always) reports whether the request is still authorized; it is
// checked after every await. It starts on the loop, and every continuation after an await comes back to the loop.
func (host *HostImpl) resolveRoute(route Route, hostName string, principal Principal, still func() bool) *Promise[*Connection] {
	result, resolve, reject := newPromise[*Connection](&host.loop)
	refuse := func() error {
		return NewChordError(CodeForbidden, hostName+" refused for this principal", ChordErrorOptions{})
	}
	revoked := func() error {
		return NewChordError(CodeGone, "principal invalidated while resolving", ChordErrorOptions{})
	}
	current := func() bool { return still == nil || still() }
	authorize := func() *Promise[bool] {
		return awaitCallback(host, func() (bool, error) { return route.Authorize(principal, hostName) })
	}
	// authorized is the check after an authorize: refused, revoked, or nil to go on.
	authorized := func(ok bool, err error) error {
		switch {
		case err != nil:
			return err
		case !ok:
			return refuse()
		case !current():
			return revoked()
		}
		return nil
	}
	authorize().then(func(ok bool, err error) {
		if err := authorized(ok, err); err != nil {
			reject(err)
			return
		}
		awaitCallback(host, func() (Link, error) { return route.Resolve(hostName) }).then(func(link Link, err error) {
			if err == nil && !current() {
				err = revoked()
			}
			if err != nil {
				reject(err)
				return
			}
			authorize().then(func(ok bool, err error) {
				if err := authorized(ok, err); err != nil {
					reject(err)
					return
				}
				var connection *Connection
				for _, candidate := range host.connections.Keys() {
					if candidate.link == link {
						connection = candidate
						break
					}
				}
				if connection == nil {
					connection = host.addConnection(link, connectionConfig{role: "downstream", principal: route.Principal(hostName), peer: Peer{kind: KindHost, name: &hostName}})
				}
				connection.whenSynced().then(func(_ struct{}, err error) {
					if err == nil && !current() {
						err = revoked()
					}
					if err != nil {
						reject(err)
						return
					}
					resolve(connection)
				})
			})
		})
	})
	return result
}

// relay relays a hold from relay's origin to the host it names (host.ts:879-890). Relaying needs a route: it is where
// authorization lives. The origin's authorization generation is re-checked after every await and right before
// forwarding, so a revocation during resolution wins. It starts on the loop; the promise settles when the hold was
// forwarded or the relay gave up.
func (host *HostImpl) relay(relay *RelayRef) *Promise[struct{}] {
	at := relay.frame.At
	// The hold names its target; its contract token says where the type lives. They must agree.
	if err := placementError(relay.frame, at); err != nil {
		return rejectedPromise[struct{}](&host.loop, err)
	}
	route, ok := host.routeFor(at)
	if !ok {
		if host.childNamed(at) == nil {
			return rejectedPromise[struct{}](&host.loop, NewChordError(CodeUnavailable, "no route to "+at, ChordErrorOptions{}))
		}
		return rejectedPromise[struct{}](&host.loop, NewChordError(CodeForbidden, "no route authorizes "+at, ChordErrorOptions{}))
	}
	done, resolve, reject := newPromise[struct{}](&host.loop)
	host.resolveRoute(route, at, relay.principal, relay.current).then(func(down *Connection, err error) {
		if err != nil {
			reject(err)
			return
		}
		if relay.current() {
			relay.open(down)
		}
		resolve(struct{}{})
	})
	return done
}

// placementError is invalid/placement unless the placement of frame's contract token puts its input on the host at.
func placementError(frame HoldFrame, at string) error {
	token, err := contractFromDescription(frame.Token)
	if err != nil {
		return err
	}
	if placement := token.Description.Host; placement != nil {
		resolved, err := resolveHost(frame.Token.Name, placement, frame.Input)
		if err != nil {
			return err
		}
		if resolved == at {
			return nil
		}
	}
	return Invalid("placement", fmt.Sprintf("%s with this input does not live on %s", frame.Token.Name, at), nil)
}

// Reload reloads the given facets (modules are re-imported); facets not listed keep running unchanged.
func (host *HostImpl) Reload(plugins []PluginRef, options ReloadOptions) (ReloadReport, error) {
	var refused error
	children := false
	host.run(func() {
		// Client-local facet code versions never cross the parent link; only child hosts require cluster coordination.
		if host.kind != KindClient && host.upstream != nil {
			refused = NewChordError(CodeUnavailable, "reloads are coordinated by the server", ChordErrorOptions{Reason: "not_coordinator"})
			return
		}
		for _, connection := range host.connections.Keys() {
			if connection.role == "downstream" && connection.peer.kind == KindHost {
				children = true
				break
			}
		}
	})
	if refused != nil {
		return ReloadReport{}, refused
	}
	if children || options.Remote != nil || options.RemoteRemove != nil {
		return runClusterReload(host, plugins, options)
	}
	return runReload(host, plugins, options)
}

// onReloadFrame references a prepare, commit or abort from the parent; a failed commit's error is returned to the
// connection.
func (host *HostImpl) onReloadFrame(connection *Connection, frame Frame) error {
	return host.childReloads.ref(connection, frame)
}

func (host *HostImpl) rememberRemoteModules(remote, remove map[string][]string) {
	prefixes := map[string]struct{}{}
	for prefix := range remote {
		prefixes[prefix] = struct{}{}
	}
	for prefix := range remove {
		prefixes[prefix] = struct{}{}
	}
	for prefix := range prefixes {
		modules, removed := remote[prefix], remove[prefix]
		host.remoteModules[prefix] = uniqueWithout(append(slices.Clone(host.remoteModules[prefix]), modules...), removed)
		host.remoteRemoved[prefix] = uniqueWithout(append(slices.Clone(host.remoteRemoved[prefix]), removed...), modules)
	}
}

// uniqueWithout is [...new Set(items)].filter((item) => !excluded.includes(item)).
func uniqueWithout(items, excluded []string) []string {
	out := []string{}
	for _, item := range items {
		if !slices.Contains(out, item) && !slices.Contains(excluded, item) {
			out = append(out, item)
		}
	}
	return out
}

// peekNextCodeVersion is the code version id the next reload would commit.
func (host *HostImpl) peekNextCodeVersion() int { return host.codeVersionSeq + 1 }

// Close disposes every object, holders before what they hold (a resource's close may still use the objects its object
// holds, local or remote), then closes the links and waits for the host's background tasks.
func (host *HostImpl) Close() error {
	var objects []*ObjectImpl
	host.run(func() {
		for _, binding := range host.lingering.Values() {
			binding.close()
		}
		host.hostObjects.Clear()
		for _, name := range host.facetRuns.Keys() {
			host.facetRunChanged(name, nil)
		}
		objects = host.objects.Values()
	})
	reason := NewChordError(CodeGone, "host "+host.name+" closed", ChordErrorOptions{})
	err := host.disposeHoldersFirst(objects, reason, "host close: disposal failed")
	var closed []Link
	host.run(func() {
		for _, connection := range host.connections.Keys() {
			connection.link.Close("host closed")
			closed = append(closed, connection.link)
		}
	})
	// Pi's close() resolves after the microtasks its link closes queued, so with an in-memory transport the peer has
	// handled the close when close() returns (link.ts:103-112). MemoryLink delivers on its own goroutine: wait for it.
	for _, link := range closed {
		if memory, ok := link.(*MemoryLink); ok {
			memory.flush()
		}
	}
	host.waitTasks()
	return err
}

// disposeHoldersFirst force-disposes objects in waves: an object goes once no object of the set that holds it is left,
// so holders are disposed (and their resources closed) while what they hold is still live. In-host holds are acyclic.
// Errors of every disposal are collected. It runs off the loop.
func (host *HostImpl) disposeHoldersFirst(objects []*ObjectImpl, reason error, message string) error {
	remaining := newOrderedMap[*ObjectImpl, struct{}]()
	refs := map[*ObjectImpl][]*ObjectImpl{}
	host.run(func() {
		for _, object := range objects {
			remaining.Set(object, struct{}{})
			refs[object] = heldObjects(object)
		}
	})
	var errs []error
	for remaining.Len() > 0 {
		heldByRemaining := map[*ObjectImpl]bool{}
		for _, object := range remaining.Keys() {
			for _, target := range refs[object] {
				heldByRemaining[target] = true
			}
		}
		var wave []*ObjectImpl
		for _, object := range remaining.Keys() {
			if !heldByRemaining[object] {
				wave = append(wave, object)
			}
		}
		// A cycle cannot happen in-host; dispose the rest together rather than loop.
		if len(wave) == 0 {
			wave = remaining.Keys()
		}
		for _, object := range wave {
			remaining.Delete(object)
		}
		promises := make([]*Promise[struct{}], len(wave))
		host.run(func() {
			for index, object := range wave {
				promises[index] = object.dispose(true, reason)
			}
		})
		//portlint:allow ctxbackground host.ts disposes each wave with `await Promise.allSettled`, which has no abort
		results, _ := promiseAllSettled(&host.loop, promises).Await(context.Background())
		for _, result := range results {
			if result.err != nil {
				errs = append(errs, result.err)
			}
		}
	}
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	}
	return &AggregateError{Message: message, Errors: errs}
}

// heldObjects are the objects object holds on this host, through any of its declarations.
func heldObjects(object *ObjectImpl) []*ObjectImpl {
	var held []*ObjectImpl
	for _, decls := range object.allDecls() {
		for _, hold := range decls.refsList() {
			if target := hold.refObject(); target != nil && !slices.Contains(held, target) {
				held = append(held, target)
			}
		}
	}
	return held
}

// sameTable reports whether two tables have the same keys in the same order with identical values (===).
func sameTable(a, b RegistryTable[any]) bool {
	if a.Len() != b.Len() {
		return false
	}
	keys := a.Keys()
	for index, key := range b.Keys() {
		if keys[index] != key {
			return false
		}
		left, _ := a.Get(key)
		right, _ := b.Get(key)
		if !identical(left, right) {
			return false
		}
	}
	return true
}

// identical is JavaScript's === over Go values: reference kinds compare by identity, others by value.
func identical(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	left, right := reflect.ValueOf(a), reflect.ValueOf(b)
	if left.Type() != right.Type() {
		return false
	}
	switch left.Kind() {
	case reflect.Map, reflect.Slice, reflect.Func, reflect.Pointer, reflect.Chan, reflect.UnsafePointer:
		return left.Pointer() == right.Pointer() && (left.Kind() != reflect.Slice || left.Len() == right.Len())
	}
	if !left.Type().Comparable() {
		return false
	}
	return a == b
}

// matchesPattern reports whether principal is an object whose pattern fields deep-equal the pattern's.
func matchesPattern(pattern *chordjson.Object, principal Principal) bool {
	field, ok := principalFields(principal)
	if !ok {
		return false
	}
	if pattern == nil {
		return true
	}
	for key, value := range pattern.All() {
		if !DeepEqual(field(key), value) {
			return false
		}
	}
	return true
}

// principalFields reads a principal's fields; ok is false unless it is an object (typeof "object", not null).
func principalFields(principal Principal) (func(key string) any, bool) {
	if principal == nil {
		return nil, false
	}
	if _, get, ok := objectEntries(principal); ok {
		return get, true
	}
	if list := reflect.ValueOf(principal); list.Kind() == reflect.Slice {
		return func(key string) any {
			//portlint:allow numbers only a canonical array index reads an element (Itoa round trip below), as a JS property key on an array
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= list.Len() || strconv.Itoa(index) != key {
				return absent{}
			}
			return list.Index(index).Interface()
		}, true
	}
	return nil, false
}

func unknownType(typ, host string) *ChordError {
	return NewChordError(CodeUnavailable, typ+" is not served by "+host, ChordErrorOptions{Reason: "unknown_type"})
}

// reaches reports whether from holds to, directly or transitively, through object-owned holds on this host.
func reaches(from, to *ObjectImpl) bool {
	seen := map[*ObjectImpl]bool{}
	stack := []*ObjectImpl{from}
	for len(stack) > 0 {
		object := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if object == to {
			return true
		}
		if seen[object] {
			continue
		}
		seen[object] = true
		// Remote holds have no object here: cycles across hosts are avoided by convention.
		for _, decls := range object.allDecls() {
			for _, hold := range decls.refsList() {
				if target := hold.refObject(); target != nil {
					stack = append(stack, target)
				}
			}
		}
	}
	return false
}
