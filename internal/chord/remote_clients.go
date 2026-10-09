package chord

import (
	"context"
	"fmt"
)

// AdaptRemoteClient applies the same registered typed facade used by a FacetHost to an explicitly opened remote service. Go cannot synthesize TypeScript's generic service proxy from a type parameter.
func AdaptRemoteClient[T any](definition ServiceDefinition[T], service *RemoteService) (T, error) {
	var zero T
	factory, ok := remoteClients.Load(definition.Id())
	if !ok {
		return zero, fmt.Errorf("Service %s has no remote client adapter", definition.Id())
	}
	value := factory.(func(*RemoteService) any)(service)
	typed, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("Service %s remote client has an incompatible Go contract", definition.Id())
	}
	return typed, nil
}

// UseRemoteClient acquires a singleton and returns its registered typed client view without bypassing binding access/readiness checks.
func UseRemoteClient[T any](binding RemoteServices, definition ServiceDefinition[T]) (T, error) {
	var zero T
	if definition.Local() {
		return zero, remoteError(ErrServiceNotAllowed, "Service %s is process-local", definition.Id())
	}
	service, err := binding.Use(definition.Id())
	if err != nil {
		return zero, err
	}
	return AdaptRemoteClient(definition, service)
}

// ObserveRemoteClient observes every live instance of a keyed service and passes each instance's registered typed client view to
// handler, as upstream RemoteServices.observe<T>(service, handler) passes the typed proxy (consumer.ts:496-523). A process-local
// service is rejected before the allowlist check, as upstream's #assertRemotable. The returned stop function is idempotent.
func ObserveRemoteClient[T any](binding RemoteServices, definition ServiceDefinition[T], handler func(context.Context, T) error) (func(), error) {
	if definition.Local() {
		return nil, remoteError(ErrServiceNotAllowed, "Service %s is process-local", definition.Id())
	}
	return binding.Observe(definition.Id(), func(ctx context.Context, service *RemoteService) error {
		client, err := AdaptRemoteClient(definition, service)
		if err != nil {
			return err
		}
		return handler(ctx, client)
	})
}
