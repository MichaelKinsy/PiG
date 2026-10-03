package chord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// ServiceProviderDefinition registers one service in a provider catalogue.
type ServiceProviderDefinition struct {
	ServiceId string
	Local     bool
	Mode      ServiceMode
}

// SingletonService registers def as a singleton catalogue entry.
func SingletonService[T any](def ServiceDefinition[T]) ServiceProviderDefinition {
	return ServiceProviderDefinition{ServiceId: def.Id(), Local: def.Local(), Mode: ServiceSingleton}
}

// KeyedService registers def as a keyed (multi-instance) catalogue entry.
func KeyedService[T any](def ServiceDefinition[T]) ServiceProviderDefinition {
	return ServiceProviderDefinition{ServiceId: def.Id(), Local: def.Local(), Mode: ServiceKeyed}
}

type instanceMember struct {
	kind   string
	method reflect.Value
	invoke func(context.Context, []json.RawMessage) (json.RawMessage, error)
	state  *stateCore
}

type classifiedImplementation struct {
	implementation any
	members        map[string]instanceMember
	names          []string
}

type providerInstance struct {
	address        *ServiceInstanceAddress
	implementation any
	members        map[string]instanceMember
	names          []string
	removeSources  []func()
	active         bool
}

type providerSubscriber struct {
	listener          UpdateListener
	buffer            []bufferedUpdate
	snapshotSequences map[string]int
	active            bool
	draining          bool
	terminated        bool
	closed            bool
}

type bufferedUpdate struct {
	update ServiceProviderUpdate
	ctx    context.Context
}

type serviceRegistration struct {
	serviceId      string
	mode           ServiceMode
	singleton      *providerInstance
	singletonShape map[string]string
	instances      *orderedMap[string, *providerInstance]
	generations    map[string]int
	subscribers    []*providerSubscriber
}

// RemoteServiceProvider hosts the allowlisted remote services of one process
// (upstream RemoteServiceProvider). It is safe for concurrent use; provider
// updates are delivered outside its lock in publication order.
type RemoteServiceProvider struct {
	mu            sync.Mutex
	catalogue     []ServiceCatalogueEntry
	registrations map[string]*serviceRegistration
	disposed      bool
}

// maxPendingUpdates is the number of provider updates one subscription buffers before it is rebaselined with a "reset" update.
const maxPendingUpdates = 100

// NewRemoteServiceProvider creates a provider for exactly the given services.
// Local services and duplicate IDs are rejected.
func NewRemoteServiceProvider(definitions ...ServiceProviderDefinition) (*RemoteServiceProvider, error) {
	provider := &RemoteServiceProvider{registrations: map[string]*serviceRegistration{}}
	for _, definition := range definitions {
		if definition.Local {
			return nil, fmt.Errorf("Local service %s cannot be published remotely", definition.ServiceId)
		}
		if definition.Mode != ServiceSingleton && definition.Mode != ServiceKeyed {
			return nil, fmt.Errorf("invalid service mode %q", definition.Mode)
		}
		if _, exists := provider.registrations[definition.ServiceId]; exists {
			return nil, errors.New("Remote service catalogue contains duplicate IDs")
		}
		provider.catalogue = append(provider.catalogue, ServiceCatalogueEntry{ServiceId: definition.ServiceId, Mode: definition.Mode})
		provider.registrations[definition.ServiceId] = &serviceRegistration{
			serviceId:   definition.ServiceId,
			mode:        definition.Mode,
			instances:   newOrderedMap[string, *providerInstance](),
			generations: map[string]int{},
		}
	}
	return provider, nil
}

// Catalogue returns the provider's catalogue in registration order.
func (provider *RemoteServiceProvider) Catalogue() []ServiceCatalogueEntry {
	return slices.Clone(provider.catalogue)
}

// Provide installs the singleton implementation of def.
func Provide[T any](provider *RemoteServiceProvider, def ServiceDefinition[T], implementation T) error {
	classified, err := classifyImplementation[T](def.Id(), implementation)
	if err != nil {
		return err
	}
	provider.mu.Lock()
	registration, err := provider.singletonRegistrationLocked(def.Id(), def.Local())
	if err != nil {
		provider.mu.Unlock()
		return err
	}
	if registration.singleton != nil {
		provider.mu.Unlock()
		return remoteError(ErrServiceModeMismatch, "Remote service %s already has a provider", def.Id())
	}
	shape := memberShape(classified)
	if err := assertSingletonShape(registration, shape); err != nil {
		provider.mu.Unlock()
		return err
	}
	registration.singleton = provider.createInstanceLocked(registration, classified, nil)
	registration.singletonShape = shape
	provider.mu.Unlock()
	return nil
}

// Withdraw disconnects a singleton while preserving subscriptions; subscribers
// receive "unavailable".
func Withdraw[T any](provider *RemoteServiceProvider, def ServiceDefinition[T]) error {
	provider.mu.Lock()
	registration, err := provider.singletonRegistrationLocked(def.Id(), def.Local())
	if err != nil {
		provider.mu.Unlock()
		return err
	}
	previous := registration.singleton
	if previous == nil {
		provider.mu.Unlock()
		return nil
	}
	deactivate(previous)
	registration.singleton = nil
	return provider.emitUnlocking(registration, ServiceProviderUpdate{Type: UpdateUnavailable}, nil)
}

// ValidateReplacement checks a singleton replacement without changing the
// active provider.
func ValidateReplacement[T any](provider *RemoteServiceProvider, def ServiceDefinition[T], implementation T) error {
	classified, err := classifyImplementation[T](def.Id(), implementation)
	if err != nil {
		return err
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	registration, err := provider.singletonRegistrationLocked(def.Id(), def.Local())
	if err != nil {
		return err
	}
	return assertSingletonShape(registration, memberShape(classified))
}

// Replace swaps a singleton implementation without making the stable remote
// facade unavailable; subscribers receive "replaced" with a new snapshot.
func Replace[T any](provider *RemoteServiceProvider, def ServiceDefinition[T], implementation T) error {
	classified, err := classifyImplementation[T](def.Id(), implementation)
	if err != nil {
		return err
	}
	provider.mu.Lock()
	registration, err := provider.singletonRegistrationLocked(def.Id(), def.Local())
	if err != nil {
		provider.mu.Unlock()
		return err
	}
	shape := memberShape(classified)
	if err := assertSingletonShape(registration, shape); err != nil {
		provider.mu.Unlock()
		return err
	}
	replacement := provider.createInstanceLocked(registration, classified, nil)
	if registration.singleton != nil {
		deactivate(registration.singleton)
	}
	registration.singleton = replacement
	registration.singletonShape = shape
	snapshot := snapshotInstance(replacement)
	return provider.emitUnlocking(registration, ServiceProviderUpdate{Type: UpdateReplaced, Snapshot: &snapshot}, nil)
}

// Use returns the local singleton implementation of def.
func Use[T any](provider *RemoteServiceProvider, def ServiceDefinition[T]) (T, error) {
	var zero T
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if err := provider.assertAccessLocked(def.Id(), def.Local()); err != nil {
		return zero, err
	}
	registration := provider.registrations[def.Id()]
	if registration.mode != ServiceSingleton || registration.singleton == nil {
		return zero, remoteError(ErrServiceNotFound, "Remote service %s has no local provider", def.Id())
	}
	return registration.singleton.implementation.(T), nil
}

// Spawn publishes one keyed instance at the next generation for key. The returned close function is idempotent, including from a lifecycle listener, and closes only this generation.
func Spawn[T any](provider *RemoteServiceProvider, def ServiceDefinition[T], key string, implementation T) (func() error, error) {
	provider.mu.Lock()
	if err := provider.assertAccessLocked(def.Id(), def.Local()); err != nil {
		provider.mu.Unlock()
		return nil, err
	}
	if key == "" {
		provider.mu.Unlock()
		return nil, errors.New("Remote service instance key must not be empty")
	}
	registration, err := provider.registrationLocked(def.Id(), ServiceKeyed)
	if err != nil {
		provider.mu.Unlock()
		return nil, err
	}
	if _, exists := registration.instances.Get(key); exists {
		provider.mu.Unlock()
		return nil, remoteError(ErrServiceModeMismatch, "Remote service %s already has a live instance with key %s", def.Id(), key)
	}
	// Upstream advances the generation before classifying the
	// implementation, so a rejected implementation consumes a generation.
	generation := registration.generations[key] + 1
	registration.generations[key] = generation
	provider.mu.Unlock()
	classified, err := classifyImplementation[T](def.Id(), implementation)
	if err != nil {
		return nil, err
	}
	provider.mu.Lock()
	if provider.disposed {
		provider.mu.Unlock()
		return nil, errors.New("Remote service provider is disposed")
	}
	if _, exists := registration.instances.Get(key); exists {
		provider.mu.Unlock()
		return nil, remoteError(ErrServiceModeMismatch, "Remote service %s already has a live instance with key %s", def.Id(), key)
	}
	address := &ServiceInstanceAddress{Key: key, Generation: generation}
	instance := provider.createInstanceLocked(registration, classified, address)
	registration.instances.Set(key, instance)
	snapshot := snapshotInstance(instance)
	if err := provider.emitUnlocking(registration, ServiceProviderUpdate{Type: UpdateSpawned, Snapshot: &snapshot}, nil); err != nil {
		return provider.closeInstanceFunc(registration, key, instance), err
	}
	return provider.closeInstanceFunc(registration, key, instance), nil
}

func (provider *RemoteServiceProvider) closeInstanceFunc(registration *serviceRegistration, key string, instance *providerInstance) func() error {
	return func() error {
		provider.mu.Lock()
		if current, _ := registration.instances.Get(key); current != instance {
			provider.mu.Unlock()
			return nil
		}
		deactivate(instance)
		registration.instances.Delete(key)
		address := *instance.address
		return provider.emitUnlocking(registration, ServiceProviderUpdate{Type: UpdateClosed, Address: &address}, nil)
	}
}

// Invoke routes one method call and returns implementation failures, including panics, as errors. The result is nil for a void method.
func (provider *RemoteServiceProvider) Invoke(ctx context.Context, call ServiceCall) (json.RawMessage, error) {
	provider.mu.Lock()
	if provider.disposed {
		provider.mu.Unlock()
		return nil, errors.New("Remote service provider is disposed")
	}
	registration, ok := provider.registrations[call.ServiceId]
	if !ok {
		provider.mu.Unlock()
		return nil, remoteError(ErrServiceNotAllowed, "Remote service %s is not allowlisted", call.ServiceId)
	}
	instance, err := resolveInstance(registration, call.Instance)
	if err != nil {
		provider.mu.Unlock()
		return nil, err
	}
	member, ok := instance.members[call.Member]
	provider.mu.Unlock()
	if !ok {
		return nil, remoteError(ErrServiceMemberNotFound, "Unknown remote service member %s.%s", call.ServiceId, call.Member)
	}
	if member.kind != MemberMethod {
		return nil, remoteError(ErrServiceMemberMismatch, "Remote service member %s.%s is not a method", call.ServiceId, call.Member)
	}
	if member.invoke != nil {
		return invokeFacetMember(ctx, call.Args, member.invoke)
	}
	return invokeMethod(ctx, call, member.method)
}

// BeginInvoke applies Invoke's allowlist, instance and member checks, then delegates admission to an implementation that exposes it. Implementations without an admission boundary are rejected rather than started on a prematurely signalled goroutine.
func (provider *RemoteServiceProvider) BeginInvoke(ctx context.Context, call ServiceCall) (*ServiceInvocation, error) {
	provider.mu.Lock()
	if provider.disposed {
		provider.mu.Unlock()
		return nil, errors.New("Remote service provider is disposed")
	}
	registration, ok := provider.registrations[call.ServiceId]
	if !ok {
		provider.mu.Unlock()
		return nil, remoteError(ErrServiceNotAllowed, "Remote service %s is not allowlisted", call.ServiceId)
	}
	instance, err := resolveInstance(registration, call.Instance)
	if err != nil {
		provider.mu.Unlock()
		return nil, err
	}
	member, ok := instance.members[call.Member]
	implementation := instance.implementation
	provider.mu.Unlock()
	if !ok {
		return nil, remoteError(ErrServiceMemberNotFound, "Unknown remote service member %s.%s", call.ServiceId, call.Member)
	}
	if member.kind != MemberMethod {
		return nil, remoteError(ErrServiceMemberMismatch, "Remote service member %s.%s is not a method", call.ServiceId, call.Member)
	}
	initiator, ok := implementation.(ServiceMemberInitiator)
	if !ok {
		return nil, ErrInvocationAdmissionUnavailable
	}
	return initiator.BeginServiceMember(ctx, call.Member, call.Args)
}

func invokeMethod(ctx context.Context, call ServiceCall, method reflect.Value) (result json.RawMessage, err error) {
	defer recoverInto(&err)
	methodType := method.Type()
	if len(call.Args) != methodType.NumIn()-1 {
		return nil, remoteError(ErrServiceInvalidValue, "Remote service method %s.%s expects %d arguments, got %d", call.ServiceId, call.Member, methodType.NumIn()-1, len(call.Args))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	in := make([]reflect.Value, methodType.NumIn())
	in[0] = reflect.ValueOf(ctx)
	for index, arg := range call.Args {
		target := reflect.New(methodType.In(index + 1))
		if err := json.Unmarshal(arg, target.Interface()); err != nil {
			return nil, remoteError(ErrServiceInvalidValue, "Remote service method %s.%s argument %d is invalid: %v", call.ServiceId, call.Member, index, err)
		}
		in[index+1] = target.Elem()
	}
	out := method.Call(in)
	if failure := out[len(out)-1]; !failure.IsNil() {
		return nil, failure.Interface().(error)
	}
	if len(out) == 1 {
		return nil, nil
	}
	encoded, err := json.Marshal(out[0].Interface())
	if err != nil {
		return nil, remoteError(ErrServiceInvalidValue, "Remote service method %s.%s result is not JSON: %v", call.ServiceId, call.Member, err)
	}
	return encoded, nil
}

type providerSubscription struct {
	provider     *RemoteServiceProvider
	registration *serviceRegistration
	subscriber   *providerSubscriber
	snapshot     ServiceSubscriptionSnapshot
}

func (subscription *providerSubscription) Snapshot() ServiceSubscriptionSnapshot {
	return subscription.snapshot
}

// Activate delivers buffered updates in order and then streams live updates. A reentrant publication from a listener appends to the same FIFO.
func (subscription *providerSubscription) Activate() error {
	provider, subscriber := subscription.provider, subscription.subscriber
	provider.mu.Lock()
	if subscriber.closed || subscriber.active {
		provider.mu.Unlock()
		return nil
	}
	subscriber.active = true
	provider.mu.Unlock()
	return collected(provider.drainSubscriber(subscriber), "Failed to activate remote service subscription")
}

// Close stops delivery and discards buffered updates; it is idempotent.
func (subscription *providerSubscription) Close(context.Context) error {
	provider := subscription.provider
	provider.mu.Lock()
	defer provider.mu.Unlock()
	subscriber := subscription.subscriber
	if subscriber.closed {
		return nil
	}
	subscriber.closed = true
	subscriber.buffer = nil
	removeSubscriber(subscription.registration, subscriber)
	return nil
}

// Subscribe opens a subscription whose snapshot is coherent with the update
// stream: updates already covered by the snapshot are never delivered, and
// later updates are buffered until Activate.
func (provider *RemoteServiceProvider) Subscribe(serviceId string, mode ServiceMode, listener UpdateListener) (ServiceSubscription, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.disposed {
		return nil, errors.New("Remote service provider is disposed")
	}
	if _, ok := provider.registrations[serviceId]; !ok {
		return nil, remoteError(ErrServiceNotAllowed, "Remote service %s is not allowlisted", serviceId)
	}
	registration, err := provider.registrationLocked(serviceId, mode)
	if err != nil {
		return nil, err
	}
	if registration.mode == ServiceSingleton && registration.singleton == nil {
		return nil, remoteError(ErrServiceNotFound, "Remote service %s has no provider", serviceId)
	}
	subscriber := &providerSubscriber{listener: listener, snapshotSequences: map[string]int{}}
	registration.subscribers = append(registration.subscribers, subscriber)
	snapshot := snapshotRegistration(registration)
	recordSnapshotSequences(subscriber.snapshotSequences, snapshot.Instances)
	return &providerSubscription{provider: provider, registration: registration, subscriber: subscriber, snapshot: snapshot}, nil
}

// Dispose deactivates every instance, emits unavailable/closed to active
// subscribers, and terminates subscriptions. It is idempotent.
func (provider *RemoteServiceProvider) Dispose() error {
	provider.mu.Lock()
	if provider.disposed {
		provider.mu.Unlock()
		return nil
	}
	provider.disposed = true
	registrations := make([]*serviceRegistration, 0, len(provider.catalogue))
	for _, entry := range provider.catalogue {
		registrations = append(registrations, provider.registrations[entry.ServiceId])
	}
	provider.mu.Unlock()
	var errs []error
	for _, registration := range registrations {
		provider.mu.Lock()
		if registration.singleton != nil {
			deactivate(registration.singleton)
			registration.singleton = nil
			if err := provider.emitUnlocking(registration, ServiceProviderUpdate{Type: UpdateUnavailable}, nil); err != nil {
				errs = append(errs, err)
			}
			provider.mu.Lock()
		}
		// Upstream iterates the instance Map in insertion order.
		for _, key := range registration.instances.Keys() {
			instance, ok := registration.instances.Get(key)
			if !ok {
				continue
			}
			deactivate(instance)
			registration.instances.Delete(key)
			address := *instance.address
			if err := provider.emitUnlocking(registration, ServiceProviderUpdate{Type: UpdateClosed, Address: &address}, nil); err != nil {
				errs = append(errs, err)
			}
			provider.mu.Lock()
		}
		for _, subscriber := range registration.subscribers {
			if subscriber.active && !subscriber.draining {
				subscriber.closed = true
				subscriber.buffer = nil
			} else {
				subscriber.terminated = true
			}
		}
		registration.subscribers = nil
		provider.mu.Unlock()
	}
	provider.mu.Lock()
	provider.registrations = map[string]*serviceRegistration{}
	provider.mu.Unlock()
	if len(errs) > 1 {
		return NewAggregateError("Failed to dispose remote service provider", errs)
	}
	return joinErrors(errs)
}

func (provider *RemoteServiceProvider) assertAccessLocked(serviceId string, local bool) error {
	if provider.disposed {
		return errors.New("Remote service provider is disposed")
	}
	if local {
		return remoteError(ErrServiceNotAllowed, "Service %s is process-local", serviceId)
	}
	if _, ok := provider.registrations[serviceId]; !ok {
		return remoteError(ErrServiceNotAllowed, "Remote service %s is not allowlisted", serviceId)
	}
	return nil
}

func (provider *RemoteServiceProvider) singletonRegistrationLocked(serviceId string, local bool) (*serviceRegistration, error) {
	if err := provider.assertAccessLocked(serviceId, local); err != nil {
		return nil, err
	}
	return provider.registrationLocked(serviceId, ServiceSingleton)
}

func (provider *RemoteServiceProvider) registrationLocked(serviceId string, mode ServiceMode) (*serviceRegistration, error) {
	registration, ok := provider.registrations[serviceId]
	if !ok {
		return nil, remoteError(ErrServiceNotFound, "Unknown remote service %s", serviceId)
	}
	if registration.mode != mode {
		return nil, remoteError(ErrServiceModeMismatch, "Remote service %s is %s, not %s", serviceId, registration.mode, mode)
	}
	return registration, nil
}

// createInstanceLocked subscribes to each state member's operation stream.
func (provider *RemoteServiceProvider) createInstanceLocked(registration *serviceRegistration, classified classifiedImplementation, address *ServiceInstanceAddress) *providerInstance {
	instance := &providerInstance{address: address, implementation: classified.implementation, members: classified.members, names: classified.names, active: true}
	for _, name := range classified.names {
		member := classified.members[name]
		if member.kind != MemberState {
			continue
		}
		instance.removeSources = append(instance.removeSources, member.state.subscribeOps(func(ctx context.Context, ops []Op, sequence int) {
			provider.mu.Lock()
			if !instance.active {
				provider.mu.Unlock()
				return
			}
			update := ServiceProviderUpdate{Type: UpdateState, Member: name, Sequence: sequence, Ops: ops}
			if address != nil {
				copied := *address
				update.Address = &copied
			}
			if err := provider.emitUnlocking(registration, update, ctx); err != nil {
				panic(err)
			}
		}))
	}
	return instance
}

// emitUnlocking is entered with mu held and returns with it released. It queues the update for every subscriber before invoking any listener, so a publication made by a listener joins the same queues, then drains each subscriber outside mu. A subscriber with maxPendingUpdates updates waiting gets one "reset" carrying a full snapshot instead.
func (provider *RemoteServiceProvider) emitUnlocking(registration *serviceRegistration, update ServiceProviderUpdate, ctx context.Context) error {
	if ctx == nil {
		ctx = deliveryContext()
	}
	subscribers := append([]*providerSubscriber(nil), registration.subscribers...)
	for _, subscriber := range subscribers {
		if subscriber.closed || updateCoveredBySnapshot(subscriber.snapshotSequences, update) {
			continue
		}
		if len(subscriber.buffer) == maxPendingUpdates {
			snapshot := snapshotRegistration(registration)
			clear(subscriber.snapshotSequences)
			recordSnapshotSequences(subscriber.snapshotSequences, snapshot.Instances)
			subscriber.buffer = []bufferedUpdate{{update: ServiceProviderUpdate{Type: UpdateReset, Reset: &snapshot}, ctx: ctx}}
			continue
		}
		subscriber.buffer = append(subscriber.buffer, bufferedUpdate{update: update, ctx: ctx})
	}
	provider.mu.Unlock()
	var errs []error
	for _, subscriber := range subscribers {
		errs = append(errs, provider.drainSubscriber(subscriber)...)
	}
	return collected(errs, fmt.Sprintf("Failed to publish remote service %s update", registration.serviceId))
}

// drainSubscriber delivers one subscriber's queue in order, outside mu. A subscriber that is already draining, inactive or closed is left alone: the running drain delivers whatever was queued behind it.
func (provider *RemoteServiceProvider) drainSubscriber(subscriber *providerSubscriber) []error {
	provider.mu.Lock()
	if !subscriber.active || subscriber.closed || subscriber.draining {
		provider.mu.Unlock()
		return nil
	}
	subscriber.draining = true
	var errs []error
	for !subscriber.closed && len(subscriber.buffer) > 0 {
		entry := subscriber.buffer[0]
		subscriber.buffer = subscriber.buffer[1:]
		provider.mu.Unlock()
		if err := callUpdate(subscriber.listener, entry.ctx, entry.update); err != nil {
			errs = append(errs, err)
		}
		provider.mu.Lock()
	}
	subscriber.draining = false
	if subscriber.terminated {
		subscriber.closed = true
	}
	provider.mu.Unlock()
	return errs
}

func callUpdate(listener UpdateListener, ctx context.Context, update ServiceProviderUpdate) (err error) {
	defer recoverInto(&err)
	listener(ctx, update)
	return nil
}

func removeSubscriber(registration *serviceRegistration, subscriber *providerSubscriber) {
	for index, candidate := range registration.subscribers {
		if candidate == subscriber {
			registration.subscribers = append(registration.subscribers[:index:index], registration.subscribers[index+1:]...)
			return
		}
	}
}

func deactivate(instance *providerInstance) {
	instance.active = false
	for _, remove := range instance.removeSources {
		remove()
	}
}

func resolveInstance(registration *serviceRegistration, address *ServiceInstanceAddress) (*providerInstance, error) {
	if registration.mode == ServiceSingleton {
		if address != nil {
			return nil, remoteError(ErrServiceModeMismatch, "Remote service %s is singleton", registration.serviceId)
		}
		if registration.singleton == nil {
			return nil, remoteError(ErrServiceNotFound, "Remote service %s has no provider", registration.serviceId)
		}
		return registration.singleton, nil
	}
	if address == nil {
		return nil, remoteError(ErrServiceModeMismatch, "Remote service %s is keyed", registration.serviceId)
	}
	instance, ok := registration.instances.Get(address.Key)
	if !ok {
		return nil, remoteError(ErrServiceInstanceNotFound, "Remote service %s has no instance %s", registration.serviceId, address.Key)
	}
	if instance.address.Generation != address.Generation {
		return nil, remoteError(ErrServiceStaleInstance, "Remote service %s instance %s is stale", registration.serviceId, address.Key)
	}
	return instance, nil
}

func snapshotRegistration(registration *serviceRegistration) ServiceSubscriptionSnapshot {
	snapshot := ServiceSubscriptionSnapshot{ServiceId: registration.serviceId, Mode: registration.mode, Instances: []ServiceInstanceSnapshot{}}
	if registration.mode == ServiceSingleton {
		if registration.singleton != nil {
			snapshot.Instances = append(snapshot.Instances, snapshotInstance(registration.singleton))
		}
		return snapshot
	}
	// Upstream sorts snapshot instances with key.localeCompare.
	keys := registration.instances.Keys()
	localeCompareKeys(keys)
	for _, key := range keys {
		instance, _ := registration.instances.Get(key)
		snapshot.Instances = append(snapshot.Instances, snapshotInstance(instance))
	}
	return snapshot
}

func snapshotInstance(instance *providerInstance) ServiceInstanceSnapshot {
	snapshot := ServiceInstanceSnapshot{Members: []ServiceMemberSnapshot{}}
	if instance.address != nil {
		copied := *instance.address
		snapshot.Instance = &copied
	}
	for _, name := range instance.names {
		member := instance.members[name]
		if member.kind == MemberMethod {
			snapshot.Members = append(snapshot.Members, ServiceMemberSnapshot{Name: name, Kind: MemberMethod})
			continue
		}
		sequence, value := member.state.snapshot()
		snapshot.Members = append(snapshot.Members, ServiceMemberSnapshot{Name: name, Kind: MemberState, Sequence: sequence, Ops: []Op{{"r", value}}})
	}
	return snapshot
}

func stateMemberKey(address *ServiceInstanceAddress, member string) string {
	if address == nil {
		return string(mustRaw([]any{member}))
	}
	return string(mustRaw([]any{address.Key, address.Generation, member}))
}

func recordSnapshotSequences(sequences map[string]int, instances []ServiceInstanceSnapshot) {
	for _, instance := range instances {
		for _, member := range instance.Members {
			if member.Kind == MemberState {
				sequences[stateMemberKey(instance.Instance, member.Name)] = member.Sequence
			}
		}
	}
}

func updateCoveredBySnapshot(sequences map[string]int, update ServiceProviderUpdate) bool {
	switch update.Type {
	case UpdateState:
		key := stateMemberKey(update.Address, update.Member)
		sequence, ok := sequences[key]
		if !ok {
			return false
		}
		if update.Sequence <= sequence {
			return true
		}
		delete(sequences, key)
	case UpdateReset:
		clear(sequences)
		recordSnapshotSequences(sequences, update.Reset.Instances)
	case UpdateReplaced:
		clear(sequences)
		recordSnapshotSequences(sequences, []ServiceInstanceSnapshot{*update.Snapshot})
	case UpdateSpawned:
		recordSnapshotSequences(sequences, []ServiceInstanceSnapshot{*update.Snapshot})
	case UpdateUnavailable:
		clear(sequences)
	case UpdateClosed:
		encoded := string(mustRaw([]any{update.Address.Key, update.Address.Generation}))
		prefix := encoded[:len(encoded)-1] + ","
		for key := range sequences {
			if strings.HasPrefix(key, prefix) {
				delete(sequences, key)
			}
		}
	}
	return false
}

func memberShape(classified classifiedImplementation) map[string]string {
	shape := make(map[string]string, len(classified.members))
	for name, member := range classified.members {
		shape[name] = member.kind
	}
	return shape
}

func assertSingletonShape(registration *serviceRegistration, replacement map[string]string) error {
	if registration.singletonShape == nil || maps.Equal(registration.singletonShape, replacement) {
		return nil
	}
	return remoteError(ErrServiceMemberMismatch, "Remote service %s replacement must preserve its member shape", registration.serviceId)
}

var (
	contextType = reflect.TypeFor[context.Context]()
	errorType   = reflect.TypeFor[error]()
)

// classifyImplementation is upstream classifyRemoteServiceImplementation over
// Go method sets. When T is an interface, exactly its methods are members;
// otherwise the implementation's exported method set is used.
func classifyImplementation[T any](serviceId string, implementation T) (classifiedImplementation, error) {
	if dynamic, ok := any(implementation).(*FacetServiceImplementation); ok {
		return classifyFacetImplementation(serviceId, dynamic)
	}
	value := reflect.ValueOf(implementation)
	if !value.IsValid() || ((value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) && value.IsNil()) {
		return classifiedImplementation{}, fmt.Errorf("Remote service %s implementation must be an object", serviceId)
	}
	methodSet := value.Type()
	if contract := reflect.TypeFor[T](); contract.Kind() == reflect.Interface {
		methodSet = contract
	}
	classified := classifiedImplementation{implementation: implementation, members: map[string]instanceMember{}}
	for method := range methodSet.Methods() {
		goName := method.Name
		method := value.MethodByName(goName)
		name := memberName(goName)
		methodType := method.Type()
		switch {
		case methodType.NumIn() == 0 && methodType.NumOut() == 1:
			result := method.Call(nil)[0]
			source, ok := result.Interface().(stateSource)
			if !ok || (result.Kind() == reflect.Pointer && result.IsNil()) {
				return classifiedImplementation{}, fmt.Errorf("Remote service member %s.%s is not remotely exposable", serviceId, name)
			}
			classified.members[name] = instanceMember{kind: MemberState, state: source.chordState()}
		case methodType.NumIn() >= 1 && methodType.In(0) == contextType && !methodType.IsVariadic() &&
			(methodType.NumOut() == 1 || methodType.NumOut() == 2) && methodType.Out(methodType.NumOut()-1) == errorType:
			classified.members[name] = instanceMember{kind: MemberMethod, method: method}
		default:
			return classifiedImplementation{}, fmt.Errorf("Remote service member %s.%s is not remotely exposable", serviceId, name)
		}
		classified.names = append(classified.names, name)
	}
	if len(classified.members) == 0 {
		return classifiedImplementation{}, fmt.Errorf("Remote service %s has no members", serviceId)
	}
	slices.Sort(classified.names)
	return classified, nil
}

func memberName(goName string) string {
	first, size := utf8.DecodeRuneInString(goName)
	return string(unicode.ToLower(first)) + goName[size:]
}
