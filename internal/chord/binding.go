package chord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

// RemoteServiceBindingOptions configures CreateRemoteServiceBinding.
type RemoteServiceBindingOptions struct {
	// Services is the allowlist of service IDs this binding may use.
	Services []string
	// Transport carries calls and subscriptions.
	Transport RemoteServiceTransport
	// Unbound starts the binding unbound (upstream bound: false).
	Unbound bool
	// OnError receives asynchronous subscription, replica and observer errors.
	OnError func(error)
	// AssertAccess guards every handle access (for example facet revocation).
	AssertAccess func() error
}

// RemoteServiceBinding is the consumer side of remote services: stable
// service facades whose state members are hydrated replicas fed by the
// provider operation stream (upstream RemoteServiceBindingImpl).
type RemoteServiceBinding struct {
	transport    RemoteServiceTransport
	allowlist    map[string]bool
	reportError  func(error)
	assertAccess func() error

	mu                sync.Mutex
	modes             map[string]ServiceMode
	singletons        map[string]*singletonBinding
	keyed             map[string]*keyedBinding
	bound             bool
	readinessRevision int
	transition        *task
	disposed          bool
}

// task is a settled-once asynchronous operation (a Go Promise<void>).
type task struct {
	done chan struct{}
	err  error
}

func startTask(run func() error) *task {
	started := &task{done: make(chan struct{})}
	go func() {
		defer close(started.done)
		started.err = run()
	}()
	return started
}

func settledTask() *task {
	settled := &task{done: make(chan struct{})}
	close(settled.done)
	return settled
}

func (waiting *task) wait(ctx context.Context) error {
	select {
	case <-waiting.done:
		return waiting.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type singletonBinding struct {
	facade       *serviceFacade
	subscription ServiceSubscription
	starting     *task
	active       bool
	revision     int
}

// CreateRemoteServiceBinding creates a binding. Duplicate allowlist IDs are
// rejected.
func CreateRemoteServiceBinding(options RemoteServiceBindingOptions) (*RemoteServiceBinding, error) {
	binding := &RemoteServiceBinding{
		transport:    options.Transport,
		allowlist:    map[string]bool{},
		reportError:  options.OnError,
		assertAccess: options.AssertAccess,
		modes:        map[string]ServiceMode{},
		singletons:   map[string]*singletonBinding{},
		keyed:        map[string]*keyedBinding{},
		bound:        !options.Unbound,
		transition:   settledTask(),
	}
	for _, id := range options.Services {
		if binding.allowlist[id] {
			return nil, errors.New("Remote service binding has duplicate service IDs")
		}
		binding.allowlist[id] = true
	}
	if binding.reportError == nil {
		binding.reportError = func(error) {}
	}
	if binding.assertAccess == nil {
		binding.assertAccess = func() error { return nil }
	}
	return binding, nil
}

func (binding *RemoteServiceBinding) assertHandleAccess() error {
	binding.mu.Lock()
	disposed := binding.disposed
	binding.mu.Unlock()
	if disposed {
		return errors.New("Remote service binding is disposed")
	}
	return binding.assertAccess()
}

func (binding *RemoteServiceBinding) assertAvailableLocked(serviceId string, local bool, mode ServiceMode) error {
	if local {
		return remoteError(ErrServiceNotAllowed, "Service %s is process-local", serviceId)
	}
	if binding.disposed {
		return errors.New("Remote service binding is disposed")
	}
	if !binding.allowlist[serviceId] {
		return remoteError(ErrServiceNotAllowed, "Remote service %s is not allowlisted", serviceId)
	}
	if existing, ok := binding.modes[serviceId]; ok && existing != mode {
		return remoteError(ErrServiceModeMismatch, "Remote service %s is already used as %s", serviceId, existing)
	}
	binding.modes[serviceId] = mode
	return nil
}

// UseRemote returns the stable singleton facade for def and, while bound,
// starts its subscription. Repeated calls return the same facade.
func UseRemote[T any](binding *RemoteServiceBinding, def pico3.ServiceDefinition[T]) (*RemoteService, error) {
	if def.Local() {
		return nil, remoteError(ErrServiceNotAllowed, "Service %s is process-local", def.Id())
	}
	return binding.Use(def.Id())
}

// Use is the untyped form of UseRemote for a transport-visible service ID.
func (binding *RemoteServiceBinding) Use(serviceId string) (*RemoteService, error) {
	binding.mu.Lock()
	defer binding.mu.Unlock()
	if err := binding.assertAvailableLocked(serviceId, false, ServiceSingleton); err != nil {
		return nil, err
	}
	if existing, ok := binding.singletons[serviceId]; ok {
		return &RemoteService{facade: existing.facade}, nil
	}
	single := &singletonBinding{active: true}
	single.facade = newServiceFacade(serviceId, nil, binding.transport, func() bool {
		binding.mu.Lock()
		defer binding.mu.Unlock()
		return single.active && !binding.disposed && binding.bound
	}, binding.assertHandleAccess, binding.reportError)
	binding.singletons[serviceId] = single
	binding.readinessRevision++
	if binding.bound {
		binding.launchSingletonLocked(serviceId, single, single.revision)
	}
	return &RemoteService{facade: single.facade}, nil
}

func (binding *RemoteServiceBinding) launchSingletonLocked(serviceId string, single *singletonBinding, revision int) {
	single.starting = startTask(func() error {
		err := binding.startSingleton(serviceId, single, revision)
		if err != nil {
			binding.mu.Lock()
			current := single.active && single.revision == revision && !binding.disposed && binding.bound
			binding.mu.Unlock()
			if current {
				binding.reportError(err)
			}
		}
		return err
	})
}

func (binding *RemoteServiceBinding) startSingleton(serviceId string, single *singletonBinding, revision int) error {
	// The facade epoch is advanced under binding.mu by every rebind/dispose
	// fence. Capturing it with the revision makes the later snapshot
	// installation and updates of this start a no-op once a newer transition
	// has cleared the facade, without holding binding.mu across callbacks
	// (upstream performs the post-await fence and install synchronously).
	binding.mu.Lock()
	epoch := single.facade.epoch.Load()
	binding.mu.Unlock()
	current := func() bool {
		binding.mu.Lock()
		defer binding.mu.Unlock()
		return single.active && single.revision == revision
	}
	subscription, err := binding.transport.Subscribe(context.Background(), serviceId, ServiceSingleton, func(ctx context.Context, update ServiceProviderUpdate) {
		if !current() {
			return
		}
		var err error
		switch {
		case update.Type == UpdateUnavailable:
			single.facade.clear()
		case update.Type == UpdateReplaced:
			if update.Snapshot.Instance != nil {
				err = errors.New("Singleton replacement has an instance address")
			} else {
				err = single.facade.install(ctx, *update.Snapshot, epoch)
			}
		case update.Type == UpdateState && update.Address == nil:
			err = single.facade.update(ctx, update.Member, update.Sequence, update.Ops, epoch)
		}
		if err != nil {
			binding.reportError(err)
		}
	})
	if err != nil {
		return err
	}
	binding.mu.Lock()
	stale := !single.active || binding.disposed || !binding.bound || single.revision != revision
	if !stale {
		single.subscription = subscription
	}
	binding.mu.Unlock()
	if stale {
		return subscription.Close(context.Background())
	}
	snapshot := subscription.Snapshot()
	if snapshot.Mode != ServiceSingleton || snapshot.ServiceId != serviceId || len(snapshot.Instances) != 1 {
		return fmt.Errorf("Remote service %s returned an invalid singleton snapshot", serviceId)
	}
	if err := single.facade.install(deliveryContext(), snapshot.Instances[0], epoch); err != nil {
		return err
	}
	return subscription.Activate()
}

// ObserveRemote observes each live instance of keyed service def. The handler
// runs on its own goroutine with a context cancelled when the instance
// closes, is replaced by a newer generation, or the observation stops. The
// returned stop function is idempotent.
func ObserveRemote[T any](binding *RemoteServiceBinding, def pico3.ServiceDefinition[T], handler func(context.Context, *RemoteService) error) (func(), error) {
	if def.Local() {
		return nil, remoteError(ErrServiceNotAllowed, "Service %s is process-local", def.Id())
	}
	return binding.Observe(def.Id(), handler)
}

// Observe is the untyped form of ObserveRemote for a transport-visible
// service ID.
func (binding *RemoteServiceBinding) Observe(serviceId string, handler func(context.Context, *RemoteService) error) (func(), error) {
	binding.mu.Lock()
	if err := binding.assertAvailableLocked(serviceId, false, ServiceKeyed); err != nil {
		binding.mu.Unlock()
		return nil, err
	}
	keyed, ok := binding.keyed[serviceId]
	if !ok {
		keyed = newKeyedBinding(binding, serviceId, binding.bound)
		binding.keyed[serviceId] = keyed
		binding.readinessRevision++
	}
	binding.mu.Unlock()
	return keyed.observe(handler)
}

// Ready waits until every currently acquired service has installed its
// initial snapshot, repeating while new services are acquired concurrently.
func (binding *RemoteServiceBinding) Ready(ctx context.Context) error {
	for {
		binding.mu.Lock()
		if binding.disposed {
			binding.mu.Unlock()
			return errors.New("Remote service binding is disposed")
		}
		revision := binding.readinessRevision
		waits := []*task{binding.transition}
		for _, single := range binding.singletons {
			if single.starting != nil {
				waits = append(waits, single.starting)
			}
		}
		keyed := make([]*keyedBinding, 0, len(binding.keyed))
		for _, entry := range binding.keyed {
			keyed = append(keyed, entry)
		}
		binding.mu.Unlock()
		for _, entry := range keyed {
			waits = append(waits, entry.ready())
		}
		for _, waiting := range waits {
			if err := waiting.wait(ctx); err != nil {
				return err
			}
		}
		binding.mu.Lock()
		disposed, same := binding.disposed, revision == binding.readinessRevision
		binding.mu.Unlock()
		if disposed {
			return errors.New("Remote service binding is disposed")
		}
		if same {
			return nil
		}
	}
}

// Rebind closes every subscription, clears replicas, and when bound
// resubscribes and rehydrates. Each call supersedes earlier transitions.
func (binding *RemoteServiceBinding) Rebind(ctx context.Context, bound bool) error {
	binding.mu.Lock()
	if binding.disposed {
		binding.mu.Unlock()
		return errors.New("Remote service binding is disposed")
	}
	binding.bound = bound
	binding.readinessRevision++
	var transitions []*task
	for serviceId, single := range binding.singletons {
		single.revision++
		single.facade.fence()
		subscription := single.subscription
		single.subscription = nil
		revision := single.revision
		serviceId, single := serviceId, single
		single.starting = startTask(func() error {
			if subscription != nil {
				if err := subscription.Close(ctx); err != nil {
					return err
				}
			}
			if bound {
				return binding.startSingleton(serviceId, single, revision)
			}
			return nil
		})
		transitions = append(transitions, single.starting)
	}
	for _, keyed := range binding.keyed {
		transitions = append(transitions, startTask(func() error { return keyed.rebind(ctx, bound) }))
	}
	completion := startTask(func() error {
		var errs []error
		for _, transition := range transitions {
			if err := transition.wait(context.Background()); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("Failed to rebind services: %w", errors.Join(errs...))
		}
		return nil
	})
	binding.transition = completion
	binding.mu.Unlock()
	return completion.wait(ctx)
}

// Dispose closes every subscription exactly once and waits for pending
// starts. Later calls are no-ops.
func (binding *RemoteServiceBinding) Dispose(ctx context.Context) error {
	binding.mu.Lock()
	if binding.disposed {
		binding.mu.Unlock()
		return nil
	}
	binding.disposed = true
	var closes []*task
	for _, single := range binding.singletons {
		single.active = false
		single.facade.fence()
		if single.starting != nil {
			starting := single.starting
			closes = append(closes, startTask(func() error { _ = starting.wait(context.Background()); return nil }))
		}
		if subscription := single.subscription; subscription != nil {
			single.subscription = nil
			closes = append(closes, startTask(func() error { return subscription.Close(ctx) }))
		}
	}
	for _, keyed := range binding.keyed {
		closes = append(closes, startTask(func() error { return keyed.close(ctx) }))
	}
	binding.singletons = map[string]*singletonBinding{}
	binding.keyed = map[string]*keyedBinding{}
	binding.mu.Unlock()
	var errs []error
	for _, closing := range closes {
		if err := closing.wait(context.Background()); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("Failed to dispose services: %w", errors.Join(errs...))
	}
	return nil
}

// RemoteService is a consumer facade over one remote service instance.
type RemoteService struct {
	facade *serviceFacade
	guard  func() error
}

// Address is the keyed instance address, or nil for a singleton.
func (service *RemoteService) Address() *ServiceInstanceAddress {
	if service.facade.address == nil {
		return nil
	}
	copied := *service.facade.address
	return &copied
}

func (service *RemoteService) access() error {
	if err := service.facade.assertAccess(); err != nil {
		return err
	}
	if service.guard != nil {
		return service.guard()
	}
	return nil
}

// Call invokes method member with JSON-encodable arguments and returns the
// raw JSON result (nil for void).
func (service *RemoteService) Call(ctx context.Context, member string, args ...any) (json.RawMessage, error) {
	if err := service.access(); err != nil {
		return nil, err
	}
	return service.facade.call(ctx, member, args)
}

// State returns the replica for state member. It is unhydrated until the
// subscription snapshot installs.
func (service *RemoteService) State(member string) (*ReplicatedStateReplica, error) {
	if err := service.access(); err != nil {
		return nil, err
	}
	core, err := service.facade.state(member)
	if err != nil {
		return nil, err
	}
	return &ReplicatedStateReplica{core: core, access: service.access}, nil
}

// CallResult invokes member and decodes its JSON result into R.
func CallResult[R any](ctx context.Context, service *RemoteService, member string, args ...any) (R, error) {
	var result R
	raw, err := service.Call(ctx, member, args...)
	if err != nil {
		return result, err
	}
	if raw == nil {
		return result, remoteError(ErrServiceInvalidValue, "Remote service method %s.%s returned no value", service.facade.serviceId, member)
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

type memberSlot struct {
	kind     string
	expected string
	replica  *replicaCore
}

type serviceFacade struct {
	serviceId    string
	address      *ServiceInstanceAddress
	transport    RemoteServiceTransport
	isActive     func() bool
	assertAccess func() error
	reportError  func(error)

	epoch        atomic.Int64
	mu           sync.Mutex
	slots        map[string]*memberSlot
	descriptions map[string]string
}

func newServiceFacade(serviceId string, address *ServiceInstanceAddress, transport RemoteServiceTransport, isActive func() bool, assertAccess func() error, reportError func(error)) *serviceFacade {
	return &serviceFacade{
		serviceId: serviceId, address: address, transport: transport, isActive: isActive,
		assertAccess: assertAccess, reportError: reportError,
		slots: map[string]*memberSlot{}, descriptions: map[string]string{},
	}
}

func (facade *serviceFacade) slotLocked(member string) *memberSlot {
	slot, ok := facade.slots[member]
	if !ok {
		slot = &memberSlot{replica: newReplica(facade.reportError), kind: facade.descriptions[member]}
		facade.slots[member] = slot
	}
	return slot
}

func (facade *serviceFacade) expectLocked(member, kind string) error {
	slot := facade.slotLocked(member)
	if slot.expected != "" && slot.expected != kind {
		return remoteError(ErrServiceMemberMismatch, "Remote service member %s.%s was used as two different kinds", facade.serviceId, member)
	}
	slot.expected = kind
	if slot.kind != "" && slot.kind != kind {
		return remoteError(ErrServiceMemberMismatch, "Remote service member %s.%s is %s, not %s", facade.serviceId, member, slot.kind, kind)
	}
	return nil
}

func (facade *serviceFacade) setDescriptionLocked(member, kind string) error {
	slot := facade.slotLocked(member)
	if slot.kind != "" && slot.kind != kind {
		return fmt.Errorf("Remote service member %s.%s changed kind", facade.serviceId, member)
	}
	slot.kind = kind
	if slot.expected != "" && slot.expected != kind {
		return remoteError(ErrServiceMemberMismatch, "Remote service member %s.%s is %s, not %s", facade.serviceId, member, kind, slot.expected)
	}
	return nil
}

func (facade *serviceFacade) call(ctx context.Context, member string, args []any) (json.RawMessage, error) {
	facade.mu.Lock()
	err := facade.expectLocked(member, MemberMethod)
	facade.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !facade.isActive() {
		return nil, remoteError(ErrServiceStaleInstance, "Remote service %s binding is closed", facade.serviceId)
	}
	encoded := make([]json.RawMessage, len(args))
	for index, arg := range args {
		raw, err := json.Marshal(arg)
		if err != nil {
			return nil, remoteError(ErrServiceInvalidValue, "Remote service method %s.%s argument %d is not JSON: %v", facade.serviceId, member, index, err)
		}
		encoded[index] = raw
	}
	call := ServiceCall{ServiceId: facade.serviceId, Member: member, Args: encoded}
	if facade.address != nil {
		copied := *facade.address
		call.Instance = &copied
	}
	return facade.transport.Invoke(ctx, call)
}

func (facade *serviceFacade) state(member string) (*replicaCore, error) {
	facade.mu.Lock()
	defer facade.mu.Unlock()
	if err := facade.expectLocked(member, MemberState); err != nil {
		return nil, err
	}
	return facade.slots[member].replica, nil
}

func (facade *serviceFacade) valid(epoch int64) func() bool {
	return func() bool { return facade.epoch.Load() == epoch }
}

// fence discards every delivery started before it, then clears replicas.
func (facade *serviceFacade) fence() {
	facade.epoch.Add(1)
	facade.clear()
}

func (facade *serviceFacade) install(ctx context.Context, snapshot ServiceInstanceSnapshot, epoch int64) error {
	if !sameAddress(snapshot.Instance, facade.address) {
		return errors.New("Remote service snapshot has the wrong address")
	}
	members := map[string]ServiceMemberSnapshot{}
	for _, member := range snapshot.Members {
		if _, duplicate := members[member.Name]; member.Name == "" || duplicate {
			return errors.New("Remote service has invalid member descriptions")
		}
		members[member.Name] = member
	}
	facade.mu.Lock()
	for name := range facade.slots {
		if _, ok := members[name]; !ok {
			facade.mu.Unlock()
			return remoteError(ErrServiceMemberNotFound, "Unknown remote service member %s.%s", facade.serviceId, name)
		}
	}
	clear(facade.descriptions)
	for name, member := range members {
		facade.descriptions[name] = member.Kind
	}
	type hydration struct {
		replica  *replicaCore
		sequence int
		ops      []pico3.Op
	}
	var hydrations []hydration
	for _, member := range snapshot.Members {
		slot, exists := facade.slots[member.Name]
		if member.Kind == MemberState {
			if !exists {
				slot = facade.slotLocked(member.Name)
			}
			if err := facade.setDescriptionLocked(member.Name, MemberState); err != nil {
				facade.mu.Unlock()
				return err
			}
			hydrations = append(hydrations, hydration{slot.replica, member.Sequence, member.Ops})
		} else if exists {
			if err := facade.setDescriptionLocked(member.Name, member.Kind); err != nil {
				facade.mu.Unlock()
				return err
			}
		}
	}
	facade.mu.Unlock()
	for _, entry := range hydrations {
		if err := entry.replica.hydrate(ctx, entry.sequence, entry.ops, facade.valid(epoch)); err != nil {
			return err
		}
	}
	return nil
}

func (facade *serviceFacade) update(ctx context.Context, member string, sequence int, ops []pico3.Op, epoch int64) error {
	facade.mu.Lock()
	if facade.descriptions[member] != MemberState {
		facade.mu.Unlock()
		return fmt.Errorf("Remote service update targets non-state member %s.%s", facade.serviceId, member)
	}
	slot := facade.slotLocked(member)
	if err := facade.setDescriptionLocked(member, MemberState); err != nil {
		facade.mu.Unlock()
		return err
	}
	facade.mu.Unlock()
	return slot.replica.update(ctx, sequence, ops, facade.valid(epoch))
}

func (facade *serviceFacade) clear() {
	facade.mu.Lock()
	slots := make([]*memberSlot, 0, len(facade.slots))
	for _, slot := range facade.slots {
		slots = append(slots, slot)
	}
	facade.mu.Unlock()
	for _, slot := range slots {
		slot.replica.clear()
	}
}

func sameAddress(left, right *ServiceInstanceAddress) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
