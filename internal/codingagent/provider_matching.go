package codingagent

// Ports packages/coding-agent/src/core/provider-matching.ts

import (
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// FindMatchingProviders matches provider IDs and display names exactly, ignoring case and surrounding whitespace in the reference. idName returns a
// provider's ID and display name. The result keeps input order.
func FindMatchingProviders[T any](providers []T, providerRef string, idName func(T) (id, name string)) []T {
	normalized := jsstring.ToLower(jsstring.Trim(providerRef))
	if normalized == "" {
		return nil
	}
	var matched []T
	for _, provider := range providers {
		id, name := idName(provider)
		if jsstring.ToLower(id) == normalized || jsstring.ToLower(name) == normalized {
			matched = append(matched, provider)
		}
	}
	return matched
}
