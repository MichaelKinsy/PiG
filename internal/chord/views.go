package chord

import (
	"context"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

// Upstream services/handle.ts hands facets a guarded proxy (ServiceSlot view)
// instead of the provider object: every member access resolves the currently
// bound target and checks the holder's access, so a retained method follows
// a reload cutover and fails after revocation or disposal. Go cannot
// synthesize such a proxy for an arbitrary interface, so each service contract
// registers a view constructor: a T whose every method calls resolve and
// forwards to the returned target. The owning service lane supplies it next to
// its DefineService token, as it supplies RemoteClients adapters.

var serviceViews sync.Map // service ID -> func(func() (any, error)) any

// RegisterServiceView registers the guarded view for def. view must return a
// T whose methods call resolve on every invocation and never cache its result.
// Registering the same service twice panics.
func RegisterServiceView[T any](def pico3.ServiceDefinition[T], view func(resolve func() (T, error)) T) {
	factory := func(resolve func() (any, error)) any {
		return view(func() (T, error) {
			target, err := resolve()
			if err != nil {
				var zero T
				return zero, err
			}
			return target.(T), nil
		})
	}
	if _, loaded := serviceViews.LoadOrStore(def.Id(), factory); loaded {
		panic(fmt.Sprintf("chord: service view for %s is already registered", def.Id()))
	}
}

func serviceView(serviceId string, resolve func() (any, error)) (any, error) {
	factory, ok := serviceViews.Load(serviceId)
	if !ok {
		return nil, fmt.Errorf("Service %s has no registered service view (RegisterServiceView)", serviceId)
	}
	return factory.(func(func() (any, error)) any)(resolve), nil
}

// StateView is the guarded counterpart of a replicated state member for
// service views: Value and Subscribe resolve the current state on each call.
// Value panics with the resolution error (revoked access, disconnected
// service) because upstream's value property throws and Value has no error
// result; Subscribe returns the error.
func StateView[T any](resolve func() (pico3.ReplicatedStateOf[T], error)) pico3.ReplicatedStateOf[T] {
	return stateView[T]{resolve: resolve}
}

type stateView[T any] struct {
	resolve func() (pico3.ReplicatedStateOf[T], error)
}

func (view stateView[T]) Value() T {
	state, err := view.resolve()
	if err != nil {
		panic(err)
	}
	return state.Value()
}

func (view stateView[T]) Subscribe(listener func(T, context.Context, pico3.ReplicatedStateDelivery)) (func(), error) {
	state, err := view.resolve()
	if err != nil {
		return nil, err
	}
	return state.Subscribe(listener)
}
