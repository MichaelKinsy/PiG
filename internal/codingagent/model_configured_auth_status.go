// Ports packages/coding-agent/src/core/provider-composer.ts
package codingagent

import "github.com/MichaelKinsy/PiG/ai"

// ConfiguredRequestAuthStatus reports explicit provider configuration without reading credentials or executing command values. Like Pi provider-composer.ts:597-610, a "$VAR" key is evaluated against the process environment, never a stored credential's env, so this synchronous snapshot getter performs no credential I/O.
func (r *ModelRegistry) ConfiguredRequestAuthStatus(providerID string) (ai.AuthStatus, bool) {
	input := r.GetRegisteredProviderConfig(providerID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, fromExtension := "", false
	if r.config != nil {
		value = r.config.Providers[providerID].APIKey
	}
	if provider, ok := r.dynamic[providerID]; ok && provider.APIKey != "" {
		value, fromExtension = provider.APIKey, true
	}
	if input != nil && input.APIKey != "" {
		value, fromExtension = input.APIKey, true
	}
	if value == "" {
		return ai.AuthStatus{}, false
	}
	return configuredRequestAuthStatus(value, fromExtension), true
}
