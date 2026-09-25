// Package secretresolver provides the product-neutral resolver seam for typed
// Piglet secret refs. Raw Pig registers no resolver; products contribute
// namespaced schemes and enforce their own authorization.
//
// pig additive (D18): product-neutral Piglet secret resolver seam.
package secretresolver

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Resolver resolves one opaque identifier to secret bytes. Implementations must
// not log or wrap errors with the returned value.
type Resolver func(context.Context, string) ([]byte, error)

var (
	mu        sync.RWMutex
	resolvers = map[string]Resolver{}
	schemeRE  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// Register installs one namespaced resolver. Duplicate schemes fail so import
// order cannot change authorization behavior.
func Register(scheme string, resolver Resolver) error {
	scheme = strings.TrimSpace(scheme)
	if !schemeRE.MatchString(scheme) {
		return fmt.Errorf("secret resolver scheme %q must be lowercase alphanumeric with hyphens", scheme)
	}
	if resolver == nil {
		return fmt.Errorf("secret resolver %q is nil", scheme)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, exists := resolvers[scheme]; exists {
		return fmt.Errorf("secret resolver %q is already registered", scheme)
	}
	resolvers[scheme] = resolver
	return nil
}

// Supports reports whether a resolver is installed for scheme.
func Supports(scheme string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, exists := resolvers[scheme]
	return exists
}

// Resolve validates `<scheme>:<opaque-id>` and invokes that exact resolver.
func Resolve(ctx context.Context, ref string) ([]byte, error) {
	scheme, opaque, ok := strings.Cut(strings.TrimSpace(ref), ":")
	if !ok || !schemeRE.MatchString(scheme) || strings.TrimSpace(opaque) == "" {
		return nil, fmt.Errorf("secret ref must be <resolver>:<opaque-id>")
	}
	mu.RLock()
	resolver := resolvers[scheme]
	mu.RUnlock()
	if resolver == nil {
		return nil, fmt.Errorf("secret resolver %q is not installed; install the package that provides it", scheme)
	}
	value, err := resolver(ctx, opaque)
	if err != nil {
		return nil, fmt.Errorf("secret resolver %q denied or failed", scheme)
	}
	if len(value) == 0 {
		return nil, fmt.Errorf("secret resolver %q returned an empty value", scheme)
	}
	return value, nil
}
