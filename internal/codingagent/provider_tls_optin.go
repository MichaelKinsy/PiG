package codingagent

import "strings"

// insecureLayer is what one source of provider configuration says about the TLS opt-in: whether the source names the provider at all, its three-state
// `insecure` setting (nil omitted, false, true) and the endpoint it names. The sources are the models.json entry (the file) and the registration of an extension
// or of a program that embeds PiG.
type insecureLayer struct {
	present bool
	setting *bool
	baseURL string
}

// pig additive (D36): providerTLSOptIn is the one answer to "does this provider skip TLS certificate verification"; every place that builds a request's
// entry or transport reads it from here and from nowhere else. The rule (owner decision 29):
//   - a registration that states `insecure` wins, true or false;
//   - one that states nothing keeps the opt-in of the models.json entry, but only while it keeps the entry's endpoint: a registration that names another
//     endpoint never inherits an opt-in the user wrote for the file's endpoint;
//   - without an opt-in in either layer, certificates are verified.
func providerTLSOptIn(file, registration insecureLayer) bool {
	if registration.present && registration.setting != nil {
		return *registration.setting
	}
	if !optedIn(file.setting) {
		return false
	}
	return !registration.present || registration.baseURL == "" || sameEndpoint(registration.baseURL, file.baseURL)
}

// sameEndpoint is whether two base URLs name one endpoint; a trailing slash does not make another one.
func sameEndpoint(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// providerTLSOptInLocked gathers the layers of providerID and answers providerTLSOptIn; the caller holds r.mu. native is the provider's composed native
// registration, which stands for the registration when no legacy registration of the provider exists.
func (r *ModelRegistry) providerTLSOptInLocked(providerID string, native *ProviderConfigInput) bool {
	var file, registration insecureLayer
	if r.config != nil {
		if configured, ok := r.config.Providers[providerID]; ok {
			file = insecureLayer{present: true, setting: configured.Insecure, baseURL: configured.BaseURL}
		}
	}
	if dynamic, ok := r.dynamic[providerID]; ok {
		registration = insecureLayer{present: true, setting: dynamic.Insecure, baseURL: dynamic.BaseURL}
	} else if native != nil {
		registration = insecureLayer{present: true, setting: native.InsecureSetting(), baseURL: native.BaseURL}
	}
	return providerTLSOptIn(file, registration)
}
