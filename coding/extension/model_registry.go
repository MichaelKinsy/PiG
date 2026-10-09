package extension

// Ports packages/coding-agent/src/core/model-registry.ts.

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
)

// ModelRegistry is the synchronous model-registry facade an extension reaches through ctx.modelRegistry (model-registry.ts:48).
// It has every public member of Pi's ModelRegistry; the Session's registry implements it. Pi's asynchronous members block until
// their result is ready and take the request's context. Pi's registerProvider overloads are RegisterProvider (name and
// ProviderConfigInput) and RegisterProviderObject (a Provider).
type ModelRegistry interface {
	// Refresh reloads models.json and provider catalogs; a caller awaits it before synchronous reads (model-registry.ts:56).
	Refresh(ctx context.Context, options ai.ModelsRefreshOptions) ai.ModelsRefreshResult
	// GetError is the current model configuration diagnostic, or "" (model-registry.ts:60).
	GetError() string
	// GetAll is every known chat model (model-registry.ts:64).
	GetAll() []*ai.Model
	// GetAvailable is the chat models whose provider has configured auth (model-registry.ts:68).
	GetAvailable() []*ai.Model
	// Find is the chat model with this provider and ID, or nil (model-registry.ts:72).
	Find(provider, modelID string) *ai.Model
	// FindOfType is the model of a non-chat type, or nil (model-registry.ts:77).
	FindOfType(modelType ai.ModelType, provider, modelID string) ai.AnyModel
	// HasConfiguredAuth reports whether the model's provider has configured auth (model-registry.ts:85).
	HasConfiguredAuth(model *ai.Model) bool
	// GetAPIKeyAndHeaders resolves the model's request credentials; it never fails, reporting a failure in the result (model-registry.ts:89).
	GetAPIKeyAndHeaders(ctx context.Context, model *ai.Model) ResolvedRequestAuth
	// GetProviderAuthStatus is how the provider is authenticated (model-registry.ts:120).
	GetProviderAuthStatus(provider string) ai.AuthStatus
	// GetProvider is the composed provider, or nil (model-registry.ts:124).
	GetProvider(provider string) *ai.ModelsProvider
	// Stream streams through the configured provider with request-time authentication (model-registry.ts:129).
	Stream(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessageEventStream
	// StreamSimple streams with provider-neutral options and request-time authentication (model-registry.ts:138).
	StreamSimple(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessageEventStream
	// Complete is Stream's final message (model-registry.ts:142).
	Complete(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessage
	// GetModelsOfType is every known model of a type, optionally for one provider (model-registry.ts:151).
	GetModelsOfType(modelType ai.ModelType, provider ...string) []ai.AnyModel
	// GetAvailableOfType is the models of a type whose provider has working credentials (model-registry.ts:156).
	GetAvailableOfType(ctx context.Context, modelType ai.ModelType, provider ...string) ([]ai.AnyModel, error)
	// GetModelOfType is the model of a type with this provider and ID, or nil (model-registry.ts:164).
	GetModelOfType(modelType ai.ModelType, provider, modelID string) ai.AnyModel
	// Classify classifies structured state with request-time authentication; it never fails (model-registry.ts:173).
	Classify(ctx context.Context, model *ai.ClassifierModel, request ai.ClassifierContext, options ...ai.ModelsClassifierOptions) ai.ClassifierResult
	// GenerateImages generates images with request-time authentication; it never fails (model-registry.ts:182).
	GenerateImages(ctx context.Context, model *ai.ImageModel, request ai.ImagesContext, options ...ai.ModelsImagesOptions) ai.AssistantImages
	// GetProviderDisplayName is the provider's name, or its ID when it has none (model-registry.ts:190).
	GetProviderDisplayName(provider string) string
	// GetProviderAuth resolves the provider's authentication (model-registry.ts:194).
	GetProviderAuth(ctx context.Context, provider string) (*ai.AuthResult, error)
	// GetAPIKeyForProvider is the provider's API key, or nil when authentication is absent or fails (model-registry.ts:198).
	GetAPIKeyForProvider(ctx context.Context, provider string) *string
	// IsUsingOAuth reports whether the model's provider authenticates with OAuth (model-registry.ts:206).
	IsUsingOAuth(model *ai.Model) bool
	// RegisterProvider registers a provider by name (model-registry.ts:211 registerProvider(providerName, config)).
	RegisterProvider(name string, config ProviderConfigInput) error
	// RegisterProviderObject registers a Provider (model-registry.ts:210 registerProvider(provider)).
	RegisterProviderObject(provider *ai.ModelsProvider) error
	// UnregisterProvider removes a registered provider (model-registry.ts:221).
	UnregisterProvider(name string)
	// RegisterVirtualModel registers a virtual model (model-registry.ts:225).
	RegisterVirtualModel(definition VirtualModelDefinition) error
	// UnregisterVirtualModel removes a virtual model (model-registry.ts:229).
	UnregisterVirtualModel(provider, id string)
	// GetRegisteredProviderConfig is the configuration a provider was registered with by name, or nil (model-registry.ts:233).
	GetRegisteredProviderConfig(name string) *ProviderConfigInput
	// GetRegisteredNativeProvider is the Provider registered under the name, or nil (model-registry.ts:237).
	GetRegisteredNativeProvider(name string) *ai.ModelsProvider
	// GetRegisteredProviderIDs lists the registered providers (model-registry.ts:241).
	GetRegisteredProviderIDs() []string
}

// ResolvedRequestAuth is the result of ModelRegistry.GetAPIKeyAndHeaders (model-registry.ts:33): OK with the request credentials,
// or not OK with Error.
type ResolvedRequestAuth struct {
	OK      bool               `json:"ok"`
	APIKey  *string            `json:"apiKey,omitempty"`
	Headers ai.ProviderHeaders `json:"headers,omitempty"`
	BaseURL string             `json:"baseUrl,omitempty"`
	Env     map[string]string  `json:"env,omitempty"`
	Error   string             `json:"error,omitempty"`
}

// ExtensionOAuthConfig is the OAuth configuration of a provider registered with ProviderConfigInput (provider-composer.ts:40); the composer adapts it to native provider auth.
type ExtensionOAuthConfig struct {
	Name           string
	IsSubscription bool
	Login          func(context.Context, ai.OAuthLoginCallbacks) (ai.Credential, error)
	RefreshToken   func(context.Context, ai.Credential) (ai.Credential, error)
	GetAPIKey      func(ai.Credential) string
	ModifyModels   func([]*ai.Model, ai.Credential) []*ai.Model
}

// ProviderConfigInput is the core registration input of ModelRegistry.RegisterProvider (provider-composer.ts:91); Models nil preserves the base catalog and an empty slice replaces it.
type ProviderConfigInput struct {
	Name         string
	BaseURL      string
	APIKey       string
	API          ai.API
	StreamSimple ai.ModelsStreamFunction
	Headers      map[string]string
	AuthHeader   *bool
	OAuth        *ExtensionOAuthConfig
	// Models are chat, image and classifier model definitions; a model without a type is a chat model.
	Models []ai.AnyModel
	// Images and Classifiers are the implementations of the image and classifier models Models declares.
	Images        ai.ProviderImageAPIMap
	Classifiers   ai.ProviderClassifierMap
	RefreshModels func(ai.RefreshModelsContext) ([]ai.AnyModel, error)
	// Insecure skips TLS certificate verification for this provider's endpoint.
	// pig additive (D36): carries ProviderConfig.Insecure through a registration composed in the native collection.
	Insecure bool
}
