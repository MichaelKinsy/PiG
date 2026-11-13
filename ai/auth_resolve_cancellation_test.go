package ai

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// The resolve functions of Pi's API-key auth providers (providers/{amazon-bedrock,anthropic,cloudflare-auth,google-vertex}.ts) await ctx.env and check the
// signal around every lookup (`signal.throwIfAborted(); await ctx.env(name); signal.throwIfAborted()`), so a cancellation that arrives while any one lookup
// runs rejects the resolution with the cancellation, whether or not the lookup found a value. The lookups are enumerated from an uncancelled run, then each
// one in turn cancels the context from inside Env.
func TestAPIKeyAuthResolveObservesACancellationDuringAnyEnvLookup(t *testing.T) {
	for _, providerID := range []string{"cloudflare-workers-ai", "cloudflare-ai-gateway", "google-vertex", "amazon-bedrock", "anthropic"} {
		t.Run(providerID, func(t *testing.T) {
			auth, err := BuiltinProviderAuth(providerID)
			if err != nil || auth.APIKey == nil {
				t.Fatalf("BuiltinProviderAuth(%q) = %#v, %v", providerID, auth, err)
			}
			resolve := func(ctx context.Context, env func(string) (string, bool)) (*AuthResult, error) {
				return auth.APIKey.Resolve(ctx, APIKeyAuthInput{Ctx: AuthContext{Env: env, FileExists: func(string) bool { return true }}})
			}
			var lookups []string
			if _, err := resolve(t.Context(), func(name string) (string, bool) { lookups = append(lookups, name); return "", false }); err != nil {
				t.Fatalf("uncancelled resolve: %v", err)
			}
			if len(lookups) == 0 {
				t.Fatal("the resolution made no environment lookup")
			}
			// A signal that is already aborted rejects the resolution; every resolver but Anthropic's checks the signal before its first lookup, so those look nothing up.
			aborted, abort := context.WithCancel(t.Context())
			abort()
			preLookups := 0
			if _, err := resolve(aborted, func(string) (string, bool) { preLookups++; return "", false }); !errors.Is(err, context.Canceled) || (providerID != "anthropic" && preLookups != 0) {
				t.Fatalf("an aborted signal resolved with error %v after %d lookups", err, preLookups)
			}
			for index, name := range lookups {
				for _, found := range []bool{false, true} {
					t.Run(fmt.Sprintf("lookup %d %s found %v", index, name, found), func(t *testing.T) {
						ctx, cancel := context.WithCancel(t.Context())
						defer cancel()
						calls := 0
						_, err := resolve(ctx, func(string) (string, bool) {
							calls++
							if calls-1 == index {
								cancel()
								return "value", found
							}
							return "", false
						})
						if !errors.Is(err, context.Canceled) {
							t.Fatalf("a cancellation during lookup %d (%s) resolved with error %v", index, name, err)
						}
					})
				}
			}
		})
	}
}
