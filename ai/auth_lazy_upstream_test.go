package ai

import (
	"context"
	"errors"
	"testing"
)

// auth/helpers.ts lazyOAuth: the returned method carries the input's name, isSubscription and loginLabel without loading; the flow loads
// on the first login, refresh or toAuth, once, and every later call delegates to that same flow with its arguments; a load that failed
// stays failed for every call (a rejected promise stays rejected).
func TestLazyOAuthLoadsOnFirstUseOnceAndDelegates(t *testing.T) {
	loads := 0
	var loginInteraction AuthInteraction
	var loginOptions LoginOptions
	var refreshed Credential
	var converted Credential
	flow := &OAuthAuth{
		Name: "inner",
		Login: func(_ context.Context, interaction AuthInteraction, options LoginOptions) (Credential, error) {
			loginInteraction, loginOptions = interaction, options
			return Credential{Type: CredentialOAuth, Access: "logged-in"}, nil
		},
		Refresh: func(_ context.Context, credential Credential) (Credential, error) {
			refreshed = credential
			return Credential{Type: CredentialOAuth, Access: "refreshed"}, nil
		},
		ToAuth: func(credential Credential) (ModelAuth, error) {
			converted = credential
			return ModelAuth{APIKey: "derived"}, nil
		},
	}
	auth := LazyOAuth(LazyOAuthInput{Name: "Lazy", IsSubscription: true, LoginLabel: "Sign in", Load: func() (*OAuthAuth, error) { loads++; return flow, nil }})

	if auth.Name != "Lazy" || !auth.IsSubscription || auth.LoginLabel != "Sign in" {
		t.Fatalf("metadata = %q %t %q", auth.Name, auth.IsSubscription, auth.LoginLabel)
	}
	if loads != 0 {
		t.Fatalf("the flow loaded at construction: %d", loads)
	}

	options := LoginOptions{AgentName: new("agent")}
	interaction := AuthInteraction{Notify: func(AuthEvent) {}}
	got, err := auth.Login(context.Background(), interaction, options)
	if err != nil || got.Access != "logged-in" || loads != 1 || loginInteraction.Notify == nil || loginOptions.AgentName == nil || *loginOptions.AgentName != "agent" {
		t.Fatalf("login = %+v, %v (loads %d)", got, err, loads)
	}
	refreshedCredential, err := auth.Refresh(context.Background(), Credential{Type: CredentialOAuth, Access: "old"})
	if err != nil || refreshedCredential.Access != "refreshed" || refreshed.Access != "old" {
		t.Fatalf("refresh = %+v, %v, inner saw %+v", refreshedCredential, err, refreshed)
	}
	model, err := auth.ToAuth(Credential{Type: CredentialOAuth, Access: "tok"})
	if err != nil || model.APIKey != "derived" || converted.Access != "tok" {
		t.Fatalf("toAuth = %+v, %v, inner saw %+v", model, err, converted)
	}
	if loads != 1 {
		t.Fatalf("the flow loaded %d times, want once", loads)
	}
}

func TestLazyOAuthLoadFailureStaysFailed(t *testing.T) {
	loads := 0
	failure := errors.New("cannot load")
	auth := LazyOAuth(LazyOAuthInput{Name: "Lazy", Load: func() (*OAuthAuth, error) { loads++; return nil, failure }})
	if _, err := auth.Login(context.Background(), AuthInteraction{}, LoginOptions{}); !errors.Is(err, failure) {
		t.Fatalf("login err = %v", err)
	}
	if _, err := auth.Refresh(context.Background(), Credential{}); !errors.Is(err, failure) {
		t.Fatalf("refresh err = %v", err)
	}
	if _, err := auth.ToAuth(Credential{}); !errors.Is(err, failure) {
		t.Fatalf("toAuth err = %v", err)
	}
	if loads != 1 {
		t.Fatalf("a failed load is not retried: %d loads", loads)
	}
}
