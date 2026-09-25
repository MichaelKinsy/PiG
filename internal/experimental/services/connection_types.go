package services

import (
	"context"
	"encoding/json"
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
type SourceState[T comparable] struct {
	mu         sync.Mutex
	value      T
	sequence   int
	next       int
	listeners  []stateListener[T]
	pending    []statePublication[T]
	delivering bool
}

type statePublication[T any] struct {
	value    T
	context  context.Context
	delivery ReplicatedStateDelivery
}

type stateListener[T any] struct {
	id               int
	hydratedSequence int
	call             func(T, context.Context, ReplicatedStateDelivery)
}

func (state *SourceState[T]) Value() T {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.value
}

// Subscribe synchronously hydrates the listener and returns an idempotent removal function. Removal affects publications that have not started delivery, not the current listener snapshot. A listener that panics during hydration is removed before the panic propagates.
func (state *SourceState[T]) Subscribe(listener func(T, context.Context, ReplicatedStateDelivery)) func() {
	state.mu.Lock()
	id := state.next
	state.next++
	entry := stateListener[T]{id: id, call: listener, hydratedSequence: state.sequence}
	state.listeners = append(state.listeners, entry)
	value, sequence := state.value, state.sequence
	state.mu.Unlock()
	remove := func() {
		state.mu.Lock()
		defer state.mu.Unlock()
		state.listeners = slices.DeleteFunc(state.listeners, func(entry stateListener[T]) bool { return entry.id == id })
	}
	hydrated := false
	defer func() {
		if !hydrated {
			remove()
		}
	}()
	listener(value, context.Background(), ReplicatedStateDelivery{Kind: "hydrate", Sequence: sequence})
	hydrated = true
	return remove
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

func (state *SourceState[T]) deliver() {
	state.mu.Lock()
	if state.delivering {
		state.mu.Unlock()
		return
	}
	state.delivering = true
	var failures []any
	for len(state.pending) > 0 {
		publication := state.pending[0]
		state.pending[0] = statePublication[T]{}
		state.pending = state.pending[1:]
		listeners := slices.Clone(state.listeners)
		state.mu.Unlock()
		for _, listener := range listeners {
			if publication.delivery.Sequence > listener.hydratedSequence {
				if failure := deliverStateListener(listener, publication); failure != nil {
					failures = append(failures, failure)
				}
			}
		}
		state.mu.Lock()
	}
	state.pending = nil
	state.delivering = false
	state.mu.Unlock()
	if len(failures) == 1 {
		panic(failures[0])
	}
	if len(failures) > 1 {
		panic(&AggregateError{Message: "Replicated state listeners failed", Errors: failures})
	}
}

// Listener panics are collected so one subscriber cannot suppress another subscriber or poison later publications.
func deliverStateListener[T any](listener stateListener[T], publication statePublication[T]) (failure any) {
	defer func() { failure = recover() }()
	listener.call(publication.value, publication.context, publication.delivery)
	return nil
}
