package coding

// Ports packages/coding-agent/src/core/model-runtime.ts and packages/coding-agent/src/core/model-registry.ts.

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type ProviderConfigInput = icodingagent.ProviderConfigInput
type ExtensionOAuthConfig = icodingagent.ExtensionOAuthConfig

// CredentialSynchronizationError reports a committed credential change whose local model/auth snapshot could not be synchronized.
type CredentialSynchronizationError = icodingagent.CredentialSynchronizationError

type CredentialSynchronizationOperation = icodingagent.CredentialSynchronizationOperation

const (
	CredentialSynchronizationLogin               = icodingagent.CredentialSynchronizationLogin
	CredentialSynchronizationLogout              = icodingagent.CredentialSynchronizationLogout
	CredentialSynchronizationSetRuntimeAPIKey    = icodingagent.CredentialSynchronizationSetRuntimeAPIKey
	CredentialSynchronizationRemoveRuntimeAPIKey = icodingagent.CredentialSynchronizationRemoveRuntimeAPIKey
)

// RegisterNativeProvider installs a provider-owned catalog, auth and API implementation.
func (runtime *ModelRuntime) RegisterNativeProvider(provider *ai.ModelsProvider) error {
	return runtime.services.Registry().RegisterNativeModelsProvider(provider)
}

// RegisterProvider composes legacy provider callbacks with models.json and native registrations.
func (runtime *ModelRuntime) RegisterProvider(id string, input ProviderConfigInput) error {
	return runtime.services.Registry().RegisterProviderInput(id, input, runtime.builtinStream(id))
}

// Refresh first yields to the queued registration refresh (model-runtime.ts:750,788,796), then awaits provider-owned catalog publication and generation-checked availability reconciliation. A full refresh recomposes every provider and so supersedes their in-flight refreshes.
func (runtime *ModelRuntime) Refresh(ctx context.Context, options ...ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	var opts ai.ModelsRefreshOptions
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.AllowNetwork == nil {
		opts.AllowNetwork = new(runtime.modelNetworkEnabled)
	}
	return runtime.services.Registry().RefreshModelRuntime(ctx, opts)
}

// Login awaits same-provider credential operations, authentication by the auth methods of the composed provider GetProvider returns for the ID, and local synchronization. Optional LoginOptions reach the provider's OAuth login, as Pi's login(providerId, type, interaction, options) does (model-runtime.ts:817-825). A committed login whose synchronization fails returns CredentialSynchronizationError.
func (runtime *ModelRuntime) Login(ctx context.Context, id string, kind ai.AuthType, interaction ai.AuthInteraction, options ...ai.LoginOptions) (ai.Credential, error) {
	return runtime.services.Registry().LoginNativeProvider(ctx, id, kind, interaction, options...)
}

// Logout awaits same-provider credential operations before deleting credentials and synchronizing local models. A committed deletion whose synchronization fails returns CredentialSynchronizationError.
func (runtime *ModelRuntime) Logout(ctx context.Context, id string) error {
	return runtime.services.Registry().LogoutNativeProvider(ctx, id)
}

// StreamDeferred forwards the selected model, handle and optional request overrides through the same native model collection as FetchDeferred.
func (runtime *ModelRuntime) StreamDeferred(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ...ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
	return runtime.services.Registry().NativeModels().StreamDeferred(ctx, model, handle, options...)
}

func (runtime *ModelRuntime) FetchDeferred(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ...ai.DeferredFetchOptions) *ai.AssistantMessage {
	return runtime.services.Registry().NativeModels().FetchDeferred(ctx, model, handle, options...)
}
func (runtime *ModelRuntime) CancelDeferred(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ...ai.DeferredCancelOptions) error {
	return runtime.services.Registry().NativeModels().CancelDeferred(ctx, model, handle, options...)
}

// Find returns an exact runtime model, including native provider registrations.
func (registry *ModelRegistry) Find(provider, id string) *ai.Model {
	return registry.runtime.GetModel(provider, id)
}

func (runtime *ModelRuntime) bindNativeModel(model *ai.Model) *ai.Model {
	if model == nil {
		return nil
	}
	bound := new(*model)
	bound.Provider = &nativeModelBackend{runtime: runtime, model: model}
	return bound
}

type nativeModelBackend struct {
	runtime *ModelRuntime
	model   *ai.Model
}

func (provider *nativeModelBackend) ID() string { return provider.model.ProviderMeta.ProviderID }
func (*nativeModelBackend) Close() error        { return nil }
func (provider *nativeModelBackend) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return provider.runtime.services.Registry().NativeModels().StreamSimple(ctx, provider.model, ai.Context{Messages: transcript.Messages()}, options), nil
}
