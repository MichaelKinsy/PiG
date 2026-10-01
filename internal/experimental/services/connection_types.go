package services

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
)

// Service identifies a Chord service. Local services cannot cross a remote binding.
type Service struct {
	ID    string
	Local bool
}

// ServiceCatalogueEntry describes an available singleton or keyed service.
type ServiceCatalogueEntry struct {
	ServiceID string `json:"serviceId"`
	Mode      string `json:"mode"`
}

// SessionTarget identifies one attachment generation, not just a Session.
type SessionTarget struct {
	ServerID     string `json:"serverId"`
	SessionID    string `json:"sessionId"`
	AttachmentID string `json:"attachmentId"`
}

// ServiceTarget routes either to the server or to an exact Session attachment.
type ServiceTarget struct {
	ServerID     string  `json:"serverId"`
	SessionID    *string `json:"sessionId,omitempty"`
	AttachmentID *string `json:"attachmentId,omitempty"`
}

// ServiceClient is the source's consumer-owned client boundary. Getters return snapshots. Event listeners run in event order and observe already-committed client state; removing a listener prevents new invocations. ServiceCatalogue starts a client-owned request and calls its completion exactly once. The client owns cancellation and shutdown draining, including requests that outlive a source.
type ServiceClient interface {
	ServerID() string
	ConnectionState() string
	Attachment() *SessionTarget
	ServiceCatalogue(context.Context, ServiceTarget, func([]ServiceCatalogueEntry, error))
	OnConnectionStateChange(func(string, error)) func()
	OnAttachmentChange(func(*SessionTarget)) func()
}

// RemoteServiceBinding is supplied by the Chord host. Rebind synchronously starts invalidation and calls its completion exactly once after hydration/release. Ready synchronously registers a waiter for current hydration/release and calls its completion exactly once, inline if already settled. Both methods return promptly; invocation order preserves the synchronous prefix of the upstream Promise API. Cancelling Ready releases only its waiter, and completion follows waiter cleanup. Implementations honor caller cancellation and make Dispose idempotent. Use and Observe retain the host's typed service handles and access checks.
type RemoteServiceBinding interface {
	Use(Service) (any, error)
	Observe(Service, func(context.Context, any) error) (func(), error)
	Ready(context.Context, func(error))
	Rebind(context.Context, bool, func(error))
	Dispose(context.Context) error
}

// ServiceBindingOptions configures one source-owned remote binding.
type ServiceBindingOptions struct {
	Services     []Service
	AssertAccess func() error
	OnError      func(error)
}

// RemoteServiceBindingOptions supplies the host with lazy routing and an initially unbound service set. GetTarget returns nil while detached.
type RemoteServiceBindingOptions struct {
	ServiceBindingOptions
	GetTarget func() *ServiceTarget
	Bound     bool
}

// ServiceSourceOptions supplies the external Chord binding implementation and asynchronous error sink. Sources own bindings, not the client or its transport. NewBinding must return an independent binding for every call.
type ServiceSourceOptions struct {
	OnError    func(error)
	NewBinding func(RemoteServiceBindingOptions) RemoteServiceBinding
}

// ServerConnectionState is discriminated by Status: connecting has Attempt; connected has Since; disconnected has Since, Reason, and nullable RetryAt.
type ServerConnectionState struct {
	Status  string
	Attempt int
	Since   string
	Reason  string
	RetryAt *string
}

func (state ServerConnectionState) MarshalJSON() ([]byte, error) {
	switch state.Status {
	case "connecting":
		return json.Marshal(struct {
			Status  string `json:"status"`
			Attempt int    `json:"attempt"`
		}{state.Status, state.Attempt})
	case "connected":
		return json.Marshal(struct {
			Status string `json:"status"`
			Since  string `json:"since"`
		}{state.Status, state.Since})
	default:
		return json.Marshal(struct {
			Status  string  `json:"status"`
			Since   string  `json:"since"`
			Reason  string  `json:"reason"`
			RetryAt *string `json:"retryAt"`
		}{state.Status, state.Since, state.Reason, state.RetryAt})
	}
}

// SessionAttachmentState has a SessionID only while attaching, attached, or degraded.
type SessionAttachmentState struct {
	Status    string `json:"status"`
	SessionID string `json:"sessionId"`
}

func (state SessionAttachmentState) MarshalJSON() ([]byte, error) {
	if state.Status == "detached" {
		return json.Marshal(struct {
			Status string `json:"status"`
		}{state.Status})
	}
	type plain SessionAttachmentState
	return json.Marshal(plain(state))
}

// ReplicatedStateDelivery distinguishes initial hydration from ordered updates.
type ReplicatedStateDelivery struct {
	Kind     string
	Sequence int
}

// SourceState is a read-only lifecycle snapshot with ordered, context-bearing subscriptions. Equal replacements do not publish. Subscribers must not mutate pointer fields. Callbacks run outside source locks and should return promptly.
//
// Each subscription has its own queue of at most stateSubscriberQueueLimit deliveries and runs one callback at a time, so a
// callback that publishes sees its own later deliveries only after it returns. Unsubscribing drops the queued deliveries, even
// those of a publication that already snapshotted the subscription. A callback panic never reaches the publisher: it is
// reported and later deliveries continue. Go callbacks cannot return a Promise, so upstream's wait for one has no counterpart.
type SourceState[T comparable] struct {
	mu          sync.Mutex
	value       T
	sequence    int
	subscribers []*stateSubscriber[T]
	pending     []statePublication[T]
	delivering  bool
	// reportError receives callback failures; nil reports through throwAsync.
	reportError func(error)
}

type statePublication[T any] struct {
	value    T
	context  context.Context
	delivery ReplicatedStateDelivery
}

// stateSubscriberQueueLimit is the pending delivery bound of chord state.ts StateSubscriber.push.
// upstream: packages/chord/src/services/state.ts:StateSubscriber
const stateSubscriberQueueLimit = 100

// stateSubscriber is chord state.ts StateSubscriber; its fields are guarded by the state's mutex.
type stateSubscriber[T any] struct {
	call             func(T, context.Context, ReplicatedStateDelivery)
	hydratedSequence int
	queue            []statePublication[T]
	running          bool
	started          bool
	closed           bool
}

// throwAsync is chord reportErrorAsync: the failure surfaces as an uncaught error outside the call that published it.
var throwAsync = func(err error) { go func() { panic(err) }() }

func (state *SourceState[T]) Value() T {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.value
}

// Subscribe synchronously hydrates the listener and returns an idempotent removal function. Removal drops queued deliveries
// but does not stop a callback that is running. A callback that panics is reported and stays subscribed.
func (state *SourceState[T]) Subscribe(listener func(T, context.Context, ReplicatedStateDelivery)) func() {
	state.mu.Lock()
	subscriber := &stateSubscriber[T]{call: listener, hydratedSequence: state.sequence}
	state.subscribers = append(state.subscribers, subscriber)
	subscriber.push(statePublication[T]{value: state.value, context: context.Background(), delivery: ReplicatedStateDelivery{Kind: "hydrate", Sequence: state.sequence}})
	// Claim the subscriber before releasing the mutex, as chord subscribe drains it without yielding: a publication on
	// another goroutine then queues behind the hydration instead of running it there after Subscribe returns.
	subscriber.running = true
	state.run(subscriber)
	return func() { state.unsubscribe(subscriber) }
}

func (state *SourceState[T]) unsubscribe(subscriber *stateSubscriber[T]) {
	state.mu.Lock()
	defer state.mu.Unlock()
	subscriber.closed = true
	subscriber.queue = nil
	state.subscribers = slices.DeleteFunc(state.subscribers, func(candidate *stateSubscriber[T]) bool { return candidate == subscriber })
}

// push is called with the state's mutex held.
func (subscriber *stateSubscriber[T]) push(publication statePublication[T]) {
	if subscriber.closed {
		return
	}
	if len(subscriber.queue) == stateSubscriberQueueLimit {
		// A subscriber that has not started keeps its hydration, which is the first queued delivery.
		var hydration []statePublication[T]
		if !subscriber.started {
			hydration = []statePublication[T]{subscriber.queue[0]}
		}
		subscriber.queue = hydration
	}
	subscriber.queue = append(subscriber.queue, publication)
}

// drain runs the subscriber's queued deliveries unless one of its callbacks is already running.
func (state *SourceState[T]) drain(subscriber *stateSubscriber[T]) {
	state.mu.Lock()
	if subscriber.running || subscriber.closed {
		state.mu.Unlock()
		return
	}
	subscriber.running = true
	state.run(subscriber)
}

// run delivers the queued deliveries of a subscriber that the caller marked running. It is called with the state's mutex
// held and releases it.
func (state *SourceState[T]) run(subscriber *stateSubscriber[T]) {
	for len(subscriber.queue) > 0 {
		publication := subscriber.queue[0]
		subscriber.queue[0] = statePublication[T]{}
		subscriber.queue = subscriber.queue[1:]
		subscriber.started = true
		state.mu.Unlock()
		if failure := deliverStateListener(subscriber, publication); failure != nil {
			state.report(failure)
		}
		state.mu.Lock()
	}
	subscriber.running = false
	state.mu.Unlock()
}

// report never propagates a reporter failure to the delivering call; it surfaces through throwAsync.
func (state *SourceState[T]) report(failure any) {
	err := toStateError(failure)
	reporter := state.reportError
	if reporter == nil {
		throwAsync(err)
		return
	}
	if reportFailure := capturePublicationFailure(func() { reporter(err) }); reportFailure != nil {
		throwAsync(toStateError(reportFailure))
	}
}

func toStateError(failure any) error {
	if err, ok := failure.(error); ok {
		return err
	}
	return fmt.Errorf("%v", failure)
}

// replace commits before notifications so observers can query the source without holding its mutex.
func (state *SourceState[T]) replace(ctx context.Context, value T) func() {
	state.mu.Lock()
	if state.value == value {
		state.mu.Unlock()
		return state.deliver
	}
	state.value = value
	state.sequence++
	state.pending = append(state.pending, statePublication[T]{value: value, context: ctx, delivery: ReplicatedStateDelivery{Kind: "update", Sequence: state.sequence}})
	state.mu.Unlock()
	return state.deliver
}

// deliver publishes queued revisions in commit order to the subscribers of each publication's snapshot.
func (state *SourceState[T]) deliver() {
	state.mu.Lock()
	if state.delivering {
		state.mu.Unlock()
		return
	}
	state.delivering = true
	for len(state.pending) > 0 {
		publication := state.pending[0]
		state.pending[0] = statePublication[T]{}
		state.pending = state.pending[1:]
		subscribers := slices.Clone(state.subscribers)
		state.mu.Unlock()
		for _, subscriber := range subscribers {
			if publication.delivery.Sequence <= subscriber.hydratedSequence {
				continue
			}
			state.mu.Lock()
			subscriber.push(publication)
			state.mu.Unlock()
			state.drain(subscriber)
		}
		state.mu.Lock()
	}
	state.pending = nil
	state.delivering = false
	state.mu.Unlock()
}

func deliverStateListener[T any](subscriber *stateSubscriber[T], publication statePublication[T]) (failure any) {
	return capturePublicationFailure(func() { subscriber.call(publication.value, publication.context, publication.delivery) })
}

// capturePublicationFailure returns the panic value of call, if any, so one callback cannot suppress another callback or
// poison later publications; the caller reports it.
func capturePublicationFailure(call func()) (failure any) {
	defer func() { failure = recover() }()
	call()
	return nil
}
