package coding

// Ports packages/coding-agent/src/core/model-runtime.ts and packages/coding-agent/src/core/model-registry.ts.

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type ProviderConfigInput = icodingagent.ProviderConfigInput
type ExtensionOAuthConfig = icodingagent.ExtensionOAuthConfig

// CredentialSynchronizationError reports a committed credential change whose local model/auth snapshot could not be synchronized.
type CredentialSynchronizationError = icodingagent.CredentialSynchronizationError

// NewCredentialSynchronizationError is `new CredentialSynchronizationError(providerId, operation, credential, { cause })`.
func NewCredentialSynchronizationError(providerID string, operation CredentialSynchronizationOperation, credential *ai.Credential, cause error) *CredentialSynchronizationError {
	return icodingagent.NewCredentialSynchronizationError(providerID, operation, credential, cause)
}

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

// StreamDeferred forwards the selected model, handle and optional request overrides through the same native model collection as FetchDeferred. A model of a provider an extension registered goes to that provider object's fetchDeferred after the same request authentication its stream gets.
// upstream: packages/ai/src/models.ts:StreamDeferred
func (runtime *ModelRuntime) StreamDeferred(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ...ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
	if native := runtime.registeredNativeProvider(model); native != nil {
		var opts ai.DeferredFetchOptions
		if len(options) > 0 {
			opts = options[0]
		}
		return runtime.streamNativeDeferred(ctx, model, native, handle, opts)
	}
	return runtime.services.Registry().NativeModels().StreamDeferred(ctx, model, handle, options...)
}

func (runtime *ModelRuntime) FetchDeferred(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ...ai.DeferredFetchOptions) *ai.AssistantMessage {
	return runtime.StreamDeferred(ctx, model, handle, options...).Result()
}

func (runtime *ModelRuntime) CancelDeferred(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ...ai.DeferredCancelOptions) error {
	native := runtime.registeredNativeProvider(model)
	if native == nil {
		return runtime.services.Registry().NativeModels().CancelDeferred(ctx, model, handle, options...)
	}
	if native.CancelDeferred == nil {
		return ai.NewModelsError(ai.ModelsErrorProvider, "Provider "+native.ID+" does not support deferred responses", nil)
	}
	var opts ai.DeferredCancelOptions
	if len(options) > 0 {
		opts = options[0]
	}
	requestModel, _, prepared, err := runtime.prepareNativeRequest(ctx, model, opts, native)
	if err != nil {
		return err
	}
	return native.CancelDeferred(ctx, requestModel, handle, prepared)
}

// registeredNativeProvider is the provider object an extension registered for the model's provider, or nil.
func (runtime *ModelRuntime) registeredNativeProvider(model *ai.Model) *extension.NativeProvider {
	if model == nil {
		return nil
	}
	return runtime.services.Registry().NativeProvider(model.ProviderMeta.ProviderID)
}

func (runtime *ModelRuntime) streamNativeDeferred(ctx context.Context, model *ai.Model, native *extension.NativeProvider, handle ai.DeferredHandle, opts ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
	outer := ai.NewAssistantMessageEventStream()
	if native.FetchDeferred == nil {
		runtime.fail(ctx, outer, model, ai.NewModelsError(ai.ModelsErrorProvider, "Provider "+native.ID+" does not support deferred responses", nil))
		return outer
	}
	requestModel, _, prepared, err := runtime.prepareNativeRequest(ctx, model, opts.StreamOptions, native)
	if err != nil {
		runtime.fail(ctx, outer, model, err)
		return outer
	}
	opts.StreamOptions = prepared
	if err := ai.RunStreamContinuation(ctx, func(observation *ai.StreamObservation) error {
		ctx := observation.Context(ctx)
		inner, err := native.FetchDeferred(ctx, requestModel, handle, opts)
		if err != nil {
			return err
		}
		if inner == nil {
			return fmt.Errorf("model runtime: provider %q returned a nil stream", native.ID)
		}
		observation.Yield()
		return outer.ForwardStream(ctx, inner)
	}); err != nil {
		runtime.fail(ctx, outer, requestModel, err)
	}
	return outer
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
