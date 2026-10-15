package ai

import (
	"context"
	"errors"
	"testing"
)

// upstream: packages/ai/src/auth/helpers.ts:40 (lazyOAuth): metadata is available without loading; the flow loads on first login/refresh/toAuth and only once.
func TestLazyOAuthLoadsOnFirstUseOnce(t *testing.T) {
	loads := 0
	var calls []string
	auth := LazyOAuth(LazyOAuthInput{Name: "Lazy", IsSubscription: true, LoginLabel: "Sign in", Load: func() (*OAuthAuth, error) {
		loads++
		return &OAuthAuth{
			Login: func(context.Context, AuthInteraction, LoginOptions) (Credential, error) {
				calls = append(calls, "login")
				return Credential{Key: "from-login"}, nil
			},
			Refresh: func(_ context.Context, c Credential) (Credential, error) {
				calls = append(calls, "refresh")
				c.Key += "+refreshed"
				return c, nil
			},
			ToAuth: func(c Credential) (ModelAuth, error) {
				calls = append(calls, "toAuth")
				return ModelAuth{APIKey: c.Key}, nil
			},
		}, nil
	}})
	if auth.Name != "Lazy" || !auth.IsSubscription || auth.LoginLabel != "Sign in" || loads != 0 {
		t.Fatalf("metadata must not load the flow: %+v loads=%d", auth, loads)
	}
	credential, err := auth.Login(t.Context(), AuthInteraction{}, LoginOptions{})
	if err != nil || credential.Key != "from-login" {
		t.Fatalf("login = %+v, %v", credential, err)
	}
	refreshed, err := auth.Refresh(t.Context(), credential)
	if err != nil || refreshed.Key != "from-login+refreshed" {
		t.Fatalf("refresh = %+v, %v", refreshed, err)
	}
	if model, err := auth.ToAuth(refreshed); err != nil || model.APIKey != "from-login+refreshed" {
		t.Fatalf("toAuth = %+v, %v", model, err)
	}
	if loads != 1 || len(calls) != 3 {
		t.Fatalf("loads = %d calls = %v", loads, calls)
	}
}

// A rejected load stays rejected: every call returns that error and the loader does not run again.
func TestLazyOAuthKeepsLoadFailure(t *testing.T) {
	loads := 0
	boom := errors.New("cannot load")
	auth := LazyOAuth(LazyOAuthInput{Name: "Broken", Load: func() (*OAuthAuth, error) { loads++; return nil, boom }})
	for range 2 {
		if _, err := auth.Login(t.Context(), AuthInteraction{}, LoginOptions{}); !errors.Is(err, boom) {
			t.Fatalf("login error = %v", err)
		}
		if _, err := auth.Refresh(t.Context(), Credential{}); !errors.Is(err, boom) {
			t.Fatalf("refresh error = %v", err)
		}
		if _, err := auth.ToAuth(Credential{}); !errors.Is(err, boom) {
			t.Fatalf("toAuth error = %v", err)
		}
	}
	if loads != 1 {
		t.Fatalf("loader ran %d times", loads)
	}
}
