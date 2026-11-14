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
