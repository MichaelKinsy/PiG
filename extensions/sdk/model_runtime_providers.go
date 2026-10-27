package sdk

import (
	"crypto/rand"
)

// The Provider object members of Pi's ModelRuntime (model-runtime.ts getProvider, getProviders, getRegisteredNativeProvider, registerNativeProvider). A Provider object holds functions, which stay in this process: the host registers a carrier for the one this extension registers, and its runtime calls it over the connection, as it does for the Session's. A provider with no Provider object of ours (a builtin or composed one) is not served: the call fails with the D78 error, as the session-bound registry's does.

// RegisterNativeProvider is `registerNativeProvider(provider)`: the provider's catalog, authentication and API run in this process, for this runtime.
func (r *ModelRuntime) RegisterNativeProvider(provider *Provider) error {
	decl, err := providerDeclaration(provider, rand.Text())
	if err != nil {
		return err
	}
	e := r.object.context.ext
	e.providerMu.Lock()
	if e.nativeProviders == nil {
		e.nativeProviders = map[string]*Provider{}
	}
	e.nativeProviders[decl.Key] = provider
	e.providerMu.Unlock()
	_, err = graphMethodCall[graphDone](r.object, "registerNativeProvider", decl)
	return err
}

// GetRegisteredNativeProvider is `getRegisteredNativeProvider(providerName)`: the Provider object registered for the name, nil when there is none.
func (r *ModelRuntime) GetRegisteredNativeProvider(providerName string) (*Provider, error) {
	decl, err := graphMethodCall[*providerObjectDeclaration](r.object, "getRegisteredNativeProvider", providerName)
	if err != nil || decl == nil {
		return nil, err
	}
	return providerFromDeclaration(r.object.context, *decl)
}

// GetProvider is `getProvider(provider)`: the Provider object of the provider, nil when the runtime has none by that id.
func (r *ModelRuntime) GetProvider(provider string) (*Provider, error) {
	decl, err := graphMethodCall[*providerObjectDeclaration](r.object, "getProvider", provider)
	if err != nil || decl == nil {
		return nil, err
	}
	return providerFromDeclaration(r.object.context, *decl)
}

// GetProviders is `getProviders()`: the Provider object of every provider of the runtime.
func (r *ModelRuntime) GetProviders() ([]*Provider, error) {
	decls, err := graphMethodCall[[]providerObjectDeclaration](r.object, "getProviders")
	if err != nil {
		return nil, err
	}
	providers := make([]*Provider, 0, len(decls))
	for _, decl := range decls {
		provider, err := providerFromDeclaration(r.object.context, decl)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	return providers, nil
}
