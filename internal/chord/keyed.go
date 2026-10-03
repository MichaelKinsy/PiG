package chord

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
)

// instanceEntry is one live keyed instance (upstream InstanceDirectoryEntry).
type instanceEntry struct {
	key        string
	generation int
	facade     *serviceFacade
	deactivate func()
}

type instanceObserver struct {
	handler func(context.Context, *RemoteService) error
	guard   func(ctx context.Context) func() error
	tasks   map[*instanceEntry]context.CancelFunc
	closed  bool
}

// instanceDirectory owns keyed instance lifetime and the cancellable tasks
// observing those instances (upstream InstanceDirectory). Callers hold the
// keyed binding's mutex.
type instanceDirectory struct {
	entries     *orderedMap[string, *instanceEntry]
	observers   *orderedMap[*instanceObserver, bool]
	reportError func(error)
	ready       bool
	disposed    bool
	// starts holds handler invocations admitted under the keyed mutex. The
	// caller that admitted them takes and runs them after unlocking, before
	// its own operation returns.
	starts []func()
}

// takeStarts returns the handler invocations admitted by the caller's
// current critical section.
func (directory *instanceDirectory) takeStarts() []func() {
	starts := directory.starts
	directory.starts = nil
	return starts
}

// runObservationStarts invokes admitted keyed observation handlers in
// admission order on the calling goroutine.
func runObservationStarts(starts []func()) {
	for _, start := range starts {
		start()
	}
}

func (directory *instanceDirectory) replace(entry *instanceEntry) error {
	if directory.disposed {
		return errors.New("Keyed service directory is disposed")
	}
	if previous, ok := directory.entries.Get(entry.key); ok {
		if previous.generation == entry.generation {
			return errors.New("Keyed service repeated a live generation")
		}
		directory.removeEntry(previous)
	}
	directory.entries.Set(entry.key, entry)
	if directory.ready {
		directory.startAll(entry)
	}
	return nil
}

func (directory *instanceDirectory) remove(entry *instanceEntry) {
	if current, _ := directory.entries.Get(entry.key); current != entry {
		return
	}
	directory.removeEntry(entry)
}

func (directory *instanceDirectory) removeEntry(entry *instanceEntry) {
	directory.entries.Delete(entry.key)
	entry.deactivate()
	for _, observer := range directory.observers.Keys() {
		if cancel, ok := observer.tasks[entry]; ok {
			cancel()
			delete(observer.tasks, entry)
		}
	}
}

func (directory *instanceDirectory) markReady() {
	if directory.disposed || directory.ready {
		return
	}
	directory.ready = true
	for _, entry := range directory.entries.Values() {
		directory.startAll(entry)
	}
}

func (directory *instanceDirectory) reset() {
	if directory.disposed {
		return
	}
	directory.ready = false
	for _, entry := range directory.entries.Values() {
		directory.removeEntry(entry)
	}
}

func (directory *instanceDirectory) observe(observer *instanceObserver) (func(), error) {
	if directory.disposed {
		return nil, errors.New("Keyed service directory is disposed")
	}
	directory.observers.Set(observer, true)
	if directory.ready {
		for _, entry := range directory.entries.Values() {
			directory.start(observer, entry)
		}
	}
	return func() {
		if observer.closed {
			return
		}
		observer.closed = true
		for _, cancel := range observer.tasks {
			cancel()
		}
		clear(observer.tasks)
		directory.observers.Delete(observer)
	}, nil
}

func (directory *instanceDirectory) dispose() {
	if directory.disposed {
		return
	}
	directory.disposed = true
	for _, observer := range directory.observers.Keys() {
		observer.closed = true
		for _, cancel := range observer.tasks {
			cancel()
		}
		clear(observer.tasks)
	}
	directory.observers.Clear()
	for _, entry := range directory.entries.Values() {
		entry.deactivate()
	}
	directory.entries.Clear()
}

func (directory *instanceDirectory) startAll(entry *instanceEntry) {
	for _, observer := range directory.observers.Keys() {
		directory.start(observer, entry)
	}
}

// start runs the observer's handler on its own goroutine with a context cancelled when the instance is removed or the observer closes. instances.ts:#start runs the handler's synchronous prefix inline; Go cannot split one function into a prefix and a tail, and running it inline would hold the directory's locks across consumer code, so callers order on their own handler's signal.
func (directory *instanceDirectory) start(observer *instanceObserver, entry *instanceEntry) {
	if observer.closed {
		return
	}
	if _, running := observer.tasks[entry]; running {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	observer.tasks[entry] = cancel
	service := &RemoteService{facade: entry.facade, guard: observer.guard(ctx)}
	directory.starts = append(directory.starts, observationStart(ctx, func(ctx context.Context) error {
		return observer.handler(ctx, service)
	}, directory.reportError))
}

// observationStart ports packages/chord/src/services/instances.ts
// InstanceDirectory.#start: the handler runs synchronously as part of the
// delivery that admitted the instance, and its failure is reported unless the
// observation was already cancelled. Work that outlives the handler belongs
// to a task the handler owns and ties to ctx. An observation cancelled
// between admission and invocation is not started.
func observationStart(ctx context.Context, handler func(context.Context) error, report func(error)) func() {
	ctx = context.WithValue(ctx, observationReportKey{}, report)
	return func() {
		if ctx.Err() != nil {
			return
		}
		var err error
		func() {
			defer recoverInto(&err)
			err = handler(ctx)
		}()
		if err != nil && ctx.Err() == nil {
			report(err)
		}
	}
}

type observationReportKey struct{}

// ContinueObservation runs the settlement of a keyed observation handler's
// returned Promise: instances.ts#start leaves that Promise unawaited and
// reports its rejection unless the observation was cancelled. ctx must be the
// observation Context passed to the handler; it cancels nothing by itself, so
// continuation must stop when ctx is done.
func ContinueObservation(ctx context.Context, continuation func() error) {
	report, _ := ctx.Value(observationReportKey{}).(func(error))
	go func() {
		var err error
		func() {
			defer recoverInto(&err)
			err = continuation()
		}()
		if err != nil && ctx.Err() == nil && report != nil {
			report(err)
		}
	}()
}

// keyedBinding is the consumer side of one keyed service (upstream
// KeyedBinding).
type keyedBinding struct {
	parent    *RemoteServiceBinding
	serviceId string

	mu           sync.Mutex
	instances    *instanceDirectory
	subscription ServiceSubscription
	starting     *task
	closed       bool
	bound        bool
	revision     int
}

func newKeyedBinding(parent *RemoteServiceBinding, serviceId string, bound bool) *keyedBinding {
	return &keyedBinding{
		parent:    parent,
		serviceId: serviceId,
		bound:     bound,
		instances: &instanceDirectory{entries: newOrderedMap[string, *instanceEntry](), observers: newOrderedMap[*instanceObserver, bool](), reportError: parent.reportError},
	}
}

// observe adds one observer and returns the subscription start it joined, or nil when the binding is unbound (upstream KeyedBinding.observe and its #starting).
func (keyed *keyedBinding) observe(handler func(context.Context, *RemoteService) error) (func(), *task, error) {
	keyed.mu.Lock()
	if keyed.closed {
		keyed.mu.Unlock()
		return nil, nil, errors.New("Remote keyed service binding is closed")
	}
	var stopped bool
	observer := &instanceObserver{handler: handler, tasks: map[*instanceEntry]context.CancelFunc{}}
	observer.guard = func(ctx context.Context) func() error {
		return func() error {
			keyed.mu.Lock()
			stale := stopped
			keyed.mu.Unlock()
			if stale || ctx.Err() != nil {
				return remoteError(ErrServiceStaleInstance, "Remote service %s observation is closed", keyed.serviceId)
			}
			return nil
		}
	}
	stop, err := keyed.instances.observe(observer)
	if err != nil {
		keyed.mu.Unlock()
		return nil, nil, err
	}
	starts := keyed.instances.takeStarts()
	if keyed.bound && keyed.starting == nil {
		keyed.launchLocked(keyed.revision)
	}
	starting := keyed.starting
	keyed.mu.Unlock()
	runObservationStarts(starts)
	var once sync.Once
	return func() {
		once.Do(func() {
			keyed.mu.Lock()
			stopped = true
			stop()
			empty := keyed.instances.observers.Len() == 0
			keyed.mu.Unlock()
			if empty {
				keyed.parent.releaseKeyed(keyed)
			}
		})
	}, starting, nil
}

func (parent *RemoteServiceBinding) releaseKeyed(keyed *keyedBinding) {
	parent.mu.Lock()
	if parent.keyed[keyed.serviceId] != keyed {
		parent.mu.Unlock()
		return
	}
	delete(parent.keyed, keyed.serviceId)
	parent.keyedOrder = slices.DeleteFunc(parent.keyedOrder, func(id string) bool { return id == keyed.serviceId })
	parent.readinessRevision++
	parent.mu.Unlock()
	go func() {
		if err := keyed.close(context.Background()); err != nil {
			parent.reportError(err)
		}
	}()
}

func (keyed *keyedBinding) launchLocked(revision int) {
	keyed.starting = startTask(func() error {
		err := keyed.start(revision)
		if err != nil {
			keyed.mu.Lock()
			current := !keyed.closed && keyed.revision == revision && keyed.bound
			keyed.mu.Unlock()
			if current {
				keyed.parent.reportError(err)
			}
		}
		return err
	})
}

func (keyed *keyedBinding) ready() *task {
	keyed.mu.Lock()
	defer keyed.mu.Unlock()
	if keyed.starting == nil {
		return settledTask()
	}
	return keyed.starting
}

func (keyed *keyedBinding) beginRebind(ctx context.Context, bound bool) *task {
	keyed.mu.Lock()
	if keyed.closed {
		keyed.mu.Unlock()
		return settledTask()
	}
	keyed.bound = bound
	keyed.revision++
	revision := keyed.revision
	keyed.instances.reset()
	keyed.starting = nil
	subscription := keyed.subscription
	keyed.subscription = nil
	keyed.mu.Unlock()
	return startTask(func() error {
		if subscription != nil {
			if err := subscription.Close(ctx); err != nil {
				return err
			}
		}
		keyed.mu.Lock()
		if keyed.closed || keyed.revision != revision || keyed.bound != bound || !bound || keyed.instances.observers.Len() == 0 {
			keyed.mu.Unlock()
			return nil
		}
		keyed.launchLocked(revision)
		starting := keyed.starting
		keyed.mu.Unlock()
		return starting.wait(context.Background())
	})
}

func (keyed *keyedBinding) close(ctx context.Context) error {
	keyed.mu.Lock()
	if keyed.closed {
		keyed.mu.Unlock()
		return nil
	}
	keyed.closed = true
	keyed.revision++
	keyed.mu.Unlock()
	err := keyed.reset(ctx, true)
	keyed.mu.Lock()
	keyed.instances.dispose()
	keyed.mu.Unlock()
	return err
}

func (keyed *keyedBinding) reset(ctx context.Context, waitForStarting bool) error {
	keyed.mu.Lock()
	keyed.instances.reset()
	starting := keyed.starting
	keyed.starting = nil
	subscription := keyed.subscription
	keyed.subscription = nil
	keyed.mu.Unlock()
	if waitForStarting && starting != nil {
		_ = starting.wait(context.Background())
	}
	if subscription != nil {
		return subscription.Close(ctx)
	}
	return nil
}

func (keyed *keyedBinding) start(revision int) error {
	subscription, err := keyed.parent.transport.Subscribe(context.Background(), keyed.serviceId, ServiceKeyed, func(ctx context.Context, update ServiceProviderUpdate) {
		keyed.mu.Lock()
		current := keyed.revision == revision && !keyed.closed
		keyed.mu.Unlock()
		if current {
			keyed.update(ctx, update, revision)
		}
	})
	if err != nil {
		return err
	}
	keyed.mu.Lock()
	stale := keyed.closed || !keyed.bound || keyed.revision != revision
	if !stale {
		keyed.subscription = subscription
	}
	keyed.mu.Unlock()
	if stale {
		return subscription.Close(context.Background())
	}
	snapshot := subscription.Snapshot()
	if snapshot.Mode != ServiceKeyed || snapshot.ServiceId != keyed.serviceId {
		return fmt.Errorf("Remote service %s returned the wrong keyed snapshot", keyed.serviceId)
	}
	for _, instance := range snapshot.Instances {
		if err := keyed.spawn(deliveryContext(), instance, revision); err != nil {
			return err
		}
	}
	if err := subscription.Activate(); err != nil {
		return err
	}
	keyed.mu.Lock()
	keyed.instances.markReady()
	starts := keyed.instances.takeStarts()
	keyed.mu.Unlock()
	runObservationStarts(starts)
	return nil
}

func (keyed *keyedBinding) update(ctx context.Context, update ServiceProviderUpdate, revision int) {
	var err error
	switch update.Type {
	case UpdateReset:
		err = keyed.rebaseline(ctx, *update.Reset, revision)
	case UpdateUnavailable, UpdateReplaced:
		err = errors.New("Keyed service received a singleton lifecycle update")
	case UpdateSpawned:
		err = keyed.spawn(ctx, *update.Snapshot, revision)
	case UpdateClosed:
		keyed.mu.Lock()
		if instance, ok := keyed.instances.entries.Get(update.Address.Key); ok && instance.generation == update.Address.Generation {
			keyed.instances.remove(instance)
		}
		keyed.mu.Unlock()
	case UpdateState:
		if update.Address == nil {
			err = errors.New("Keyed state update has no instance address")
			break
		}
		keyed.mu.Lock()
		instance, ok := keyed.instances.entries.Get(update.Address.Key)
		keyed.mu.Unlock()
		if !ok || instance.generation != update.Address.Generation {
			return
		}
		err = instance.facade.update(ctx, update.Member, update.Sequence, update.Ops, 0)
	}
	if err != nil {
		keyed.parent.reportError(err)
	}
}

// rebaseline rebaselines the observed instances from a full snapshot: instances whose key is absent or whose generation differs are removed, live generations keep their facade and reinstall their state, and new keys are spawned (upstream consumer.ts "reset").
func (keyed *keyedBinding) rebaseline(ctx context.Context, snapshot ServiceSubscriptionSnapshot, revision int) error {
	if err := validateResetSnapshot(snapshot, keyed.serviceId, ServiceKeyed); err != nil {
		return err
	}
	snapshots := make(map[string]ServiceInstanceSnapshot, len(snapshot.Instances))
	for _, instance := range snapshot.Instances {
		snapshots[instance.Instance.Key] = instance
	}
	keyed.mu.Lock()
	for _, key := range keyed.instances.entries.Keys() {
		instance, _ := keyed.instances.entries.Get(key)
		if current, ok := snapshots[key]; !ok || current.Instance.Generation != instance.generation {
			keyed.instances.remove(instance)
		}
	}
	keyed.mu.Unlock()
	for _, instance := range snapshot.Instances {
		keyed.mu.Lock()
		existing, ok := keyed.instances.entries.Get(instance.Instance.Key)
		keyed.mu.Unlock()
		if !ok {
			if err := keyed.spawn(ctx, instance, revision); err != nil {
				return err
			}
			continue
		}
		if err := existing.facade.install(ctx, instance, 0); err != nil {
			return err
		}
	}
	return nil
}

func (keyed *keyedBinding) spawn(ctx context.Context, snapshot ServiceInstanceSnapshot, revision int) error {
	if snapshot.Instance == nil {
		return errors.New("Keyed service instance snapshot has no address")
	}
	address := *snapshot.Instance
	var active atomic.Bool
	active.Store(true)
	facade := newServiceFacade(keyed.serviceId, &address, keyed.parent.transport, func() bool {
		if !active.Load() {
			return false
		}
		keyed.mu.Lock()
		defer keyed.mu.Unlock()
		return !keyed.closed
	}, keyed.parent.assertHandleAccess, keyed.parent.reportError)
	if err := facade.install(ctx, snapshot, 0); err != nil {
		return err
	}
	keyed.mu.Lock()
	if keyed.closed || keyed.revision != revision {
		keyed.mu.Unlock()
		// A rebind or close fenced this start/update after it began; the
		// fresh facade was never published, so discard it.
		return nil
	}
	err := keyed.instances.replace(&instanceEntry{
		key:        address.Key,
		generation: address.Generation,
		facade:     facade,
		deactivate: func() {
			active.Store(false)
			facade.fence()
		},
	})
	starts := keyed.instances.takeStarts()
	keyed.mu.Unlock()
	runObservationStarts(starts)
	return err
}
