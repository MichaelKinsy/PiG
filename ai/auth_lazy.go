package ai

import (
	"context"
	"sync"
)

// LazyOAuthInput describes an OAuth method whose flow is loaded on first use.
//
// upstream: packages/ai/src/auth/helpers.ts:40 (lazyOAuth input)
type LazyOAuthInput struct {
	Name           string
	IsSubscription bool
	LoginLabel     string
	// Load returns the flow. It runs at most once, at the first Login, Refresh or ToAuth; its error is returned by every one of those calls, as a rejected promise stays rejected.
	Load func() (*OAuthAuth, error)
}

// LazyOAuth wraps an OAuth flow so a provider definition can advertise OAuth without loading the implementation.
//
// upstream: packages/ai/src/auth/helpers.ts:40 (lazyOAuth)
func LazyOAuth(input LazyOAuthInput) *OAuthAuth {
	var (
		once   sync.Once
		loaded *OAuthAuth
		err    error
	)
	load := func() (*OAuthAuth, error) {
		once.Do(func() { loaded, err = input.Load() })
		return loaded, err
	}
	return &OAuthAuth{
		Name:           input.Name,
		IsSubscription: input.IsSubscription,
		LoginLabel:     input.LoginLabel,
		Login: func(ctx context.Context, interaction AuthInteraction, options LoginOptions) (Credential, error) {
			auth, err := load()
			if err != nil {
				return Credential{}, err
			}
			return auth.Login(ctx, interaction, options)
		},
		Refresh: func(ctx context.Context, credential Credential) (Credential, error) {
			auth, err := load()
			if err != nil {
				return Credential{}, err
			}
			return auth.Refresh(ctx, credential)
		},
		ToAuth: func(credential Credential) (ModelAuth, error) {
			auth, err := load()
			if err != nil {
				return ModelAuth{}, err
			}
			return auth.ToAuth(credential)
		},
	}
}
