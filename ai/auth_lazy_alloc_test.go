package ai

import (
	"context"
	"sync"
	"testing"
)

var lazyOAuthSink *OAuthAuth

// A lazy OAuth method costs one allocation for its state plus one per bound method: provider lookups build one per call.
func TestLazyOAuthCostsOneAllocationPlusItsMethods(t *testing.T) {
	load := func() (*OAuthAuth, error) { return nil, nil }
	if allocs := testing.AllocsPerRun(100, func() { lazyOAuthSink = LazyOAuth(LazyOAuthInput{Name: "Lazy", Load: load}) }); allocs > 4 {
		t.Fatalf("LazyOAuth = %v allocations, want at most 4", allocs)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		var ok bool
		lazyOAuthSink, ok = OAuthProviderAuth("anthropic")
		if !ok {
			panic("anthropic has no OAuth")
		}
	}); allocs > 5 {
		t.Fatalf("OAuthProviderAuth = %v allocations, want at most 5", allocs)
	}
}

// Concurrent first uses of every method load the flow once and each reaches it.
func TestLazyOAuthConcurrentFirstUseLoadsOnce(t *testing.T) {
	var loads, refreshes, toAuths, logins int
	var mu sync.Mutex
	count := func(n *int) { mu.Lock(); *n++; mu.Unlock() }
	flow := &OAuthAuth{
		Login: func(context.Context, AuthInteraction, LoginOptions) (Credential, error) {
			count(&logins)
			return Credential{Access: "login"}, nil
		},
		Refresh: func(context.Context, Credential) (Credential, error) {
			count(&refreshes)
			return Credential{Access: "refresh"}, nil
		},
		ToAuth: func(Credential) (ModelAuth, error) { count(&toAuths); return ModelAuth{APIKey: "key"}, nil },
	}
	auth := LazyOAuth(LazyOAuthInput{Name: "Lazy", Load: func() (*OAuthAuth, error) { count(&loads); return flow, nil }})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if c, err := auth.Login(context.Background(), AuthInteraction{}, LoginOptions{}); err != nil || c.Access != "login" {
				t.Errorf("Login = %v, %v", c, err)
			}
		})
		wg.Go(func() {
			if c, err := auth.Refresh(context.Background(), Credential{}); err != nil || c.Access != "refresh" {
				t.Errorf("Refresh = %v, %v", c, err)
			}
		})
		wg.Go(func() {
			if m, err := auth.ToAuth(Credential{}); err != nil || m.APIKey != "key" {
				t.Errorf("ToAuth = %v, %v", m, err)
			}
		})
	}
	wg.Wait()
	if loads != 1 || logins != 8 || refreshes != 8 || toAuths != 8 {
		t.Fatalf("loads, logins, refreshes, toAuths = %d, %d, %d, %d; want 1, 8, 8, 8", loads, logins, refreshes, toAuths)
	}
}
