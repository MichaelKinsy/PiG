package chord

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

// Facet is one unit of composition (upstream Facet). Setup declares the
// facet's service requirements and provisions synchronously; it must not
// start work, which belongs in OnActivate.
type Facet struct {
	Id    string
	Setup func(env *FacetEnvironment) error
}

// DefineFacet returns facet unchanged (upstream defineFacet).
func DefineFacet(facet Facet) Facet { return facet }

// FacetOptions configures CreateFacetHost.
type FacetOptions struct {
	Facets []Facet
	// ServiceSources offer external services for requirements no facet
	// provides (upstream serviceSources).
	ServiceSources []RemoteServiceSource
	// RemoteClients adapts an external service facade to the Go contract T a
	// facet receives from UseService/ObserveService, keyed by service ID. Go
	// cannot synthesize upstream's typed proxy; the owning service lane
	// supplies the adapter.
	RemoteClients map[string]func(*RemoteService) any
	// OnError receives asynchronous observer and binding errors.
	OnError func(error)
}

// RemoteServices is one opened set of external services (upstream
// RemoteServices). *RemoteServiceBinding implements it.
type RemoteServices interface {
	Use(serviceId string) (*RemoteService, error)
	Observe(serviceId string, handler func(context.Context, *RemoteService) error) (func(), error)
	Ready(ctx context.Context) error
	Dispose(ctx context.Context) error
}

var _ RemoteServices = (*RemoteServiceBinding)(nil)

// RemoteServiceSourceOpenOptions is passed to RemoteServiceSource.Open.
type RemoteServiceSourceOpenOptions struct {
	Services     []string
	AssertAccess func() error
	OnError      func(error)
}

// RemoteServiceSource offers external services to a facet host (upstream
// RemoteServiceSource).
type RemoteServiceSource interface {
	// AcceptsUnavailableServices reports whether this currently unavailable
	// source may provisionally own requirements absent from every catalogue.
	AcceptsUnavailableServices() bool
	Catalogue(ctx context.Context) ([]ServiceCatalogueEntry, error)
	Open(options RemoteServiceSourceOpenOptions) (RemoteServices, error)
}

// LoadedFacets is one loaded facet set and its release function.
type LoadedFacets struct {
	Facets  []Facet
	Dispose func(context.Context) error
}

// FacetLoader loads a facet set (upstream FacetLoader).
type FacetLoader interface {
	Load(ctx context.Context) (LoadedFacets, error)
}

type facetLoaderFunc func(ctx context.Context) (LoadedFacets, error)

func (load facetLoaderFunc) Load(ctx context.Context) (LoadedFacets, error) { return load(ctx) }

// CreateStaticFacetLoader always loads the same facets with a no-op release.
func CreateStaticFacetLoader(facets []Facet) FacetLoader {
	loaded := slices.Clone(facets)
	return facetLoaderFunc(func(context.Context) (LoadedFacets, error) {
		return LoadedFacets{Facets: slices.Clone(loaded), Dispose: func(context.Context) error { return nil }}, nil
	})
}

// CombineFacetLoaders loads each loader in order. A load failure releases the already loaded sets in reverse order. The combined release runs once and releases every set in reverse order; repeated or reentrant calls return immediately.
func CombineFacetLoaders(loaders ...FacetLoader) FacetLoader {
	return facetLoaderFunc(func(ctx context.Context) (LoadedFacets, error) {
		var loaded []LoadedFacets
		for _, loader := range loaders {
			var set LoadedFacets
			err := safeCall(func() error {
				var loadErr error
				set, loadErr = loader.Load(ctx)
				return loadErr
			})
			if err != nil {
				reversed := slices.Clone(loaded)
				slices.Reverse(reversed)
				if cleanup := disposeLoaded(ctx, reversed); len(cleanup) > 0 {
					return LoadedFacets{}, fmt.Errorf("Facet loading and cleanup failed: %w", errors.Join(append([]error{err}, cleanup...)...))
				}
				return LoadedFacets{}, err
			}
			loaded = append(loaded, set)
		}
		var facets []Facet
		for _, set := range loaded {
			facets = append(facets, set.Facets...)
		}
		var disposed atomic.Bool
		return LoadedFacets{Facets: facets, Dispose: func(ctx context.Context) error {
			if disposed.Swap(true) {
				return nil
			}
			reversed := slices.Clone(loaded)
			slices.Reverse(reversed)
			errs := disposeLoaded(ctx, reversed)
			if len(errs) > 1 {
				return fmt.Errorf("Failed to dispose loaded facets: %w", errors.Join(errs...))
			}
			return joinErrors(errs)
		}}, nil
	})
}

func disposeLoaded(ctx context.Context, sets []LoadedFacets) []error {
	var errs []error
	for _, set := range sets {
		if set.Dispose == nil {
			continue
		}
		if err := safeCall(func() error { return set.Dispose(ctx) }); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

type lifecycleState string

const (
	lifecycleSettingUp lifecycleState = "setting_up"
	lifecyclePrepared  lifecycleState = "prepared"
	lifecycleActive    lifecycleState = "active"
	lifecycleDisposing lifecycleState = "disposing"
	lifecycleDead      lifecycleState = "dead"
)

// facetLifecycle is upstream FacetLifecycle.
type facetLifecycle struct {
	id            string
	mu            sync.Mutex
	state         lifecycleState
	serviceAccess bool
	effects       []func(context.Context) error
	observations  []func() func()
	activate      []func(context.Context) error
}

func (lifecycle *facetLifecycle) assertSettingUp(operation string) error {
	if lifecycle.state != lifecycleSettingUp {
		return fmt.Errorf("Facet %s can %s only during setup", lifecycle.id, operation)
	}
	return nil
}

func (lifecycle *facetLifecycle) assertRunning(operation string) error {
	if lifecycle.state != lifecycleSettingUp && lifecycle.state != lifecycleActive {
		return fmt.Errorf("Facet %s cannot %s while %s", lifecycle.id, operation, lifecycle.state)
	}
	return nil
}

func (lifecycle *facetLifecycle) assertServiceAccess() error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if !lifecycle.serviceAccess {
		return fmt.Errorf("Facet %s service handles cannot be used while %s", lifecycle.id, lifecycle.state)
	}
	return nil
}

func (lifecycle *facetLifecycle) revoke() {
	lifecycle.mu.Lock()
	lifecycle.serviceAccess = false
	lifecycle.mu.Unlock()
}

func (lifecycle *facetLifecycle) own(disposal func(context.Context) error) error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if err := lifecycle.assertRunning("own resources"); err != nil {
		return err
	}
	lifecycle.effects = append(lifecycle.effects, disposal)
	return nil
}

func (lifecycle *facetLifecycle) activateNow(ctx context.Context) error {
	lifecycle.mu.Lock()
	if lifecycle.state != lifecyclePrepared {
		lifecycle.mu.Unlock()
		return fmt.Errorf("Facet %s is not prepared", lifecycle.id)
	}
	lifecycle.state = lifecycleActive
	lifecycle.serviceAccess = true
	observations := lifecycle.observations
	callbacks := lifecycle.activate
	lifecycle.mu.Unlock()
	for _, start := range observations {
		var stop func()
		if err := safeCall(func() error { stop = start(); return nil }); err != nil {
			return err
		}
		lifecycle.mu.Lock()
		lifecycle.effects = append(lifecycle.effects, func(context.Context) error { stop(); return nil })
		lifecycle.mu.Unlock()
	}
	for _, callback := range callbacks {
		if err := safeCall(func() error { return callback(ctx) }); err != nil {
			return err
		}
	}
	return nil
}

// dispose runs owned effects in reverse order exactly once.
func (lifecycle *facetLifecycle) dispose(ctx context.Context) error {
	lifecycle.mu.Lock()
	if lifecycle.state == lifecycleDead {
		lifecycle.mu.Unlock()
		return nil
	}
	lifecycle.state = lifecycleDisposing
	effects := lifecycle.effects
	lifecycle.effects = nil
	lifecycle.mu.Unlock()
	var errs []error
	for _, effect := range slices.Backward(effects) {
		if err := safeCall(func() error { return effect(ctx) }); err != nil {
			errs = append(errs, err)
		}
	}
	lifecycle.mu.Lock()
	lifecycle.observations, lifecycle.activate = nil, nil
	lifecycle.serviceAccess = false
	lifecycle.state = lifecycleDead
	lifecycle.mu.Unlock()
	if len(errs) > 1 {
		return fmt.Errorf("Failed to dispose facet %s: %w", lifecycle.id, errors.Join(errs...))
	}
	return joinErrors(errs)
}

type serviceReference struct {
	serviceId string
	local     bool
	mode      ServiceMode
}

type facetProvision struct {
	kind      ServiceMode
	serviceId string
	local     bool
	// singleton
	implementation      any
	install             func(*RemoteServiceProvider) error
	validateReplacement func(*RemoteServiceProvider) error
	replace             func(*RemoteServiceProvider) error
	// keyed
	connect func(kernel *facetKernel) error
}

type facetRuntime struct {
	facetId    string
	requires   []serviceReference
	provides   []serviceReference
	lifecycle  *facetLifecycle
	provisions []*facetProvision
}

// FacetEnvironment is passed to Facet.Setup (upstream FacetEnvironment). The
// generic operations are package functions: ProvideService, ProvideMany,
// UseService and ObserveService.
type FacetEnvironment struct {
	kernel  *facetKernel
	runtime *facetRuntime
}

// Own gives the facet ownership of a cleanup, run in reverse order when the
// facet is disposed or replaced.
func (env *FacetEnvironment) Own(disposal func(context.Context) error) error {
	return env.runtime.lifecycle.own(disposal)
}

// OnActivate registers initialization run after dependencies are bound.
func (env *FacetEnvironment) OnActivate(callback func(context.Context) error) error {
	lifecycle := env.runtime.lifecycle
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if err := lifecycle.assertSettingUp("register activation callbacks"); err != nil {
		return err
	}
	lifecycle.activate = append(lifecycle.activate, callback)
	return nil
}

// OnDeactivate registers final teardown (upstream: same as own).
func (env *FacetEnvironment) OnDeactivate(callback func(context.Context) error) error {
	return env.Own(callback)
}

func recordReference(target *[]serviceReference, serviceId string, local bool, mode ServiceMode) {
	for _, reference := range *target {
		if reference.serviceId == serviceId && reference.mode == mode {
			return
		}
	}
	*target = append(*target, serviceReference{serviceId: serviceId, local: local, mode: mode})
}

// ProvideService declares and installs this facet's singleton implementation
// of def. Remote (non-local) implementations are classified immediately.
func ProvideService[T any](env *FacetEnvironment, def pico3.ServiceDefinition[T], implementation T) error {
	runtime := env.runtime
	runtime.lifecycle.mu.Lock()
	err := runtime.lifecycle.assertSettingUp("provide services")
	runtime.lifecycle.mu.Unlock()
	if err != nil {
		return err
	}
	if !def.Local() {
		if _, err := classifyImplementation[T](def.Id(), implementation); err != nil {
			return err
		}
	}
	recordReference(&runtime.provides, def.Id(), def.Local(), ServiceSingleton)
	runtime.provisions = append(runtime.provisions, &facetProvision{
		kind: ServiceSingleton, serviceId: def.Id(), local: def.Local(), implementation: implementation,
		install:             func(provider *RemoteServiceProvider) error { return Provide(provider, def, implementation) },
		validateReplacement: func(provider *RemoteServiceProvider) error { return ValidateReplacement(provider, def, implementation) },
		replace:             func(provider *RemoteServiceProvider) error { return Replace(provider, def, implementation) },
	})
	return nil
}

// ServiceSpawner spawns keyed instances owned by a facet (upstream
// StagedServiceSpawner). Instances spawned before the host connects the
// provision are staged and installed at connection.
type ServiceSpawner[T any] struct {
	lifecycle *facetLifecycle
	validate  func(key string, implementation T) error
	mu        sync.Mutex
	instances *orderedMap[string, *stagedInstance[T]]
	installer func(key string, implementation T) (func(), error)
}

type stagedInstance[T any] struct {
	key            string
	implementation T
	release        func()
}

func (spawner *ServiceSpawner[T]) connect(installer func(string, T) (func(), error)) error {
	spawner.mu.Lock()
	defer spawner.mu.Unlock()
	if spawner.installer != nil {
		return errors.New("Facet service provider is already connected")
	}
	spawner.installer = installer
	// Upstream StagedServiceSpawner.connect iterates its Map in insertion order.
	for _, instance := range spawner.instances.Values() {
		release, err := installer(instance.key, instance.implementation)
		if err != nil {
			return err
		}
		instance.release = release
	}
	return nil
}

// Spawn publishes one instance while the facet is active. The returned close
// function is idempotent and is also owned by the facet.
func (spawner *ServiceSpawner[T]) Spawn(key string, implementation T) (func(), error) {
	spawner.lifecycle.mu.Lock()
	active := spawner.lifecycle.state == lifecycleActive
	spawner.lifecycle.mu.Unlock()
	if !active {
		return nil, fmt.Errorf("Facet %s can spawn service instances only while active", spawner.lifecycle.id)
	}
	if err := spawner.validate(key, implementation); err != nil {
		return nil, err
	}
	spawner.mu.Lock()
	if _, exists := spawner.instances.Get(key); exists {
		spawner.mu.Unlock()
		return nil, fmt.Errorf("Facet service already has a live instance with key %s", key)
	}
	instance := &stagedInstance[T]{key: key, implementation: implementation}
	spawner.instances.Set(key, instance)
	installer := spawner.installer
	spawner.mu.Unlock()
	if installer != nil {
		release, err := installer(key, implementation)
		if err != nil {
			spawner.mu.Lock()
			spawner.instances.Delete(key)
			spawner.mu.Unlock()
			return nil, err
		}
		spawner.mu.Lock()
		instance.release = release
		spawner.mu.Unlock()
	}
	closeInstance := func() {
		spawner.mu.Lock()
		if current, _ := spawner.instances.Get(key); current != instance {
			spawner.mu.Unlock()
			return
		}
		spawner.instances.Delete(key)
		release := instance.release
		spawner.mu.Unlock()
		if release != nil {
			release()
		}
	}
	if err := spawner.lifecycle.own(func(context.Context) error { closeInstance(); return nil }); err != nil {
		closeInstance()
		return nil, err
	}
	return closeInstance, nil
}

// ProvideMany declares ownership of keyed service def and returns its
// deferred spawning capability.
func ProvideMany[T any](env *FacetEnvironment, def pico3.ServiceDefinition[T]) (*ServiceSpawner[T], error) {
	runtime := env.runtime
	runtime.lifecycle.mu.Lock()
	err := runtime.lifecycle.assertSettingUp("provide service instances")
	runtime.lifecycle.mu.Unlock()
	if err != nil {
		return nil, err
	}
	recordReference(&runtime.provides, def.Id(), def.Local(), ServiceKeyed)
	spawner := &ServiceSpawner[T]{
		lifecycle: runtime.lifecycle,
		instances: newOrderedMap[string, *stagedInstance[T]](),
		validate: func(key string, implementation T) error {
			if key == "" {
				return errors.New("Facet service instance key must not be empty")
			}
			if !def.Local() {
				_, err := classifyImplementation[T](def.Id(), implementation)
				return err
			}
			return nil
		},
	}
	runtime.provisions = append(runtime.provisions, &facetProvision{
		kind: ServiceKeyed, serviceId: def.Id(), local: def.Local(),
		connect: func(kernel *facetKernel) error {
			return spawner.connect(func(key string, implementation T) (func(), error) {
				releaseLocal, err := kernel.keyed.spawn(def.Id(), key, implementation)
				if err != nil {
					return nil, err
				}
				if def.Local() {
					return releaseLocal, nil
				}
				closeRemote, err := Spawn(kernel.provider, def, key, implementation)
				if err != nil {
					releaseLocal()
					return nil, err
				}
				return func() {
					if err := closeRemote(); err != nil {
						kernel.onError(err)
					}
					releaseLocal()
				}, nil
			})
		},
	})
	return spawner, nil
}

// ServiceRef is a facet's stable handle to a singleton service (upstream
// ServiceSlot view). Get returns the service's registered view, whose every
// method resolves the currently bound implementation and checks the facet's
// access, so a retained view or method value follows a reload cutover and
// fails after revocation or disposal.
type ServiceRef[T any] struct {
	slot   *serviceSlot
	access func() error
	once   sync.Once
	view   T
	err    error
}

// Get returns the guarded view, or an error when the facet's service access
// is currently revoked or no view is registered for the service.
func (ref *ServiceRef[T]) Get() (T, error) {
	var zero T
	if err := ref.access(); err != nil {
		return zero, err
	}
	ref.once.Do(func() {
		view, err := serviceView(ref.slot.serviceId, ref.resolve)
		if err != nil {
			ref.err = err
			return
		}
		ref.view = view.(T)
	})
	return ref.view, ref.err
}

func (ref *ServiceRef[T]) resolve() (any, error) {
	if err := ref.access(); err != nil {
		return nil, err
	}
	return ref.slot.resolve()
}

type serviceSlot struct {
	serviceId      string
	mu             sync.Mutex
	implementation any
	bound          bool
}

func (slot *serviceSlot) bind(implementation any) {
	slot.mu.Lock()
	slot.implementation, slot.bound = implementation, true
	slot.mu.Unlock()
}

func (slot *serviceSlot) unbind() {
	slot.mu.Lock()
	slot.implementation, slot.bound = nil, false
	slot.mu.Unlock()
}

func (slot *serviceSlot) resolve() (any, error) {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if !slot.bound {
		return nil, fmt.Errorf("Service %s is disconnected", slot.serviceId)
	}
	return slot.implementation, nil
}

// UseService declares a hard dependency on singleton def and returns its
// stable handle. The handle is usable once the facet activates.
func UseService[T any](env *FacetEnvironment, def pico3.ServiceDefinition[T]) (*ServiceRef[T], error) {
	runtime := env.runtime
	runtime.lifecycle.mu.Lock()
	err := runtime.lifecycle.assertSettingUp("acquire services")
	runtime.lifecycle.mu.Unlock()
	if err != nil {
		return nil, err
	}
	recordReference(&runtime.requires, def.Id(), def.Local(), ServiceSingleton)
	return &ServiceRef[T]{slot: env.kernel.singletonSlot(def.Id()), access: func() error {
		if err := env.kernel.assertServiceTargetAccess(); err != nil {
			return err
		}
		return runtime.lifecycle.assertServiceAccess()
	}}, nil
}

// ObserveService declares a hard dependency on keyed def. After activation,
// handler runs on its own goroutine for each live instance with a context
// cancelled when that instance closes or the facet is disposed.
func ObserveService[T any](env *FacetEnvironment, def pico3.ServiceDefinition[T], handler func(context.Context, T) error) error {
	runtime := env.runtime
	runtime.lifecycle.mu.Lock()
	defer runtime.lifecycle.mu.Unlock()
	if err := runtime.lifecycle.assertSettingUp("observe services"); err != nil {
		return err
	}
	recordReference(&runtime.requires, def.Id(), def.Local(), ServiceKeyed)
	runtime.lifecycle.observations = append(runtime.lifecycle.observations, func() func() {
		return env.kernel.observeKeyed(def.Id(), func(ctx context.Context, implementation any) error {
			if err := runtime.lifecycle.assertServiceAccess(); err != nil {
				return err
			}
			view, err := serviceView(def.Id(), func() (any, error) {
				if err := env.kernel.assertServiceTargetAccess(); err != nil {
					return nil, err
				}
				if err := runtime.lifecycle.assertServiceAccess(); err != nil {
					return nil, err
				}
				if ctx.Err() != nil {
					return nil, fmt.Errorf("Keyed service %s observation is closed", def.Id())
				}
				return implementation, nil
			})
			if err != nil {
				return err
			}
			return handler(ctx, view.(T))
		})
	})
	return nil
}

// localKeyedRegistry is upstream LocalKeyedServiceRegistry, used for every
// keyed service a host's facets provide (remote ones are additionally
// spawned on the provider).
type localKeyedRegistry struct {
	mu            sync.Mutex
	onError       func(error)
	registrations map[string]*localKeyedRegistration
	disposed      bool
}

type localKeyedRegistration struct {
	generations map[string]int
	entries     *orderedMap[string, *localInstance]
	observers   *orderedMap[*localObserver, bool]
}

type localInstance struct {
	key            string
	generation     int
	implementation any
}

type localObserver struct {
	handler func(context.Context, any) error
	tasks   map[*localInstance]context.CancelFunc
	closed  bool
}

func newLocalKeyedRegistry(serviceIds []string, onError func(error)) *localKeyedRegistry {
	registry := &localKeyedRegistry{onError: onError, registrations: map[string]*localKeyedRegistration{}}
	for _, id := range serviceIds {
		registry.registrations[id] = &localKeyedRegistration{generations: map[string]int{}, entries: newOrderedMap[string, *localInstance](), observers: newOrderedMap[*localObserver, bool]()}
	}
	return registry
}

func (registry *localKeyedRegistry) spawn(serviceId, key string, implementation any) (func(), error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.disposed {
		return nil, errors.New("Local keyed service registry is disposed")
	}
	registration, ok := registry.registrations[serviceId]
	if !ok {
		return nil, fmt.Errorf("Local keyed service %s is not registered", serviceId)
	}
	if _, exists := registration.entries.Get(key); exists {
		return nil, fmt.Errorf("Local service %s already has a live instance with key %s", serviceId, key)
	}
	registration.generations[key]++
	instance := &localInstance{key: key, generation: registration.generations[key], implementation: implementation}
	registration.entries.Set(key, instance)
	for _, observer := range registration.observers.Keys() {
		registry.startLocked(observer, instance)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			registry.mu.Lock()
			defer registry.mu.Unlock()
			if current, _ := registration.entries.Get(key); current != instance {
				return
			}
			registration.entries.Delete(key)
			for _, observer := range registration.observers.Keys() {
				if cancel, ok := observer.tasks[instance]; ok {
					cancel()
					delete(observer.tasks, instance)
				}
			}
		})
	}, nil
}

func (registry *localKeyedRegistry) observe(serviceId string, handler func(context.Context, any) error) func() {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registration, ok := registry.registrations[serviceId]
	if !ok || registry.disposed {
		registry.onError(fmt.Errorf("Service %s is disconnected", serviceId))
		return func() {}
	}
	observer := &localObserver{handler: handler, tasks: map[*localInstance]context.CancelFunc{}}
	registration.observers.Set(observer, true)
	for _, instance := range registration.entries.Values() {
		registry.startLocked(observer, instance)
	}
	return func() {
		registry.mu.Lock()
		defer registry.mu.Unlock()
		if observer.closed {
			return
		}
		observer.closed = true
		for _, cancel := range observer.tasks {
			cancel()
		}
		clear(observer.tasks)
		registration.observers.Delete(observer)
	}
}

func (registry *localKeyedRegistry) startLocked(observer *localObserver, instance *localInstance) {
	if observer.closed {
		return
	}
	if _, running := observer.tasks[instance]; running {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	observer.tasks[instance] = cancel
	onError := registry.onError
	go func() {
		var err error
		func() {
			defer recoverInto(&err)
			err = observer.handler(ctx, instance.implementation)
		}()
		if err != nil && ctx.Err() == nil {
			onError(err)
		}
	}()
}

func (registry *localKeyedRegistry) dispose() {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.disposed {
		return
	}
	registry.disposed = true
	for _, registration := range registry.registrations {
		for _, observer := range registration.observers.Keys() {
			observer.closed = true
			for _, cancel := range observer.tasks {
				cancel()
			}
			clear(observer.tasks)
		}
		registration.observers.Clear()
		registration.entries.Clear()
	}
}

type generationPhase string

const (
	phaseSetup      generationPhase = "setup"
	phaseAssembling generationPhase = "assembling"
	phaseConnecting generationPhase = "connecting"
	phaseActivating generationPhase = "activating"
	phaseActive     generationPhase = "active"
	phaseReloading  generationPhase = "reloading"
	phaseDisposing  generationPhase = "disposing"
	phaseDead       generationPhase = "dead"
)

type externalService struct {
	mode   ServiceMode
	source RemoteServiceSource
}

type sourceBinding struct {
	source   RemoteServiceSource
	services RemoteServices
}

// facetKernel is upstream FacetKernel.
type facetKernel struct {
	onError        func(error)
	sources        []RemoteServiceSource
	remoteClients  map[string]func(*RemoteService) any
	sourceBindings []sourceBinding
	// internal is upstream's #internalServices: a loopback binding through
	// which facets use remotely exposable in-host singletons, so a retained
	// view's state is a replica that follows provider replacement.
	internal        *RemoteServiceBinding
	externalKeyed   map[string]RemoteServices
	mu              sync.Mutex
	phase           generationPhase
	facets          map[string]*facetRuntime
	facetOrder      []string
	activationOrder []string
	slots           map[string]*serviceSlot
	provider        *RemoteServiceProvider
	keyed           *localKeyedRegistry
}

// FacetHost is an active facet generation (upstream FacetHost).
type FacetHost struct {
	kernel *facetKernel
}

// CreateFacetHost sets up, validates, assembles and activates facets in dependency order. Binding readiness uses a background context. On failure everything already set up is disposed.
func CreateFacetHost(ctx context.Context, options FacetOptions) (*FacetHost, error) {
	ids := map[string]bool{}
	for _, facet := range options.Facets {
		if facet.Id == "" {
			return nil, errors.New("Facet ID must not be empty")
		}
		if ids[facet.Id] {
			return nil, errors.New("Facet IDs must be unique within a generation")
		}
		ids[facet.Id] = true
	}
	onError := options.OnError
	if onError == nil {
		onError = func(error) {}
	}
	kernel := &facetKernel{
		onError: onError, sources: options.ServiceSources, remoteClients: options.RemoteClients,
		phase: phaseSetup, facets: map[string]*facetRuntime{}, slots: map[string]*serviceSlot{},
		externalKeyed: map[string]RemoteServices{},
	}
	if err := kernel.activate(ctx, options.Facets); err != nil {
		return nil, err
	}
	return &FacetHost{kernel: kernel}, nil
}

// Services is the host's remote service provider, for an endpoint.
func (host *FacetHost) Services() *RemoteServiceProvider { return host.kernel.provider }

// Reload activates replacements for facets with matching IDs and cuts over
// their provisions without disconnecting consumer handles.
func (host *FacetHost) Reload(ctx context.Context, facets []Facet) error {
	return host.kernel.reload(ctx, facets)
}

// Dispose disposes facets in reverse activation order, then services.
func (host *FacetHost) Dispose(ctx context.Context) error {
	return host.kernel.dispose(ctx)
}

func (kernel *facetKernel) setPhase(phase generationPhase) {
	kernel.mu.Lock()
	kernel.phase = phase
	kernel.mu.Unlock()
}

func (kernel *facetKernel) currentPhase() generationPhase {
	kernel.mu.Lock()
	defer kernel.mu.Unlock()
	return kernel.phase
}

func (kernel *facetKernel) assertServiceTargetAccess() error {
	switch phase := kernel.currentPhase(); phase {
	case phaseActivating, phaseActive, phaseReloading, phaseDisposing:
		return nil
	default:
		return fmt.Errorf("Facet service targets cannot be used during %s", phase)
	}
}

func (kernel *facetKernel) singletonSlot(serviceId string) *serviceSlot {
	kernel.mu.Lock()
	defer kernel.mu.Unlock()
	slot, ok := kernel.slots[serviceId]
	if !ok {
		slot = &serviceSlot{serviceId: serviceId}
		kernel.slots[serviceId] = slot
	}
	return slot
}

func (kernel *facetKernel) setupFacet(facet Facet) (*facetRuntime, error) {
	runtime := &facetRuntime{facetId: facet.Id, lifecycle: &facetLifecycle{id: facet.Id, state: lifecycleSettingUp}}
	if facet.Setup != nil {
		if err := safeCall(func() error { return facet.Setup(&FacetEnvironment{kernel: kernel, runtime: runtime}) }); err != nil {
			return runtime, err
		}
	}
	runtime.lifecycle.mu.Lock()
	runtime.lifecycle.state = lifecyclePrepared
	runtime.lifecycle.mu.Unlock()
	return runtime, nil
}

func (kernel *facetKernel) activate(ctx context.Context, facets []Facet) (err error) {
	defer func() {
		if err != nil {
			if cleanup := kernel.terminate(ctx, nil); len(cleanup) > 0 {
				err = fmt.Errorf("Facet generation startup and cleanup failed: %w", errors.Join(append([]error{err}, cleanup...)...))
			}
		}
	}()
	var records []*facetRuntime
	for _, facet := range facets {
		record, err := kernel.setupFacet(facet)
		kernel.facets[facet.Id] = record
		kernel.facetOrder = append(kernel.facetOrder, facet.Id)
		if err != nil {
			return err
		}
		records = append(records, record)
	}
	kernel.setPhase(phaseAssembling)
	external, err := kernel.resolveExternalServices(ctx, records)
	if err != nil {
		return err
	}
	order, err := validateFacets(records, external)
	if err != nil {
		return err
	}
	kernel.activationOrder = order
	if err := kernel.assemble(); err != nil {
		return err
	}
	if err := kernel.bindServices(external); err != nil {
		return err
	}
	kernel.setPhase(phaseConnecting)
	ready := []RemoteServices{kernel.internal}
	for _, binding := range kernel.sourceBindings {
		ready = append(ready, binding.services)
	}
	if err := allOrFirstError(len(ready), func(index int) error {
		return ready[index].Ready(context.Background())
	}); err != nil {
		return err
	}
	kernel.setPhase(phaseActivating)
	for _, id := range kernel.activationOrder {
		if err := kernel.facets[id].lifecycle.activateNow(ctx); err != nil {
			return err
		}
	}
	kernel.setPhase(phaseActive)
	return nil
}

func (kernel *facetKernel) provisions() []*facetProvision {
	var provisions []*facetProvision
	for _, id := range kernel.facetOrder {
		if record, ok := kernel.facets[id]; ok {
			provisions = append(provisions, record.provisions...)
		}
	}
	return provisions
}

func (kernel *facetKernel) assemble() error {
	provisions := kernel.provisions()
	var remote []ServiceProviderDefinition
	var keyedIds []string
	for _, provision := range provisions {
		if !provision.local {
			remote = append(remote, ServiceProviderDefinition{ServiceId: provision.serviceId, Mode: provision.kind})
		}
		if provision.kind == ServiceKeyed {
			keyedIds = append(keyedIds, provision.serviceId)
		}
	}
	provider, err := NewRemoteServiceProvider(remote...)
	if err != nil {
		return err
	}
	kernel.provider = provider
	var remoteIds []string
	for _, definition := range remote {
		remoteIds = append(remoteIds, definition.ServiceId)
	}
	internal, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{
		Services: remoteIds, Transport: NewLoopbackTransport(provider),
		AssertAccess: kernel.assertServiceTargetAccess, OnError: kernel.onError,
	})
	if err != nil {
		return err
	}
	kernel.internal = internal
	kernel.keyed = newLocalKeyedRegistry(keyedIds, kernel.onError)
	for _, provision := range provisions {
		if provision.kind == ServiceSingleton {
			if !provision.local {
				if err := provision.install(provider); err != nil {
					return err
				}
			}
		} else if err := provision.connect(kernel); err != nil {
			return err
		}
	}
	return nil
}

// resolveExternalServices opens each source for the requirements it owns
// (upstream #resolveExternalServices).
func (kernel *facetKernel) resolveExternalServices(ctx context.Context, records []*facetRuntime) (map[string]externalService, error) {
	offered := map[string]externalService{}
	catalogues := make([][]ServiceCatalogueEntry, len(kernel.sources))
	if err := allOrFirstError(len(kernel.sources), func(index int) error {
		entries, err := kernel.sources[index].Catalogue(ctx)
		catalogues[index] = entries
		return err
	}); err != nil {
		return nil, err
	}
	for index, source := range kernel.sources {
		for _, entry := range catalogues[index] {
			if _, exists := offered[entry.ServiceId]; exists {
				return nil, fmt.Errorf("Facet host service %s is offered by more than one source", entry.ServiceId)
			}
			offered[entry.ServiceId] = externalService{mode: entry.Mode, source: source}
		}
	}
	local := map[string]bool{}
	for _, record := range records {
		for _, provision := range record.provides {
			local[provision.serviceId] = true
		}
	}
	external := map[string]externalService{}
	var externalOrder []string
	for _, record := range records {
		for _, requirement := range record.requires {
			if local[requirement.serviceId] {
				continue
			}
			if _, done := external[requirement.serviceId]; done {
				continue
			}
			source, ok := offered[requirement.serviceId]
			if !ok {
				var deferred []RemoteServiceSource
				for _, candidate := range kernel.sources {
					if candidate.AcceptsUnavailableServices() {
						deferred = append(deferred, candidate)
					}
				}
				if len(deferred) > 1 {
					return nil, fmt.Errorf("Facet host service %s has more than one deferred source", requirement.serviceId)
				}
				if len(deferred) == 1 {
					source, ok = externalService{mode: requirement.mode, source: deferred[0]}, true
				}
			}
			if ok {
				external[requirement.serviceId] = source
				externalOrder = append(externalOrder, requirement.serviceId)
			}
		}
	}
	for _, candidate := range kernel.sources {
		var ids []string
		for _, id := range externalOrder {
			if external[id].source == candidate {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			continue
		}
		services, err := candidate.Open(RemoteServiceSourceOpenOptions{Services: ids, AssertAccess: kernel.assertServiceTargetAccess, OnError: kernel.onError})
		if err != nil {
			return nil, err
		}
		kernel.sourceBindings = append(kernel.sourceBindings, sourceBinding{source: candidate, services: services})
	}
	return external, nil
}

// bindServices binds each used singleton slot. In-host provisions bind to the
// providing facet's implementation (upstream routes remote in-host use through
// a loopback binding; the slot resolves to the same implementation the
// provider invokes). External services bind through their source's
// RemoteServices and the host's RemoteClients adapter.
func (kernel *facetKernel) bindServices(external map[string]externalService) error {
	for _, provision := range kernel.provisions() {
		if provision.kind != ServiceSingleton {
			continue
		}
		kernel.mu.Lock()
		slot := kernel.slots[provision.serviceId]
		kernel.mu.Unlock()
		if slot == nil {
			continue
		}
		if provision.local {
			slot.bind(provision.implementation)
			continue
		}
		adapt, ok := kernel.remoteClient(provision.serviceId)
		if !ok {
			return fmt.Errorf("In-host remote service %s has no remote client adapter (RegisterRemoteClient)", provision.serviceId)
		}
		remote, err := kernel.internal.Use(provision.serviceId)
		if err != nil {
			return err
		}
		slot.bind(adapt(remote))
	}
	for serviceId, service := range external {
		var services RemoteServices
		for _, binding := range kernel.sourceBindings {
			if binding.source == service.source {
				services = binding.services
			}
		}
		if services == nil {
			return fmt.Errorf("Service source for %s is not open", serviceId)
		}
		adapt, ok := kernel.remoteClient(serviceId)
		if !ok {
			return fmt.Errorf("External service %s has no remote client adapter", serviceId)
		}
		if service.mode == ServiceKeyed {
			kernel.externalKeyed[serviceId] = services
			continue
		}
		remote, err := services.Use(serviceId)
		if err != nil {
			return err
		}
		kernel.mu.Lock()
		slot := kernel.slots[serviceId]
		kernel.mu.Unlock()
		if slot != nil {
			slot.bind(adapt(remote))
		}
	}
	return nil
}

// observeKeyed starts one keyed observation from an in-host provision or an
// external source.
func (kernel *facetKernel) observeKeyed(serviceId string, handler func(context.Context, any) error) func() {
	if services, ok := kernel.externalKeyed[serviceId]; ok {
		adapt, _ := kernel.remoteClient(serviceId)
		stop, err := services.Observe(serviceId, func(ctx context.Context, remote *RemoteService) error {
			return handler(ctx, adapt(remote))
		})
		if err != nil {
			kernel.onError(err)
			return func() {}
		}
		return stop
	}
	return kernel.keyed.observe(serviceId, handler)
}

// reload and dispose claim the generation by an atomic phase transition
// instead of a lock held across user callbacks, so a callback that re-enters
// Reload or Dispose is rejected for the non-active phase, as upstream.
func (kernel *facetKernel) reload(ctx context.Context, facets []Facet) error {
	kernel.mu.Lock()
	if kernel.phase != phaseActive {
		phase := kernel.phase
		kernel.mu.Unlock()
		return fmt.Errorf("Facet host cannot reload while %s", phase)
	}
	ids := map[string]bool{}
	for _, facet := range facets {
		var err error
		switch {
		case facet.Id == "":
			err = errors.New("Facet ID must not be empty")
		case ids[facet.Id]:
			err = errors.New("Reloaded facet IDs must be unique")
		case kernel.facets[facet.Id] == nil:
			err = fmt.Errorf("Facet %s is not active", facet.Id)
		}
		if err != nil {
			kernel.mu.Unlock()
			return err
		}
		ids[facet.Id] = true
	}
	kernel.phase = phaseReloading
	kernel.mu.Unlock()

	var staged, candidates []*facetRuntime
	stageErr := func() error {
		for _, facet := range facets {
			record, err := kernel.setupFacet(facet)
			staged = append(staged, record)
			if err != nil {
				return err
			}
			if !sameFacetShape(kernel.facets[facet.Id], record) {
				return fmt.Errorf("Reloaded facet %s must preserve its service requirements and provisions", facet.Id)
			}
			if err := kernel.validateReplacementProvisions(record.provisions); err != nil {
				return err
			}
			candidates = append(candidates, record)
		}
		return nil
	}()
	if stageErr != nil {
		slices.Reverse(staged)
		if cleanup := disposeRecords(ctx, staged); len(cleanup) > 0 {
			abortErrors := kernel.abort(ctx, nil)
			return fmt.Errorf("Facet reload setup and cleanup failed: %w", errors.Join(append(append([]error{stageErr}, cleanup...), abortErrors...)...))
		}
		kernel.setPhase(phaseActive)
		return stageErr
	}

	replacements := map[string]*facetRuntime{}
	for _, record := range candidates {
		replacements[record.facetId] = record
	}
	var candidateOrder []*facetRuntime
	for _, id := range kernel.activationOrder {
		if candidate, ok := replacements[id]; ok {
			candidateOrder = append(candidateOrder, candidate)
		}
	}
	activateErr := func() error {
		for _, candidate := range candidateOrder {
			if err := candidate.lifecycle.activateNow(ctx); err != nil {
				return err
			}
		}
		for _, candidate := range candidateOrder {
			if err := kernel.validateReplacementProvisions(candidate.provisions); err != nil {
				return err
			}
		}
		return nil
	}()
	if activateErr != nil {
		reversed := slices.Clone(candidateOrder)
		slices.Reverse(reversed)
		if cleanup := disposeRecords(ctx, reversed); len(cleanup) > 0 {
			abortErrors := kernel.abort(ctx, nil)
			return fmt.Errorf("Facet reload activation and cleanup failed: %w", errors.Join(append(append([]error{activateErr}, cleanup...), abortErrors...)...))
		}
		kernel.setPhase(phaseActive)
		return activateErr
	}

	previous := make([]*facetRuntime, len(candidateOrder))
	for index, candidate := range candidateOrder {
		previous[index] = kernel.facets[candidate.facetId]
		kernel.facets[candidate.facetId] = candidate
	}
	cutoverErr := func() error {
		for _, candidate := range candidateOrder {
			for _, provision := range candidate.provisions {
				if provision.kind != ServiceSingleton {
					continue
				}
				if !provision.local {
					// The slot stays bound to the stable loopback facade;
					// the provider's "replaced" update rehydrates it.
					if err := provision.replace(kernel.provider); err != nil {
						return err
					}
					continue
				}
				kernel.mu.Lock()
				slot := kernel.slots[provision.serviceId]
				kernel.mu.Unlock()
				if slot != nil {
					slot.bind(provision.implementation)
				}
			}
		}
		retiring := slices.Clone(previous)
		slices.Reverse(retiring)
		if errs := disposeRecords(ctx, retiring); len(errs) > 0 {
			if len(errs) == 1 {
				return errs[0]
			}
			return fmt.Errorf("Failed to retire replaced facets: %w", errors.Join(errs...))
		}
		for _, candidate := range candidateOrder {
			for _, provision := range candidate.provisions {
				if provision.kind == ServiceKeyed {
					if err := provision.connect(kernel); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}()
	if cutoverErr != nil {
		abortErrors := kernel.abort(ctx, previous)
		return fmt.Errorf("Facet reload failed after cutover: %w", errors.Join(append([]error{cutoverErr}, abortErrors...)...))
	}
	kernel.setPhase(phaseActive)
	return nil
}

func (kernel *facetKernel) validateReplacementProvisions(provisions []*facetProvision) error {
	for _, provision := range provisions {
		if provision.kind != ServiceSingleton || provision.local {
			continue
		}
		if err := provision.validateReplacement(kernel.provider); err != nil {
			return err
		}
	}
	return nil
}

func (kernel *facetKernel) dispose(ctx context.Context) error {
	kernel.mu.Lock()
	switch phase := kernel.phase; phase {
	case phaseDead:
		kernel.mu.Unlock()
		return nil
	case phaseActive:
		kernel.phase = phaseDisposing
		kernel.mu.Unlock()
	default:
		kernel.mu.Unlock()
		return fmt.Errorf("Facet host cannot be disposed while %s", phase)
	}
	errs := kernel.terminate(ctx, nil)
	if len(errs) > 1 {
		return fmt.Errorf("Failed to dispose facet generation: %w", errors.Join(errs...))
	}
	return joinErrors(errs)
}

func (kernel *facetKernel) abort(ctx context.Context, extra []*facetRuntime) []error {
	for _, record := range kernel.facets {
		record.lifecycle.revoke()
	}
	for _, record := range extra {
		record.lifecycle.revoke()
	}
	return kernel.terminate(ctx, extra)
}

func (kernel *facetKernel) terminate(ctx context.Context, extra []*facetRuntime) []error {
	kernel.setPhase(phaseDisposing)
	var errs []error
	order := slices.Clone(kernel.activationOrder)
	if len(order) == 0 {
		order = slices.Clone(kernel.facetOrder)
	}
	slices.Reverse(order)
	for _, id := range order {
		record, ok := kernel.facets[id]
		if !ok {
			continue
		}
		delete(kernel.facets, id)
		if err := record.lifecycle.dispose(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	reversed := slices.Clone(extra)
	slices.Reverse(reversed)
	errs = append(errs, disposeRecords(ctx, reversed)...)
	if kernel.keyed != nil {
		kernel.keyed.dispose()
	}
	var bindings []RemoteServices
	for _, binding := range kernel.sourceBindings {
		bindings = append(bindings, binding.services)
	}
	if kernel.internal != nil {
		bindings = append(bindings, kernel.internal)
	}
	errs = append(errs, allSettled(len(bindings), func(index int) error {
		return bindings[index].Dispose(ctx)
	})...)
	kernel.sourceBindings = nil
	kernel.internal = nil
	kernel.mu.Lock()
	for _, slot := range kernel.slots {
		slot.unbind()
	}
	clear(kernel.slots)
	kernel.mu.Unlock()
	if kernel.provider != nil {
		if err := kernel.provider.Dispose(); err != nil {
			errs = append(errs, err)
		}
	}
	kernel.setPhase(phaseDead)
	return errs
}

func disposeRecords(ctx context.Context, records []*facetRuntime) []error {
	var errs []error
	for _, record := range records {
		if err := record.lifecycle.dispose(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

type facetProvider struct {
	facetId string
	mode    ServiceMode
}

// validateFacets is upstream validateFacets; external services are provided
// by the host (facetId "").
func validateFacets(records []*facetRuntime, external map[string]externalService) ([]string, error) {
	providers := map[string]facetProvider{}
	for serviceId, service := range external {
		providers[serviceId] = facetProvider{mode: service.mode}
	}
	for _, record := range records {
		for _, provision := range record.provides {
			if existing, ok := providers[provision.serviceId]; ok {
				if existing.mode != provision.mode {
					return nil, fmt.Errorf("Service %s is provided as both singleton and keyed", provision.serviceId)
				}
				if existing.facetId == "" {
					return nil, fmt.Errorf("Service %s is provided by both the host and %s", provision.serviceId, record.facetId)
				}
				return nil, fmt.Errorf("Service %s is provided by both %s and %s", provision.serviceId, existing.facetId, record.facetId)
			}
			providers[provision.serviceId] = facetProvider{facetId: record.facetId, mode: provision.mode}
		}
	}
	dependencies := map[string]map[string]bool{}
	dependents := map[string][]string{}
	for _, record := range records {
		dependencies[record.facetId] = map[string]bool{}
	}
	for _, record := range records {
		for _, requirement := range record.requires {
			provider, ok := providers[requirement.serviceId]
			if !ok {
				return nil, fmt.Errorf("Facet %s requires local/%s/%s, but no facet provides it", record.facetId, requirement.serviceId, requirement.mode)
			}
			if provider.mode != requirement.mode {
				by := provider.facetId
				if by == "" {
					by = "the host"
				}
				return nil, fmt.Errorf("Facet %s requires %s as %s, but %s provides it as %s", record.facetId, requirement.serviceId, requirement.mode, by, provider.mode)
			}
			if provider.facetId == "" || provider.facetId == record.facetId || dependencies[record.facetId][provider.facetId] {
				continue
			}
			dependencies[record.facetId][provider.facetId] = true
			dependents[provider.facetId] = append(dependents[provider.facetId], record.facetId)
		}
	}
	remaining := map[string]int{}
	var ready []string
	for _, record := range records {
		remaining[record.facetId] = len(dependencies[record.facetId])
		if remaining[record.facetId] == 0 {
			ready = append(ready, record.facetId)
		}
	}
	var order []string
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, dependent := range dependents[id] {
			remaining[dependent]--
			if remaining[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	if len(order) != len(records) {
		var cycle []string
		for _, record := range records {
			if remaining[record.facetId] > 0 {
				cycle = append(cycle, record.facetId)
			}
		}
		return nil, fmt.Errorf("Facet dependency cycle: %s", strings.Join(cycle, ", "))
	}
	return order, nil
}

func sameFacetShape(left, right *facetRuntime) bool {
	return sameReferences(left.requires, right.requires) && sameReferences(left.provides, right.provides)
}

func sameReferences(left, right []serviceReference) bool {
	if len(left) != len(right) {
		return false
	}
	for _, reference := range left {
		if !slices.ContainsFunc(right, func(other serviceReference) bool {
			return other.serviceId == reference.serviceId && other.mode == reference.mode
		}) {
			return false
		}
	}
	return true
}

// safeCall runs a user callback and converts a panic into its error, the Go
// counterpart of upstream's catch around thrown setup, activation and
// cleanup callbacks.
func safeCall(callback func() error) (err error) {
	defer recoverInto(&err)
	return callback()
}

// allOrFirstError runs count operations concurrently and returns as soon as
// one fails, or nil after all succeed (upstream Promise.all).
func allOrFirstError(count int, run func(index int) error) error {
	results := make(chan error, count)
	for index := range count {
		go func() { results <- safeCall(func() error { return run(index) }) }()
	}
	for range count {
		if err := <-results; err != nil {
			return err
		}
	}
	return nil
}

// allSettled runs count operations concurrently, waits for all of them, and
// returns their failures in index order (upstream Promise.allSettled).
func allSettled(count int, run func(index int) error) []error {
	failures := make([]error, count)
	var group sync.WaitGroup
	for index := range count {
		group.Go(func() { failures[index] = safeCall(func() error { return run(index) }) })
	}
	group.Wait()
	var errs []error
	for _, err := range failures {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

var remoteClients sync.Map // service ID -> func(*RemoteService) any

// RegisterRemoteClient registers the typed client adapter for def: a T whose
// methods call the remote facade (Call/CallResult/State). A facet host uses
// it for remotely exposable services, in-host (through its internal loopback
// binding, as upstream) and from external sources. FacetOptions.RemoteClients
// overrides it per host. Registering the same service twice panics.
func RegisterRemoteClient[T any](def pico3.ServiceDefinition[T], adapter func(*RemoteService) T) {
	if _, loaded := remoteClients.LoadOrStore(def.Id(), func(service *RemoteService) any { return adapter(service) }); loaded {
		panic(fmt.Sprintf("chord: remote client for %s is already registered", def.Id()))
	}
}

func (kernel *facetKernel) remoteClient(serviceId string) (func(*RemoteService) any, bool) {
	if adapt, ok := kernel.remoteClients[serviceId]; ok {
		return adapt, true
	}
	adapt, ok := remoteClients.Load(serviceId)
	if !ok {
		return nil, false
	}
	return adapt.(func(*RemoteService) any), true
}
