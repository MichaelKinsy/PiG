package runtime

import (
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Go-only: the single-native registries the runtime declares for its own natives (`ServerNatives`, `WorkerProcess`),
// for the plugins that hand a native to another plugin (the SSH manager `EnvManagers`, the model configuration
// `ModelResources`): a registry whose value is its first contribution, nil while there is none.

// NewNativeRegistry declares a single-native registry named name (version 1).
func NewNativeRegistry(name string) registries.Registry[any, any] { return singleNative[any](name) }

// ServeNative serves registry (from NewNativeRegistry) on facet, its composed value a *V: the defining plugin's facet.
func ServeNative[V any](facet host.FacetHost, registry registries.Registry[any, any]) error {
	return serveNative[V](facet, registry)
}

// NativeOf reads a single-native registry's *V at each use: the declared dependency of a factory
// (`self.Registry(registry)`); what names the native in the error of an absent one.
func NativeOf[V any](source *registries.RegistryValueSource, what string) func() (*V, error) {
	return nativeOf[V](source, what)
}
