package cli

// Ports packages/coding-agent/src/cli/auth-logout.ts

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// AuthLogoutRuntime is the part of the model runtime `pig auth logout` uses.
type AuthLogoutRuntime interface {
	GetProviders() []*ai.ModelsProvider
	ListCredentials(ctx context.Context) ([]ai.CredentialInfo, error)
	Logout(ctx context.Context, id string) error
}

// AuthLogoutOptions are the dependencies of RunAuthLogout, injected so tests can capture the output.
type AuthLogoutOptions struct {
	ModelRuntime AuthLogoutRuntime
	Terminal     AuthOutput
}

var authLogoutUsage = codingagent.AppName + " auth logout <provider>"

// providerIDName is the ID and display name FindMatchingProviders matches a provider by.
func providerIDName(provider *ai.ModelsProvider) (string, string) { return provider.ID, provider.Name }

// RunAuthLogout runs `pig auth logout <provider>`: it removes the credential `pig auth login` or /login stored, and returns the exit code. Environment
// variables and models.json config stay, as with /logout.
func RunAuthLogout(ctx context.Context, args []string, options AuthLogoutOptions) int {
	modelRuntime, terminal := options.ModelRuntime, options.Terminal
	if len(args) == 0 || strings.HasPrefix(args[0], "-") || len(args) > 1 {
		terminal.Error("Usage: " + authLogoutUsage)
		return 1
	}
	providerRef := args[0]

	stored, err := modelRuntime.ListCredentials(ctx)
	if err != nil {
		terminal.Error("Could not read stored credentials: " + err.Error())
		return 1
	}
	var provider *ai.ModelsProvider
	if matches := codingagent.FindMatchingProviders(modelRuntime.GetProviders(), providerRef, providerIDName); len(matches) > 0 {
		provider = matches[0]
	}
	// Providers registered by extensions are not loaded here, so their stored credentials match by id alone.
	storedID := providerRef
	if provider != nil {
		storedID = provider.ID
	}
	credentialIndex := slices.IndexFunc(stored, func(candidate ai.CredentialInfo) bool { return candidate.ProviderID == storedID })
	if provider == nil && credentialIndex < 0 {
		terminal.Error(`Unknown provider "` + widthx.ReplaceControlCharacters(providerRef) + `".`)
		storedIDs := make([]string, len(stored))
		for index, credential := range stored {
			storedIDs[index] = credential.ProviderID
		}
		slices.SortFunc(storedIDs, func(a, b string) int { return slices.Compare(jsstring.ToUTF16(a), jsstring.ToUTF16(b)) })
		list := strings.Join(storedIDs, ", ")
		if list == "" {
			list = "none"
		}
		terminal.Error("Stored credentials: " + list)
		return 1
	}
	providerName := providerRef
	if provider != nil {
		providerName = provider.Name
	}
	if credentialIndex < 0 {
		terminal.Write("No stored credentials for " + providerName + ". Environment variables and models.json config are unchanged.")
		return 0
	}
	credential := stored[credentialIndex]

	if err := modelRuntime.Logout(ctx, credential.ProviderID); err != nil {
		if _, ok := errors.AsType[*codingagent.CredentialSynchronizationError](err); ok {
			terminal.Error("Credentials removed for " + providerName + ", but local model state could not be synchronized: " + err.Error())
		} else {
			terminal.Error("Logout failed: " + err.Error())
		}
		return 1
	}
	if credential.Type == ai.CredentialOAuth {
		terminal.Write("Logged out of " + providerName + ".")
	} else {
		terminal.Write("Removed stored API key for " + providerName + ". Environment variables and models.json config are unchanged.")
	}
	return 0
}
