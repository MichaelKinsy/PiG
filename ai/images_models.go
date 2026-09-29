package ai

// Ports packages/ai/src/images-models.ts.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"
)

// ProviderImages is an image API module. Rejections are returned as errors.
type ProviderImages struct {
	GenerateImages func(context.Context, ImagesModel, ImagesContext, ImagesOptions) (AssistantImages, error)
}

// ImagesProvider owns image catalog, auth and generation behavior. Callers must not mutate metadata or callbacks concurrently with collection operations.
type ImagesProvider struct {
	ID             string
	Name           string
	Auth           ProviderAuth
	GetModels      func() ([]ImagesModel, error)
	RefreshModels  func() error
	GenerateImages func(context.Context, ImagesModel, ImagesContext, ImagesOptions) (AssistantImages, error)
}

// CreateImagesProviderOptions constructs a static or refreshable image provider.
type CreateImagesProviderOptions struct {
	ID            string
	Name          *string
	Auth          ProviderAuth
	Models        []ImagesModel
	RefreshModels func() ([]ImagesModel, error)
	API           ProviderImages
}

type imagesRefresh struct {
	done chan struct{}
	err  error
}

// CreateImagesProvider coalesces concurrent refreshes and publishes a catalog only after a successful fetch.
func CreateImagesProvider(input CreateImagesProviderOptions) *ImagesProvider {
	name := input.ID
	if input.Name != nil {
		name = *input.Name
	}
	var mu sync.Mutex
	models := append([]ImagesModel{}, input.Models...)
	var inflight *imagesRefresh
	provider := &ImagesProvider{ID: input.ID, Name: name, Auth: input.Auth,
		GetModels: func() ([]ImagesModel, error) {
			mu.Lock()
			defer mu.Unlock()
			return append([]ImagesModel{}, models...), nil
		},
		GenerateImages: input.API.GenerateImages,
	}
	if input.RefreshModels != nil {
		provider.RefreshModels = func() error {
			mu.Lock()
			if pending := inflight; pending != nil {
				mu.Unlock()
				<-pending.done
				return pending.err
			}
			pending := &imagesRefresh{done: make(chan struct{})}
			inflight = pending
			mu.Unlock()
			next, err := input.RefreshModels()
			mu.Lock()
			if err == nil {
				models = append([]ImagesModel{}, next...)
			}
			pending.err = err
			inflight = nil
			close(pending.done)
			mu.Unlock()
			return err
		}
	}
	return provider
}

// ImagesModels is an insertion-ordered collection of image providers with request auth resolution.
type ImagesModels struct {
	mu          sync.RWMutex
	providers   map[string]*ImagesProvider
	order       []string
	credentials CredentialStore
	authContext AuthContext
}

// MutableImagesModels is the concrete collection, which also exposes provider mutation methods.
type MutableImagesModels = ImagesModels

// CreateImagesModels creates an empty image provider collection.
func CreateImagesModels(options ...CreateModelsOptions) *ImagesModels {
	var opts CreateModelsOptions
	if len(options) > 0 {
		opts = options[0]
	}
	credentials := opts.Credentials
	if credentials == nil {
		credentials = NewInMemoryCredentialStore()
	}
	authContext := DefaultProviderAuthContext()
	if opts.AuthContext != nil {
		authContext = *opts.AuthContext
	}
	return &ImagesModels{providers: map[string]*ImagesProvider{}, credentials: credentials, authContext: authContext}
}

// SetProvider replaces an existing id without moving its position.
func (m *ImagesModels) SetProvider(provider *ImagesProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.providers[provider.ID]; !exists {
		m.order = append(m.order, provider.ID)
	}
	m.providers[provider.ID] = provider
}

func (m *ImagesModels) DeleteProvider(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.providers, id)
	m.order = slices.DeleteFunc(m.order, func(value string) bool { return value == id })
}

func (m *ImagesModels) ClearProviders() {
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.providers)
	m.order = nil
}

func (m *ImagesModels) GetProviders() []*ImagesProvider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	providers := make([]*ImagesProvider, 0, len(m.order))
	for _, id := range m.order {
		providers = append(providers, m.providers[id])
	}
	return providers
}

func (m *ImagesModels) GetProvider(id string) *ImagesProvider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.providers[id]
}

// GetModels reads a last-known catalog; a failing source contributes no models.
func (m *ImagesModels) GetModels(provider ...string) []ImagesModel {
	var providers []*ImagesProvider
	if len(provider) > 0 {
		providers = []*ImagesProvider{m.GetProvider(provider[0])}
	} else {
		providers = m.GetProviders()
	}
	models := []ImagesModel{}
	for _, entry := range providers {
		if entry == nil || entry.GetModels == nil {
			continue
		}
		list, err := entry.GetModels()
		if err == nil {
			models = append(models, list...)
		}
	}
	return models
}

func (m *ImagesModels) GetModel(provider, id string) *ImagesModel {
	for _, model := range m.GetModels(provider) {
		if model.ID == id {
			return &model
		}
	}
	return nil
}

// Refresh waits for one provider, or for all providers concurrently. All-provider refresh is best-effort and joins every operation.
func (m *ImagesModels) Refresh(provider ...string) error {
	if len(provider) > 0 {
		entry := m.GetProvider(provider[0])
		if entry == nil || entry.RefreshModels == nil {
			return nil
		}
		if err := entry.RefreshModels(); err != nil {
			if _, ok := errors.AsType[*ModelsError](err); ok {
				return err
			}
			return NewModelsError(ModelsErrorModelSource, "Model refresh failed for "+provider[0], err)
		}
		return nil
	}
	var wg sync.WaitGroup
	for _, entry := range m.GetProviders() {
		if entry.RefreshModels != nil {
			wg.Go(func() { _ = entry.RefreshModels() })
		}
	}
	wg.Wait()
	return nil
}

func (m *ImagesModels) GetAuth(ctx context.Context, providerID string, overrides ...AuthResolutionOverrides) (*AuthResult, error) {
	provider := m.GetProvider(providerID)
	if provider == nil {
		return nil, nil
	}
	var opts AuthResolutionOverrides
	if len(overrides) > 0 {
		opts = overrides[0]
	}
	return ResolveProviderAuth(ctx, providerID, provider.Auth, m.credentials, m.authContext, opts)
}

// GetModelAuth is the model overload of upstream getAuth; image models add no intrinsic headers to auth resolution.
func (m *ImagesModels) GetModelAuth(ctx context.Context, model ImagesModel, overrides ...AuthResolutionOverrides) (*AuthResult, error) {
	return m.GetAuth(ctx, string(model.Provider), overrides...)
}

// GenerateImages resolves provider auth, overlays explicit request values and converts rejections to image error results.
func (m *ImagesModels) GenerateImages(ctx context.Context, model ImagesModel, request ImagesContext, options ...ImagesOptions) AssistantImages {
	var opts ImagesOptions
	if len(options) > 0 {
		opts = options[0]
	}
	result, err := m.generateImages(ctx, model, request, opts)
	if err != nil {
		return AssistantImages{API: model.API, Provider: model.Provider, Model: model.ID, Output: []ContentBlock{}, StopReason: ImagesStopReasonError, ErrorMessage: err.Error(), Timestamp: time.Now().UnixMilli()}
	}
	return result
}

func (m *ImagesModels) generateImages(ctx context.Context, model ImagesModel, request ImagesContext, options ImagesOptions) (AssistantImages, error) {
	provider := m.GetProvider(string(model.Provider))
	if provider == nil {
		return AssistantImages{}, NewModelsError(ModelsErrorProvider, "Unknown provider: "+string(model.Provider), nil)
	}
	overrides := AuthResolutionOverrides{Env: options.Env}
	if options.APIKey != "" || options.APIKeySet {
		overrides.APIKey = new(options.APIKey)
	}
	resolution, err := m.GetModelAuth(ctx, model, overrides)
	if err != nil {
		return AssistantImages{}, err
	}
	if resolution != nil {
		if resolution.Auth.BaseURL != "" {
			model.BaseURL = resolution.Auth.BaseURL
		}
		if !options.APIKeySet && options.APIKey == "" {
			options.APIKey = resolution.Auth.APIKey
		}
		if resolution.Auth.Headers != nil || options.Headers != nil {
			headers := make(ProviderHeaders, len(resolution.Auth.Headers)+len(options.Headers))
			maps.Copy(headers, resolution.Auth.Headers)
			maps.Copy(headers, options.Headers)
			options.Headers = headers
		}
		if resolution.Env != nil || options.Env != nil {
			env := make(map[string]string, len(resolution.Env)+len(options.Env))
			maps.Copy(env, resolution.Env)
			maps.Copy(env, options.Env)
			options.Env = env
		}
	}
	if provider.GenerateImages == nil {
		return AssistantImages{}, fmt.Errorf("Image provider %s has no image API", provider.ID)
	}
	return provider.GenerateImages(ctx, model, request, options)
}

// Ports packages/ai/src/providers/openrouter-images.ts.

func OpenrouterImagesProvider() *ImagesProvider {
	oauth, _ := OAuthProviderAuth("openrouter")
	return CreateImagesProvider(CreateImagesProviderOptions{ID: "openrouter", Name: new("OpenRouter"), Auth: ProviderAuth{APIKey: EnvAPIKeyAuth("OpenRouter API key", "OPENROUTER_API_KEY"), OAuth: oauth}, Models: GetImageModels(ProviderImagesOpenRouter), API: ProviderImages{GenerateImages: func(ctx context.Context, model ImagesModel, request ImagesContext, options ImagesOptions) (AssistantImages, error) {
		return GenerateImagesOpenRouter(ctx, model, request, options), nil
	}}})
}

// BuiltinImagesModels registers the built-in image provider catalog with application-owned auth.
func BuiltinImagesModels(options ...CreateModelsOptions) *ImagesModels {
	models := CreateImagesModels(options...)
	models.SetProvider(OpenrouterImagesProvider())
	return models
}
