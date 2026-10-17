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
	lazy := &lazyOAuth{
		auth: OAuthAuth{Name: input.Name, IsSubscription: input.IsSubscription, LoginLabel: input.LoginLabel},
		load: input.Load,
	}
	lazy.auth.Login, lazy.auth.Refresh, lazy.auth.ToAuth = lazy.login, lazy.refresh, lazy.toAuth
	return &lazy.auth
}

// lazyOAuth is one LazyOAuth method and its load state in a single allocation: provider lookups build an OAuth method per call.
type lazyOAuth struct {
	auth   OAuthAuth
	load   func() (*OAuthAuth, error)
	once   sync.Once
	loaded *OAuthAuth
	err    error
}

func (l *lazyOAuth) flow() (*OAuthAuth, error) {
	l.once.Do(func() { l.loaded, l.err = l.load() })
	return l.loaded, l.err
}

func (l *lazyOAuth) login(ctx context.Context, interaction AuthInteraction, options LoginOptions) (Credential, error) {
	auth, err := l.flow()
	if err != nil {
		return Credential{}, err
	}
	return auth.Login(ctx, interaction, options)
}

func (l *lazyOAuth) refresh(ctx context.Context, credential Credential) (Credential, error) {
	auth, err := l.flow()
	if err != nil {
		return Credential{}, err
	}
	return auth.Refresh(ctx, credential)
}

func (l *lazyOAuth) toAuth(credential Credential) (ModelAuth, error) {
	auth, err := l.flow()
	if err != nil {
		return ModelAuth{}, err
	}
	return auth.ToAuth(credential)
}
