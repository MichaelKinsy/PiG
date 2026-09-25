package chord

import (
	"context"
	"errors"
	"fmt"
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
	report := directory.reportError
	go func() {
		var err error
		func() {
			defer recoverInto(&err)
			err = observer.handler(ctx, service)
		}()
		if err != nil && ctx.Err() == nil {
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

func (keyed *keyedBinding) observe(handler func(context.Context, *RemoteService) error) (func(), error) {
	keyed.mu.Lock()
	if keyed.closed {
		keyed.mu.Unlock()
		return nil, errors.New("Remote keyed service binding is closed")
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
		return nil, err
	}
	if keyed.bound && keyed.starting == nil {
		keyed.launchLocked(keyed.revision)
	}
	keyed.mu.Unlock()
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
	}, nil
}

func (parent *RemoteServiceBinding) releaseKeyed(keyed *keyedBinding) {
	parent.mu.Lock()
	if parent.keyed[keyed.serviceId] != keyed {
		parent.mu.Unlock()
		return
	}
	delete(parent.keyed, keyed.serviceId)
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

func (keyed *keyedBinding) rebind(ctx context.Context, bound bool) error {
	keyed.mu.Lock()
	if keyed.closed {
		keyed.mu.Unlock()
		return nil
	}
	keyed.bound = bound
	keyed.revision++
	revision := keyed.revision
	keyed.mu.Unlock()
	if err := keyed.reset(ctx, false); err != nil {
		return err
	}
	keyed.mu.Lock()
	if keyed.closed || keyed.revision != revision || keyed.bound != bound {
		keyed.mu.Unlock()
		return nil
	}
	if !bound || keyed.instances.observers.Len() == 0 {
		keyed.mu.Unlock()
		return nil
	}
	keyed.launchLocked(revision)
	starting := keyed.starting
	keyed.mu.Unlock()
	return starting.wait(context.Background())
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
	keyed.mu.Unlock()
	return nil
}

func (keyed *keyedBinding) update(ctx context.Context, update ServiceProviderUpdate, revision int) {
	var err error
	switch update.Type {
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
	defer keyed.mu.Unlock()
	if keyed.closed || keyed.revision != revision {
		// A rebind or close fenced this start/update after it began; the
		// fresh facade was never published, so discard it.
		return nil
	}
	return keyed.instances.replace(&instanceEntry{
		key:        address.Key,
		generation: address.Generation,
		facade:     facade,
		deactivate: func() {
			active.Store(false)
			facade.fence()
		},
	})
}
