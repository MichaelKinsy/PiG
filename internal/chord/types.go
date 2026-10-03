// Package chord ports Pi 1.0.0's packages/chord: replicated state, the remote service provider and its endpoint, the operation-stream replica binding, facet hosts and JSON-copy transports. The operation vocabulary lives in package delta, strict-JSON helpers in chordjson and context helpers in chordctx.
//
// Upstream source: .upstream/v1.0.0/packages/chord/src/{types,api}.ts and services/{state,provider,consumer,instances,wire,errors,loopback}.ts.
//
// Go mapping decisions (not wire changes):
//   - Chord's Context is context.Context. Synthetic deliveries (hydration, provider lifecycle) use context.Background(), matching upstream's BACKGROUND_CONTEXT.
//   - A service token is ServiceDefinition[T] created by DefineService.
//   - Replicated state satisfies ReplicatedStateOf[T] and MutableReplicatedStateOf[T]. A state value is stored as its strict JSON representation; Value returns a detached decoded copy, which is the Go equivalent of upstream's immutable revisions. Mutate callbacks receive a detached decoded copy rather than upstream's revocable copy-on-write Draft proxy; the change is diffed against the stored revision by delta.Tracker.PrepareCandidate.
//   - State delivery follows 1.0.0: each subscription serializes its callbacks and keeps at most 100 pending deliveries; a listener that is still working returns a Completion (the Go form of a Promise) through SubscribeAsync; subscriber failures go to the state's error reporter (SetUncaughtErrorReporter by default) and exact publication listeners' failures are returned by the publishing call.
//   - Operations are Op tuples (delta.Op). Operation shape is not canonical upstream either; consumers depend only on the resulting value. A provider subscription buffers 100 updates and then rebaselines with a "reset" update.
//   - A provider classifies a Go implementation by reflection over the service type's method set. Member names are the method names with a lower-case first letter ("CycleThinking" -> "cycleThinking"), which is the upstream TypeScript member name. A method member has the signature func(context.Context, args...) error or func(context.Context, args...) (R, error); a state member takes no arguments and returns a replicated state created by NewReplicatedState.
//   - Go cannot synthesize a typed proxy, so the consumer side exposes an untyped RemoteService facade (Call, State, CallResult). Typed client adapters over that facade belong to the owning service lane, which registers them with RegisterRemoteClient (or passes FacetOptions.RemoteClients). A facet host needs one for every remotely exposable singleton a facet uses: in-host ones go through the host's internal loopback binding, as upstream, so retained state subscriptions follow provider replacement. Keyed in-host observation still resolves through the local registry.
//   - Value accessors on state handles panic with the access error after revocation (upstream throws); Load returns it.
//   - Facets require a guarded service view registered with RegisterServiceView next to the contract's DefineService token, including for in-host services. Register once before acquiring or observing the service. Every method invocation must resolve the current target and check access; never cache the resolved implementation. State members use StateView. A missing registration fails Get or observation instead of exposing the raw implementation.
//
// ServiceStateEncoder and ServiceStateDecoder maintain independent path dictionaries per subscription, instance, and state member. ParseService* and ParseWireService* validate their distinct tuple grammars before conversion. The JSON-copy transport remains local; framed routing supplies a separate transport boundary.
package chord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ServiceMode is "singleton" or "keyed".
type ServiceMode string

// Service modes.
const (
	ServiceSingleton ServiceMode = "singleton"
	ServiceKeyed     ServiceMode = "keyed"
)

// ServiceCatalogueEntry is one provider catalogue row.
type ServiceCatalogueEntry struct {
	ServiceId string      `json:"serviceId"`
	Mode      ServiceMode `json:"mode"`
}

// ServiceInstanceAddress identifies one generation of a keyed instance.
type ServiceInstanceAddress struct {
	Key        string `json:"key"`
	Generation int    `json:"generation"`
}

// Service member kinds.
const (
	MemberMethod = "method"
	MemberState  = "state"
)

// ServiceMemberSnapshot describes a method member, or a state member with its
// current sequence and a base ["r", value] operation batch. Sequence and Ops
// are serialized only for state members.
type ServiceMemberSnapshot struct {
	Name     string
	Kind     string
	Sequence int
	Ops      []Op
}

// ServiceInstanceSnapshot describes one live instance. Instance is nil for a
// singleton.
type ServiceInstanceSnapshot struct {
	Instance *ServiceInstanceAddress `json:"instance,omitempty"`
	Members  []ServiceMemberSnapshot `json:"members"`
}

// ServiceSubscriptionSnapshot is the coherent state a subscription starts from.
type ServiceSubscriptionSnapshot struct {
	ServiceId string                    `json:"serviceId"`
	Mode      ServiceMode               `json:"mode"`
	Instances []ServiceInstanceSnapshot `json:"instances"`
}

// Provider update types.
const (
	UpdateState       = "state"
	UpdateReset       = "reset"
	UpdateUnavailable = "unavailable"
	UpdateReplaced    = "replaced"
	UpdateSpawned     = "spawned"
	UpdateClosed      = "closed"
)

// ServiceProviderUpdate is one ordered provider publication.
//
//   - "state": Address (nil for a singleton), Member, Sequence, Ops.
//   - "reset": Reset, a full subscription snapshot whose every state member is a root replacement. A subscription is rebaselined this way when its pending updates overflow.
//   - "unavailable": no fields.
//   - "replaced": Snapshot.
//   - "spawned": Snapshot (serialized under the upstream "instance" key).
//   - "closed": Address (serialized under the upstream "instance" key).
type ServiceProviderUpdate struct {
	Type     string
	Address  *ServiceInstanceAddress
	Member   string
	Sequence int
	Ops      []Op
	Snapshot *ServiceInstanceSnapshot
	Reset    *ServiceSubscriptionSnapshot
}

// ServiceCall addresses one method invocation. Args are strict JSON values.
type ServiceCall struct {
	ServiceId string                  `json:"serviceId"`
	Instance  *ServiceInstanceAddress `json:"instance,omitempty"`
	Member    string                  `json:"member"`
	Args      []json.RawMessage       `json:"args"`
}

// ServiceSubscription is an opened provider subscription. Updates published
// after the snapshot are buffered until Activate; Close is idempotent.
type ServiceSubscription interface {
	Snapshot() ServiceSubscriptionSnapshot
	Activate() error
	Close(ctx context.Context) error
}

// UpdateListener receives ordered provider updates for one subscription.
type UpdateListener func(ctx context.Context, update ServiceProviderUpdate)

// RemoteServiceTransport is the pluggable wire boundary consumed by a
// RemoteServiceBinding. Invoke returns nil for a void result. Implementations
// own serialization and isolation copies.
type RemoteServiceTransport interface {
	Invoke(ctx context.Context, call ServiceCall) (json.RawMessage, error)
	Subscribe(ctx context.Context, serviceId string, mode ServiceMode, listener UpdateListener) (ServiceSubscription, error)
}

// RemoteServiceErrorCode is one of the upstream REMOTE_SERVICE_ERROR_CODES.
type RemoteServiceErrorCode string

// Remote service error codes.
const (
	ErrServiceNotAllowed       RemoteServiceErrorCode = "service_not_allowed"
	ErrServiceNotFound         RemoteServiceErrorCode = "service_not_found"
	ErrServiceModeMismatch     RemoteServiceErrorCode = "service_mode_mismatch"
	ErrServiceMemberNotFound   RemoteServiceErrorCode = "service_member_not_found"
	ErrServiceMemberMismatch   RemoteServiceErrorCode = "service_member_mismatch"
	ErrServiceInstanceNotFound RemoteServiceErrorCode = "service_instance_not_found"
	ErrServiceStaleInstance    RemoteServiceErrorCode = "service_stale_instance"
	ErrServiceInvalidValue     RemoteServiceErrorCode = "service_invalid_value"
)

// RemoteServiceError is a typed service routing or validation failure.
type RemoteServiceError struct {
	Code    RemoteServiceErrorCode
	Message string
}

func (err *RemoteServiceError) Error() string { return err.Message }

func remoteError(code RemoteServiceErrorCode, format string, args ...any) error {
	return &RemoteServiceError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// IsRemoteServiceErrorCode reports whether err is a RemoteServiceError with code.
func IsRemoteServiceErrorCode(err error, code RemoteServiceErrorCode) bool {
	var remote *RemoteServiceError
	return errors.As(err, &remote) && remote.Code == code
}

// AggregateError retains every failure while exposing the upstream aggregate message, as JavaScript's AggregateError(errors, message) does: Error() is the message alone and the causes are reachable through Unwrap.
type AggregateError struct {
	Message string
	Errors  []any
}

func (err *AggregateError) Error() string { return err.Message }

// Unwrap exposes each cause that is an error to errors.Is and errors.As.
func (err *AggregateError) Unwrap() []error {
	var causes []error
	for _, value := range err.Errors {
		if cause, ok := value.(error); ok {
			causes = append(causes, cause)
		}
	}
	return causes
}

// NewAggregateError builds an AggregateError over failures in order.
func NewAggregateError(message string, failures []error) *AggregateError {
	values := make([]any, len(failures))
	for i, failure := range failures {
		values[i] = failure
	}
	return &AggregateError{Message: message, Errors: values}
}

// joinErrors mirrors upstream's single-error / AggregateError collection.
func joinErrors(errs []error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return errors.Join(errs...)
	}
}
