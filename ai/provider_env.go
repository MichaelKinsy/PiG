package ai

import (
	"maps"
	"os"
)

// ProviderEnv holds provider-scoped environment overrides whose values take
// precedence over the process environment for provider configuration such as
// regional settings, endpoint placeholders, and proxy variables. Mirrors
// upstream types.ts ProviderEnv (Record<string, string>).
type ProviderEnv = map[string]string

// getProviderEnvValue resolves an environment value from provider-scoped
// overrides first, then the process environment. Mirrors upstream
// provider-env.ts getProviderEnvValue.
//
// The upstream Bun /proc/self/environ sandbox fallback is intentionally not
// ported: it works around a Bun-specific bug where a compiled binary's
// process.env can be empty inside a Linux sandbox. os.Getenv reads the real
// process environment in Go, so the fallback is unnecessary.
func mergeProviderEnv(configured, request ProviderEnv) ProviderEnv {
	if len(configured) == 0 && len(request) == 0 {
		return nil
	}
	merged := maps.Clone(configured)
	if merged == nil {
		merged = make(ProviderEnv, len(request))
	}
	maps.Copy(merged, request)
	return merged
}

func getProviderEnvValue(name string, env ProviderEnv) string {
	if v := env[name]; v != "" {
		return v
	}
	return os.Getenv(name)
}
