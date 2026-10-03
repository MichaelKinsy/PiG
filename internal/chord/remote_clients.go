package chord

import (
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
