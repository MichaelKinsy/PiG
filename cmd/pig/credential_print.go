package main

// Mirrors upstream packages/coding-agent/src/cli/credential-print.ts.

import (
	"context"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

const defaultBearerTokenMinExpiryMs = float64(30 * 60_000)

type printableProvider struct {
	id    string
	model *codingagent.RuntimeModel
}

type printedCredential struct {
	providerID string
	value      string
}

// ResolveCredentialForPrint resolves one configured provider credential for
// `auth print-api-key` or `auth print-bearer-token`. It resolves through the
// normal request-auth path, which refreshes and persists OAuth credentials
// with less than five minutes remaining. An API key is printed only from a
// provider not configured with OAuth; a bearer token only from one that is.
func ResolveCredentialForPrint(ctx context.Context, flags CLIFlags, rawArgs []string, runtime *codingagent.RequestAuthRuntime, kind AuthCommandKind, minExpiryMs *float64) (string, error) {
	target, err := ValidateAuthCommandArgs(flags, rawArgs, kind)
	if err != nil {
		return "", err
	}
	infos, err := runtime.ListCredentials(ctx)
	if err != nil {
		return "", err
	}
	credentialTypes := make(map[string]ai.CredentialType, len(infos))
	for _, info := range infos {
		credentialTypes[info.ProviderID] = info.Type
	}
	providers, err := printableProviders(target, runtime, credentialTypes)
	if err != nil {
		return "", err
	}

	var credentials []printedCredential
	for _, provider := range providers {
		credentialType, stored := credentialTypes[provider.id]
		if kind == AuthCommandAPIKey && stored && credentialType == ai.CredentialOAuth {
			continue
		}
		if kind == AuthCommandBearerToken && (!stored || credentialType != ai.CredentialOAuth) {
			continue
		}
		overrides := ai.AuthResolutionOverrides{}
		if kind == AuthCommandBearerToken {
			minimum := defaultBearerTokenMinExpiryMs
			if minExpiryMs != nil {
				minimum = *minExpiryMs
			}
			overrides.MinOAuthValidityMs = &minimum
		}
		var auth *ai.AuthResult
		if provider.model != nil {
			auth, err = runtime.GetModelAuth(ctx, *provider.model, overrides)
		} else {
			auth, err = runtime.GetAuth(ctx, provider.id, overrides)
		}
		if err != nil {
			return "", err
		}
		if value := GetAuthCredential(auth); value != "" {
			credentials = append(credentials, printedCredential{providerID: provider.id, value: value})
		}
	}

	switch len(credentials) {
	case 1:
		return credentials[0].value, nil
	case 0:
		providerID := ""
		if len(providers) > 0 {
			providerID = providers[0].id
		}
		credentialType := credentialTypes[providerID]
		if target.Provider != "" && kind == AuthCommandAPIKey && credentialType == ai.CredentialOAuth {
			return "", authCommandError(`Provider "%s" is configured with OAuth, not an API key`, providerID)
		}
		if target.Provider != "" && kind == AuthCommandBearerToken && credentialType != ai.CredentialOAuth {
			return "", authCommandError(`Provider "%s" is not configured with an OAuth bearer token`, providerID)
		}
		label := "OAuth bearer token"
		if kind == AuthCommandAPIKey {
			label = "API key"
		}
		return "", authCommandError("No usable %s is configured", label)
	}
	ids := make([]string, len(credentials))
	for index, credential := range credentials {
		ids[index] = credential.providerID
	}
	return "", authCommandError("Multiple configured providers matched (%s). Specify --provider.", strings.Join(ids, ", "))
}

// printableProviders selects the provider named by --provider, or every
// provider with a stored credential whose catalog has the --model model.
func printableProviders(target AuthCommandTarget, runtime *codingagent.RequestAuthRuntime, credentialTypes map[string]ai.CredentialType) ([]printableProvider, error) {
	if target.Provider != "" {
		provider := runtime.GetProvider(target.Provider)
		if provider == nil {
			return nil, authCommandError(`Unknown provider "%s". Use --list-models to see available providers.`, target.Provider)
		}
		if target.Model == "" {
			return []printableProvider{{id: provider.ID}}, nil
		}
		resolved := ResolveCliModel(provider.ID, target.Model, "", runtime)
		if resolved.Error != "" || resolved.Model == nil {
			message := resolved.Error
			if message == "" {
				message = "Unable to resolve the requested provider/model"
			}
			return nil, &AuthCommandError{Message: message}
		}
		return []printableProvider{{id: provider.ID, model: resolved.Model}}, nil
	}
	var providers []printableProvider
	for _, provider := range runtime.GetProviders() {
		if _, stored := credentialTypes[provider.ID]; !stored {
			continue
		}
		resolved := ResolveCliModel(provider.ID, target.Model, "", runtime)
		if resolved.Model != nil && resolved.Error == "" && !strings.Contains(resolved.Warning, "Using custom model id") {
			providers = append(providers, printableProvider{id: provider.ID, model: resolved.Model})
		}
	}
	if len(providers) == 0 {
		return nil, authCommandError(`Model "%s" not found. Use --list-models to see available models.`, target.Model)
	}
	return providers, nil
}

// createCredentialPrintRuntime mirrors the ModelRuntime.create call of
// upstream runAuthCommand for credential printing: file-backed credentials,
// models.json, and a local availability pass.
func createCredentialPrintRuntime(ctx context.Context, agentDir string) (*codingagent.RequestAuthRuntime, error) {
	credentials, err := ai.NewAuthStorage(authJSONPath(agentDir))
	if err != nil {
		return nil, fmt.Errorf("open credentials: %w", err)
	}
	return codingagent.NewRequestAuthRuntime(ctx, codingagent.RequestAuthRuntimeOptions{
		Credentials:     credentials,
		AgentDir:        agentDir,
		RefreshOnCreate: true,
	})
}
