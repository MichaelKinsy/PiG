package chord

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// JsonValue is a strict JSON value: nil, bool, float64, string, []any, or *chordjson.Object (an object that keeps JavaScript property order).
type JsonValue = any

// Op is one chord/delta operation tuple.
type Op = delta.Op

// WireOp is a delta tuple with optional path interning or batch-local path omission. Decode it before applying operations to a replica.
type WireOp = delta.WireOp

// Ops is a batch of operations that keeps object key order when it decodes JSON.
type Ops = delta.Ops

// Delta codec, validation and error types live in package delta.
type (
	Encoder         = delta.Encoder
	Decoder         = delta.Decoder
	PathError       = delta.PathError
	UnsafePathError = delta.UnsafePathError
)

var (
	NewEncoder        = delta.NewEncoder
	NewDecoder        = delta.NewDecoder
	AssertValidOp     = delta.AssertValidOpValue
	AssertValidWireOp = delta.AssertValidWireOp
)

// ReplicatedStateDelivery distinguishes initial hydration from publications (upstream ReplicatedStateDelivery, types.ts).
type ReplicatedStateDelivery struct {
	Kind     string
	Sequence int
}

// ReplicatedStateOf is the typed read side of a replicated state. Values are contract-immutable.
type ReplicatedStateOf[T any] interface {
	Value() T
	Subscribe(func(T, context.Context, ReplicatedStateDelivery)) (func(), error)
}

// MutableReplicatedStateOf applies and publishes detached typed drafts atomically.
type MutableReplicatedStateOf[T any] interface {
	ReplicatedStateOf[T]
	Change(context.Context, func(T) error) error
	Replace(context.Context, T) error
}

// ServiceDefinition identifies a typed service contract; local services never cross the transport boundary. Its fields are immutable to callers.
type ServiceDefinition[T any] struct {
	id    string
	local bool
}

// ServiceReference is upstream's `{ readonly id: string }` (types.ts:269-270,308-312): the identity a binding or a source is asked for. A ServiceDefinition is one, and so is a [ServiceID].
type ServiceReference interface{ Id() string }

// ServiceID is a service id as a [ServiceReference].
type ServiceID string

// Id is the service id.
func (id ServiceID) Id() string { return string(id) }

// ServiceIDs lists service ids as references.
func ServiceIDs(ids ...string) []ServiceReference {
	references := make([]ServiceReference, len(ids))
	for i, id := range ids {
		references[i] = ServiceID(id)
	}
	return references
}

// ServiceOptions selects whether a service stays on the local control plane (upstream packages/chord/src/api.ts defineService).
type ServiceOptions struct {
	Local bool
}

// DefineService is upstream defineService(id, options?) (the overload with the optional options): it declares a transport-visible service token without activating it. The optional options argument is the variadic tail (a second one is ignored, as extra JavaScript arguments are); Local services never cross the transport boundary. It panics for empty or reserved identifiers.
// upstream: packages/chord/src/api.ts:73-85
func DefineService[T any](id string, options ...ServiceOptions) ServiceDefinition[T] {
	var chosen ServiceOptions
	if len(options) > 0 {
		chosen = options[0]
	}
	return DefineServiceWithOptions[T](id, chosen)
}

// DefineServiceWithOptions is upstream defineService(id, options) with the required options (the `{ readonly local: true }` overload, api.ts:73): the options select whether the service stays on the local control plane. It panics for empty or reserved identifiers.
// upstream: packages/chord/src/api.ts:73-85
func DefineServiceWithOptions[T any](id string, options ServiceOptions) ServiceDefinition[T] {
	if id == "" {
		panic("Service ID must not be empty")
	}
	if strings.HasPrefix(id, "$chord.") {
		panic("Service IDs beginning with $chord. are reserved")
	}
	return ServiceDefinition[T]{id: id, local: options.Local}
}

// Id is the service registration identifier.
func (service ServiceDefinition[T]) Id() string { return service.id }

// Local reports whether this service is restricted to the local control plane.
func (service ServiceDefinition[T]) Local() bool { return service.local }

// recoverInto stores a recovered panic as an error, for a deferred call that converts a thrown failure into a result.
func recoverInto(err *error) {
	if recovered := recover(); recovered != nil {
		*err = recoveredError(recovered)
	}
}

// deltaNumber converts any Go number or json.Number to float64.
func deltaNumber(value any) (float64, bool) {
	if number, ok := value.(json.Number); ok {
		n, err := strconv.ParseFloat(string(number), 64)
		return n, err == nil
	}
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	default:
		return 0, false
	}
}

// deltaInteger reports whether value is a finite integer number at least minimum.
func deltaInteger(value any, minimum float64) bool {
	n, ok := deltaNumber(value)
	return ok && !math.IsInf(n, 0) && !math.IsNaN(n) && n >= minimum && math.Trunc(n) == n
}

// deltaTuple views an operation in any of its tuple types as a plain slice.
func deltaTuple(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case WireOp:
		return typed, true
	default:
		return nil, false
	}
}

// validateResetSnapshot checks a "reset" update against the service and mode it was subscribed for (upstream validateResetSnapshot).
func validateResetSnapshot(snapshot ServiceSubscriptionSnapshot, serviceId string, mode ServiceMode) error {
	if snapshot.ServiceId != serviceId || snapshot.Mode != mode || (mode == ServiceSingleton && len(snapshot.Instances) > 1) {
		return errors.New("Remote service reset has the wrong service or mode")
	}
	keys := map[string]bool{}
	for _, instance := range snapshot.Instances {
		if (mode == ServiceSingleton) != (instance.Instance == nil) {
			return errors.New("Remote service reset has an invalid instance address")
		}
		if instance.Instance != nil {
			if keys[instance.Instance.Key] {
				return errors.New("Remote service reset repeats an instance key")
			}
			keys[instance.Instance.Key] = true
		}
		for _, member := range instance.Members {
			if member.Kind == MemberState && (len(member.Ops) != 1 || delta.Verb(member.Ops[0]) != "r") {
				return errors.New("Remote service reset must contain full root replacements")
			}
		}
	}
	return nil
}
