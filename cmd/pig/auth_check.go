package main

// Mirrors upstream packages/coding-agent/src/cli/auth-check.ts.

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// AuthCheckStatus mirrors upstream AuthCheckStatus.
type AuthCheckStatus string

const (
	AuthCheckReady    AuthCheckStatus = "ready"
	AuthCheckNotReady AuthCheckStatus = "not_ready"
	AuthCheckInvalid  AuthCheckStatus = "invalid"
)

// AuthCheckReason mirrors upstream AuthCheckReason.
type AuthCheckReason string

const (
	AuthCheckProviderNotFound         AuthCheckReason = "provider_not_found"
	AuthCheckCredentialsNotConfigured AuthCheckReason = "credentials_not_configured"
	AuthCheckCredentialNotAvailable   AuthCheckReason = "credential_not_available"
	AuthCheckInvalidState             AuthCheckReason = "invalid_state"
)

// AuthCheckResult mirrors upstream AuthCheckResult; JSON field order matches
// the upstream object literals.
type AuthCheckResult struct {
	Status   AuthCheckStatus   `json:"status"`
	Provider string            `json:"provider"`
	Reason   AuthCheckReason   `json:"reason,omitempty"`
	AuthType ai.CredentialType `json:"authType,omitempty"`
}

// CheckProviderAuth mirrors upstream checkProviderAuth.
func CheckProviderAuth(ctx context.Context, flags CLIFlags, rawArgs []string, runtime *codingagent.RequestAuthRuntime, refresh bool) (AuthCheckResult, error) {
	target, err := ValidateAuthCommandArgs(flags, rawArgs, AuthCommandCheck)
	if err != nil {
		return AuthCheckResult{}, err
	}
	provider := target.Provider
	if target.Model != "" {
		resolved := ResolveCliModel(target.Provider, target.Model, "", runtime)
		if resolved.Error != "" || resolved.Model == nil {
			message := resolved.Error
			if message == "" {
				message = `Unable to resolve model "` + target.Model + `"`
			}
			return AuthCheckResult{}, &AuthCommandError{Message: message}
		}
		provider = resolved.Model.Provider
	}
	if provider == "" {
		return AuthCheckResult{}, &AuthCommandError{Message: "Unable to resolve an auth provider"}
	}
	if runtime.GetError() != "" {
		return AuthCheckResult{Status: AuthCheckInvalid, Provider: provider, Reason: AuthCheckInvalidState}, nil
	}
	if runtime.GetProvider(provider) == nil {
		return AuthCheckResult{Status: AuthCheckNotReady, Provider: provider, Reason: AuthCheckProviderNotFound}, nil
	}
	auth, err := runtime.CheckAuth(ctx, provider)
	if err != nil {
		return AuthCheckResult{Status: AuthCheckInvalid, Provider: provider, Reason: AuthCheckInvalidState}, nil
	}
	if auth == nil {
		return AuthCheckResult{Status: AuthCheckNotReady, Provider: provider, Reason: AuthCheckCredentialsNotConfigured}, nil
	}
	if refresh {
		resolved, err := runtime.GetAuth(ctx, provider, ai.AuthResolutionOverrides{})
		if err != nil {
			return AuthCheckResult{Status: AuthCheckInvalid, Provider: provider, Reason: AuthCheckInvalidState}, nil
		}
		if resolved == nil {
			return AuthCheckResult{Status: AuthCheckNotReady, Provider: provider, Reason: AuthCheckCredentialsNotConfigured}, nil
		}
	}
	return AuthCheckResult{Status: AuthCheckReady, Provider: provider, AuthType: auth.Type}, nil
}

// GetProviderCredential mirrors upstream getProviderCredential: without
// refresh a stored OAuth access token is returned as stored.
func GetProviderCredential(ctx context.Context, providerID string, runtime *codingagent.RequestAuthRuntime, credentials ai.CredentialStore, refresh bool) (string, error) {
	credential, err := credentials.Read(ctx, providerID)
	if err != nil {
		return "", err
	}
	if !refresh && credential != nil && credential.Type == ai.CredentialOAuth {
		return credential.Access, nil
	}
	auth, err := runtime.GetAuth(ctx, providerID, ai.AuthResolutionOverrides{})
	if err != nil {
		return "", err
	}
	return GetAuthCredential(auth), nil
}

// CreateAuthCheckModelRuntime mirrors upstream createAuthCheckModelRuntime:
// an in-memory models store, no catalog refresh, and no availability pass.
func CreateAuthCheckModelRuntime(ctx context.Context, credentials ai.CredentialStore, agentDir string) (*codingagent.RequestAuthRuntime, error) {
	return codingagent.NewRequestAuthRuntime(ctx, codingagent.RequestAuthRuntimeOptions{
		Credentials: credentials,
		AgentDir:    agentDir,
		ModelsStore: ai.NewInMemoryModelsStore(),
	})
}
