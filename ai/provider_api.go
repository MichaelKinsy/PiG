package ai

// ProviderCatalogServesAPI reports whether the built-in catalog of providerID lists a model of api, that is, whether the provider has an implementation of that api of its own.
// upstream: compat.ts:236-239 (getBuiltinProviderForModel: the provider's own stream is used only when `provider.getModels().some((candidate) => candidate.api === model.api)`;
// any other api, a missing one included, goes to the API registry, which refuses a name nothing is registered for, compat.ts:244-247).
func ProviderCatalogServesAPI(providerID string, api API) bool {
	registryProviderOnce.Do(initRegistryProvider)
	for _, model := range registryByProvider[providerID] {
		if model.API == api {
			return true
		}
	}
	return false
}

// ownStreamProviders are the built-in providers that carry a stream implementation of their own besides the shared per-api ones. Each provider registers itself
// from its own file, so shared code never names a provider: it asks ProviderHasOwnStreamFor.
var ownStreamProviders = map[string]bool{}

// registerOwnStreamProvider records that providerID has its own stream implementation.
func registerOwnStreamProvider(providerID string) { ownStreamProviders[providerID] = true }

// ProviderHasOwnStreamFor reports whether providerID has a stream of its own and its catalog lists a model of api, which is when Pi uses the provider own stream
// (compat.ts:236-239 getBuiltinProviderForModel); any other api goes to the API registry and is refused when nothing is registered for it (compat.ts:244-247).
func ProviderHasOwnStreamFor(providerID string, api API) bool {
	return ownStreamProviders[providerID] && ProviderCatalogServesAPI(providerID, api)
}
