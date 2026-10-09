package extension

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
)

// NativeProvider is a session-owned carrier for Pi's native Provider callbacks.
// Metadata is immutable; refresh publishes replacement model snapshots. The host
// owns credentials and persistence, and every callback receives its operation context.
type NativeProvider struct {
	// IsCurrent guards publication when host registrations overlap across connections.
	IsCurrent func() bool
	ID        string
	Name      string
	BaseURL   string
	// Headers is Pi's Provider.headers: the provider's declared header names and values. A null value is a header the provider removes.
	// upstream: packages/ai/src/models.ts:155 (Provider.headers), read by packages/coding-agent/src/core/bug-report.ts:119
	Headers ai.ProviderHeaders
	Models  []ProviderModelConfig
	// Auth is Pi's Provider.auth: the API-key and OAuth methods the provider supports. Registration reads it for the provisional configured auth type.
	// upstream: packages/ai/src/models.ts:164 (Provider.auth), packages/coding-agent/src/core/model-runtime.ts:885-897
	Auth      ai.ProviderAuth
	CheckAuth func(context.Context, *ai.Credential) (*ai.AuthCheck, error)
	// GetModels lists the provider's current chat models, as the provider object reports them now; Models is the list it declared at registration. A nil GetModels leaves the registered list as the current one.
	// upstream: pi-ai Provider.getModels
	GetModels func(context.Context) ([]*ai.Model, error)
	// GetAllModels lists the models of every type and FilterAllModels filters them by credential; nil when the provider object has no such member.
	// upstream: pi-ai Provider.getAllModels, Provider.filterAllModels
	GetAllModels             func(context.Context) ([]ai.AnyModel, error)
	FilterAllModels          func(context.Context, []ai.AnyModel, *ai.Credential) ([]ai.AnyModel, error)
	FilterModels             func(context.Context, []*ai.Model, *ai.Credential) ([]*ai.Model, error)
	ResolveAuth              func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error)
	ResolveRefreshCredential func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error)
	RefreshModels            func(ai.RefreshModelsContext) error
	Stream                   func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error)
	// StreamSimple is the provider's simple stream method; a provider with only Stream serves simple requests through it.
	// upstream: pi-ai Provider.streamSimple
	StreamSimple func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error)
	// FetchDeferred and CancelDeferred are the provider object's deferred-response operations; nil when it has none.
	// upstream: pi-ai Provider.fetchDeferred, Provider.cancelDeferred
	FetchDeferred  func(context.Context, *ai.Model, ai.DeferredHandle, ai.DeferredFetchOptions) (*ai.AssistantMessageEventStream, error)
	CancelDeferred func(context.Context, *ai.Model, ai.DeferredHandle, ai.DeferredCancelOptions) error
	// GenerateImages and Classify are the provider object's image and classifier operations; nil when it has none.
	// upstream: pi-ai Provider.generateImages, Provider.classify
	GenerateImages func(context.Context, *ai.ImageModel, ai.ImagesContext, ai.ImagesOptions) (ai.AssistantImages, error)
	Classify       func(context.Context, *ai.ClassifierModel, ai.ClassifierContext, ai.ClassifierOptions) (ai.ClassifierResult, error)
}

type providerStreamMethodKey struct{}

// WithProviderStreamSimple carries the selected Pi stream method through the host request.
func WithProviderStreamSimple(ctx context.Context, simple bool) context.Context {
	return context.WithValue(ctx, providerStreamMethodKey{}, simple)
}
func ProviderStreamSimpleSelected(ctx context.Context) bool {
	value, _ := ctx.Value(providerStreamMethodKey{}).(bool)
	return value
}

// NativeProviderOf is the registration carrier of a provider object a compiled-in extension built: every member the object has is the
// carrier's member of the same name, so the registry sees the object as it would see a subprocess extension's provider.
// upstream: packages/coding-agent/src/core/extensions/types.ts registerProvider(name, provider: Provider)
func NativeProviderOf(provider *ai.ModelsProvider) *NativeProvider {
	carrier := &NativeProvider{ID: provider.ID, Name: provider.Name, BaseURL: provider.BaseURL, Headers: provider.Headers, Auth: provider.Auth}
	if provider.GetModels != nil {
		carrier.GetModels = func(context.Context) ([]*ai.Model, error) { return provider.GetModels() }
	}
	if provider.GetAllModels != nil {
		carrier.GetAllModels = func(context.Context) ([]ai.AnyModel, error) { return provider.GetAllModels() }
	}
	if provider.FilterAllModels != nil {
		carrier.FilterAllModels = func(_ context.Context, models []ai.AnyModel, credential *ai.Credential) ([]ai.AnyModel, error) {
			return provider.FilterAllModels(models, credential), nil
		}
	}
	if provider.FilterModels != nil {
		carrier.FilterModels = func(_ context.Context, models []*ai.Model, credential *ai.Credential) ([]*ai.Model, error) {
			return provider.FilterModels(models, credential), nil
		}
	}
	carrier.RefreshModels = provider.RefreshModels
	carrier.Stream = provider.Stream
	carrier.StreamSimple = provider.StreamSimple
	carrier.FetchDeferred = provider.FetchDeferred
	carrier.CancelDeferred = provider.CancelDeferred
	carrier.GenerateImages = provider.GenerateImages
	carrier.Classify = provider.Classify
	return carrier
}

// ProviderObject is the Provider object a carrier made by [NativeProviderOf] was built from, assembled again from the carrier's members. A
// registry registers a compiled-in extension's provider as that object: it has no host callbacks, only the object's own auth, model and
// stream members.
// upstream: packages/coding-agent/src/core/model-runtime.ts registerNativeProvider(provider: Provider)
func (p *NativeProvider) ProviderObject() *ai.ModelsProvider {
	object := &ai.ModelsProvider{
		ID: p.ID, Name: p.Name, BaseURL: p.BaseURL, Headers: p.Headers, Auth: p.Auth, RefreshModels: p.RefreshModels,
		Stream: p.Stream, StreamSimple: p.StreamSimple, FetchDeferred: p.FetchDeferred, CancelDeferred: p.CancelDeferred,
		GenerateImages: p.GenerateImages, Classify: p.Classify,
	}
	if p.GetModels != nil {
		object.GetModels = func() ([]*ai.Model, error) { return p.GetModels(context.Background()) }
	}
	if p.GetAllModels != nil {
		object.GetAllModels = func() ([]ai.AnyModel, error) { return p.GetAllModels(context.Background()) }
	}
	if p.FilterModels != nil {
		object.FilterModels = func(models []*ai.Model, credential *ai.Credential) []*ai.Model {
			filtered, err := p.FilterModels(context.Background(), models, credential)
			if err != nil {
				return nil
			}
			return filtered
		}
	}
	if p.FilterAllModels != nil {
		object.FilterAllModels = func(models []ai.AnyModel, credential *ai.Credential) []ai.AnyModel {
			filtered, err := p.FilterAllModels(context.Background(), models, credential)
			if err != nil {
				return nil
			}
			return filtered
		}
	}
	return object
}
