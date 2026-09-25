package pico3

import (
	"context"
	"strings"
)

// PublishedConversationView is a conversation snapshot with commit.events.
type PublishedConversationView = JsonObject

// ReplicatedStateDelivery distinguishes initial hydration from publications.
type ReplicatedStateDelivery struct {
	Kind     string
	Sequence int
}

// ReplicatedState is the read side of the state supplied by a Chord host.
// Value and subscription snapshots must be detached from mutable storage.
type ReplicatedState = ReplicatedStateOf[PublishedConversationView]

// ReplicatedStateOf is the typed read side of a caller-supplied Chord state.
type ReplicatedStateOf[T any] interface {
	Value() T
	Subscribe(func(T, context.Context, ReplicatedStateDelivery)) (func(), error)
}

// MutableReplicatedState is the bridge's consumer-owned publication contract.
// Change applies its synchronous callback atomically; a callback error discards
// the draft, while delivery may fail after committing it. Hosts detach committed
// state from the draft so retaining it cannot mutate published snapshots.
type MutableReplicatedState = MutableReplicatedStateOf[PublishedConversationView]

// MutableReplicatedStateOf applies and publishes detached typed drafts atomically.
type MutableReplicatedStateOf[T any] interface {
	ReplicatedStateOf[T]
	Change(context.Context, func(T) error) error
	Replace(context.Context, T) error
}

// ServiceDefinition identifies a typed Chord facet; local facets never cross
// the transport boundary. Its fields are immutable to callers.
type ServiceDefinition[T any] struct {
	id    string
	local bool
}

// DefineService declares a transport-visible service token without activating it. It panics for empty or reserved identifiers.
func DefineService[T any](id string) ServiceDefinition[T] {
	if id == "" {
		panic("Service ID must not be empty")
	}
	if strings.HasPrefix(id, "$chord.") {
		panic("Service IDs beginning with $chord. are reserved")
	}
	return ServiceDefinition[T]{id: id}
}

// Id is the upstream service registration identifier.
func (service ServiceDefinition[T]) Id() string { return service.id }

// Local reports whether this service is restricted to the local control plane.
func (service ServiceDefinition[T]) Local() bool { return service.local }

// PicoHarnessService registers the local harness control plane.
var PicoHarnessService = ServiceDefinition[*Harness]{id: "pi.harness", local: true}

// PicoConversationServiceDefinition registers the keyed conversation service.
// Go shares the type and value namespace, so the token needs a suffix while
// the upstream interface name remains PicoConversationService.
var PicoConversationServiceDefinition = ServiceDefinition[*PicoConversationService]{id: "pi.conversation"}

// PicoConversationService binds remote operations to one conversation.
type PicoConversationService struct {
	View         ReplicatedState
	harness      *Harness
	conversation *ConversationHandle
}

// CreatePicoConversationService creates the upstream keyed conversation facade.
func CreatePicoConversationService(harness *Harness, conversation *ConversationHandle, view ReplicatedState) *PicoConversationService {
	return &PicoConversationService{View: view, harness: harness, conversation: conversation}
}

// Send admits input and returns its id.
func (service *PicoConversationService) Send(ctx context.Context, input SendInput) (Id, error) {
	handle, err := service.conversation.Send(ctx, input)
	if err != nil {
		return 0, err
	}
	return handle.Id, nil
}

// Write appends a passive entry.
func (service *PicoConversationService) Write(ctx context.Context, entry NewEntry) (Id, error) {
	return service.conversation.Write(ctx, entry)
}

// InputAbort withdraws an input only if it belongs to this conversation.
func (service *PicoConversationService) InputAbort(ctx context.Context, id Id) (string, error) {
	return service.harness.AbortInput(ctx, id, &service.conversation.Id)
}

// ConfigSet validates and commits a configuration patch.
func (service *PicoConversationService) ConfigSet(ctx context.Context, patch JsonObject) error {
	return service.conversation.Config().Set(ctx, patch)
}

// Abort aborts foreground work and waits for owned conversations.
func (service *PicoConversationService) Abort(ctx context.Context) error {
	return service.conversation.Abort(ctx)
}

// Reset resets the transcript, optionally retaining a handoff.
func (service *PicoConversationService) Reset(ctx context.Context, handoff *string) error {
	return service.conversation.Reset(ctx, handoff)
}

// Collapse starts a manual collapse.
func (service *PicoConversationService) Collapse(ctx context.Context, instructions *string) (Id, error) {
	return service.conversation.Collapse(ctx, instructions)
}

// Fork forks at an entry, or at the start when at is nil.
func (service *PicoConversationService) Fork(ctx context.Context, at *Id, spec ConversationSpec) (Id, error) {
	child, err := service.conversation.Fork(ctx, at, spec)
	if err != nil {
		return 0, err
	}
	return child.Id, nil
}

// Entries always scopes the scan to the service's conversation.
func (service *PicoConversationService) Entries(ctx context.Context, scan EntryScan) ([]Entry, error) {
	scan.ConversationId = service.conversation.Id
	return service.harness.Entries(ctx, scan)
}
