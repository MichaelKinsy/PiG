// Package chord is the local Go runtime for the subset of Pi 0.87.1's
// packages/chord that the experimental service graph consumes: replicated
// state, the remote service provider and its endpoint, the operation-stream
// replica binding, and JSON-copy transports.
//
// Upstream source: .upstream/v0.87.1/packages/chord/src/{types,api}.ts and
// services/{state,provider,consumer,instances,wire,errors,loopback}.ts.
// packages/chord is outside PORT_MAP package scope (see PORT_MAP.md); this
// package exists so experimental service lanes share one concrete runtime
// instead of consumer-owned stand-ins.
//
// Go mapping decisions (not wire changes):
//   - Chord's Context is context.Context. Synthetic deliveries (hydration,
//     provider lifecycle) use context.Background(), matching upstream's
//     BACKGROUND_CONTEXT.
//   - Service tokens are pico3.ServiceDefinition[T] created by
//     pico3.DefineService; this package does not define a second token type.
//   - Replicated state satisfies pico3.ReplicatedStateOf[T] and
//     pico3.MutableReplicatedStateOf[T]. A state value is stored as its strict
//     JSON representation; Value returns a detached decoded copy, which is the
//     Go equivalent of upstream's immutable revisions.
//   - Operations are pico3.Op tuples (the chord/delta vocabulary already ported
//     in agent/harness/pico3/delta.go). Operation shape is not canonical
//     upstream either; consumers depend only on the resulting value.
//   - A provider classifies a Go implementation by reflection over the
//     service type's method set. Member names are the method names with a
//     lower-case first letter ("CycleThinking" -> "cycleThinking"), which is
//     the upstream TypeScript member name. A method member has the signature
//     func(context.Context, args...) error or func(context.Context, args...)
//     (R, error); a state member takes no arguments and returns a replicated
//     state created by NewReplicatedState.
//   - Go cannot synthesize a typed proxy, so the consumer side exposes an
//     untyped RemoteService facade (Call, State, CallResult). Typed client
//     adapters over that facade belong to the owning service lane, which
//     registers them with RegisterRemoteClient (or passes
//     FacetOptions.RemoteClients). A facet host needs one for every
//     remotely exposable singleton a facet uses: in-host ones go through the
//     host's internal loopback binding, as upstream, so retained state
//     subscriptions follow provider replacement. Keyed in-host observation
//     still resolves through the local registry.
//   - Value accessors on state handles panic with the access error after
//     revocation (upstream throws); Load returns it.
//   - Facets require a guarded service view registered with RegisterServiceView next to the contract's pico3.DefineService token, including for in-host services. Register once before acquiring or observing the service. Every method invocation must resolve the current target and check access; never cache the resolved implementation. State members use StateView. A missing registration fails Get or observation instead of exposing the raw implementation.
//   - Mutate callbacks receive a detached decoded copy of the state rather
//     than upstream's revocable copy-on-write Draft proxy.
//
// Ported: replicated state and replicas, provider (singleton and keyed,
// provide/withdraw/replace/spawn/invoke/subscribe/dispose), endpoint
// control calls, loopback and JSON-copy transports, binding
// (use/observe/ready/rebind/dispose), facet host (setup validation,
// dependency-ordered activation, reload cutover, external service sources,
// disposal) and facet loaders.
//
// Not ported: services/state-codec.ts per-subscription, per-instance/member path dictionaries, interned or omitted paths, and dictionary resets. Plain Op tuples are valid WireOps to emit, but this runtime cannot consume upstream compressed streams. The wire.ts parse* validators are also unported: JSON decoding does not validate the full Chord wire grammar. Bundler/Node bundle loading and the separate pi-client/pi-server framed transports are unported. The in-memory JSON-copy transport does not establish framed interoperability.
package chord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
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
	Ops      []pico3.Op
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
	UpdateUnavailable = "unavailable"
	UpdateReplaced    = "replaced"
	UpdateSpawned     = "spawned"
	UpdateClosed      = "closed"
)

// ServiceProviderUpdate is one ordered provider publication.
//
//   - "state": Address (nil for a singleton), Member, Sequence, Ops.
//   - "unavailable": no fields.
//   - "replaced": Snapshot.
//   - "spawned": Snapshot (serialized under the upstream "instance" key).
//   - "closed": Address (serialized under the upstream "instance" key).
type ServiceProviderUpdate struct {
	Type     string
	Address  *ServiceInstanceAddress
	Member   string
	Sequence int
	Ops      []pico3.Op
	Snapshot *ServiceInstanceSnapshot
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
