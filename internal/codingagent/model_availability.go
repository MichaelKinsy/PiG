// Ports packages/coding-agent/src/core/model-runtime.ts
package codingagent

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

// SetAvailabilityRefresh binds the Services-owned snapshot reconciler before the registry is shared with consumers. It runs after catalog publication and inside credential synchronization.
func (r *ModelRegistry) SetAvailabilityRefresh(refresh func(context.Context, []string) ai.ModelsRefreshResult) {
	r.availabilityRefresh = refresh
}

// SetAvailabilitySnapshot binds the Services-owned availability snapshot before the registry is shared with consumers. configured reports configured auth for Models-collection providers. Registration and removal call sync with a reader of the catalog's provider order after the catalog commits and before change observers run. A nil provisional check grants no availability before the queued refresh.
// upstream: packages/coding-agent/src/core/model-runtime.ts:registerProvider
func (r *ModelRegistry) SetAvailabilitySnapshot(configured func(providerID string) bool, sync func(providerID string, provisional *ai.AuthCheck, providerOrder func() []string)) {
	r.availabilityAuth, r.registrationSync = configured, sync
}

// syncRegistration completes a committed registration or removal as Pi's registerNativeProvider, registerProvider and unregisterProvider do: it projects the snapshot synchronously, then queues the unawaited local refresh (model-runtime.ts:744-750,768-797).
func (r *ModelRegistry) syncRegistration(providerID string, provisional *ai.AuthCheck) {
	if r.registrationSync != nil {
		r.registrationSync(providerID, provisional, r.modelProviderIDs)
	}
	r.deferRegistrationRefresh(providerID)
}

// registrationAuth is the provisional check a legacy registration publishes until its refresh lands.
// upstream: packages/coding-agent/src/core/model-runtime.ts:registerProvider
func registrationAuth(oauth bool, apiKey string) *ai.AuthCheck {
	kind := ai.CredentialAPIKey
	if oauth && apiKey == "" {
		kind = ai.CredentialOAuth
	}
	return &ai.AuthCheck{Type: kind, Source: "configured provider"}
}

func (r *ModelRegistry) reconcileAvailability(ctx context.Context, providers []string, result ai.ModelsRefreshResult) ai.ModelsRefreshResult {
	if r.availabilityRefresh == nil {
		return result
	}
	availability := r.availabilityRefresh(ctx, providers)
	if result.Errors == nil {
		result.Errors = map[string]error{}
	}
	for _, id := range availability.ErrorOrder {
		if _, exists := result.Errors[id]; !exists {
			result.ErrorOrder = append(result.ErrorOrder, id)
		}
		result.Errors[id] = availability.Errors[id]
	}
	result.Aborted = result.Aborted || availability.Aborted
	return result
}

// ProviderIsSubscription reads the currently composed provider's OAuth capability without reading credentials.
func (r *ModelRegistry) ProviderIsSubscription(providerID string) bool {
	if provider := r.GetProvider(providerID); provider != nil {
		return provider.Auth.OAuth != nil && provider.Auth.OAuth.IsSubscription
	}
	auth, _, _ := r.registryAuthConfig(providerID)
	return auth.OAuth != nil && auth.OAuth.IsSubscription
}

// ListCredentials enumerates the runtime overlay and shared credential storage.
func (r *ModelRegistry) ListCredentials(ctx context.Context) ([]ai.CredentialInfo, error) {
	return (registryCredentials{r}).List(ctx)
}

// ReadCredential reads a provider credential through the runtime overlay.
func (r *ModelRegistry) ReadCredential(ctx context.Context, providerID string) (*ai.Credential, error) {
	return (registryCredentials{r}).Read(ctx, providerID)
}

// GetProviderAuthChecks starts every provider's current auth check, including empty catalogs. The first rejection returns immediately; Services retains and drains the remaining checks.
func (r *ModelRegistry) GetProviderAuthChecks(ctx context.Context) (map[string]*ai.AuthCheck, error) {
	ids := r.modelProviderIDs()
	checks := make([]*ai.AuthCheck, len(ids))
	tasks := make([]func(context.Context) error, len(ids))
	for i, id := range ids {
		tasks[i] = func(ctx context.Context) error {
			var err error
			checks[i], err = r.CheckRegistryAuth(ctx, id)
			return err
		}
	}
	if err := r.AwaitModelTasks(ctx, tasks...); err != nil {
		return nil, err
	}
	result := make(map[string]*ai.AuthCheck, len(ids))
	for i, id := range ids {
		result[id] = checks[i]
	}
	return result, nil
}

// GetAvailableModelDataContext awaits provider auth and account filtering without resolving request-only credentials or constructing backend clients. The first rejection returns immediately while the shared owner drains other providers.
func (r *ModelRegistry) GetAvailableModelDataContext(ctx context.Context, providerID string) ([]*ai.Model, error) {
	ids := r.modelProviderIDs()
	if providerID != "" {
		ids = []string{providerID}
	}
	available := make([][]*ai.Model, len(ids))
	tasks := make([]func(context.Context) error, len(ids))
	for i, id := range ids {
		tasks[i] = func(ctx context.Context) error {
			var err error
			available[i], err = r.availableProviderModels(ctx, id)
			return err
		}
	}
	if err := r.AwaitModelTasks(ctx, tasks...); err != nil {
		return nil, err
	}
	result := make([]*ai.Model, 0)
	for i := range ids {
		result = append(result, available[i]...)
	}
	return result, nil
}

func (r *ModelRegistry) availableProviderModels(ctx context.Context, id string) ([]*ai.Model, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.GetProvider(id) != nil {
		return r.NativeModels().GetAvailable(ctx, id)
	}
	check, err := r.CheckRegistryAuth(ctx, id)
	if err != nil || check == nil {
		return nil, err
	}
	models := r.GetProviderModelData(id)
	filter := r.accountModelFilter(id)
	return slices.DeleteFunc(models, func(model *ai.Model) bool { return !filter(model.ID) }), nil
}
