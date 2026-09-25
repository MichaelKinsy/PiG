package llama

import (
	"context"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// providerRegistry is the model-registry surface the provider publishes into.
type providerRegistry interface {
	SetProvider(name string, config extension.ProviderConfig)
}

// Host binds the llama.cpp provider to PiG's model registry, credential
// store, and models store. It ports the parts of Pi's model runtime
// (packages/ai/src/models.ts refresh, publishProviderModels, and getAuth) that
// a registered native provider relies on, for this one provider.
type Host struct {
	controller  *LlamaProviderController
	registry    providerRegistry
	credentials *ai.AuthStorage
	store       *ai.FileModelsStore
	authContext AuthContext
	// huggingFaceURL overrides https://huggingface.co in tests.
	huggingFaceURL string

	mu         sync.Mutex
	generation int
	cancel     context.CancelFunc
	publishMu  sync.Mutex
}

// NewHost creates the llama.cpp provider and registers it, mirroring the
// built-in extension's pi.registerProvider call at startup.
func NewHost(registry providerRegistry, credentials *ai.AuthStorage, store *ai.FileModelsStore) *Host {
	host := &Host{
		controller:  CreateLlamaProvider(),
		registry:    registry,
		credentials: credentials,
		store:       store,
		authContext: DefaultAuthContext(),
	}
	host.SyncRegistration(context.Background())
	return host
}

// Provider returns the registered provider.
func (h *Host) Provider() *Provider { return h.controller.Provider }

// SyncRegistration republishes the provider's catalog and resolved auth. An
// unresolved provider keeps no API key, so the registry reports it as not
// configured and hides its models.
func (h *Host) SyncRegistration(ctx context.Context) {
	provider := h.controller.Provider
	config := extension.ProviderConfig{Name: provider.Name, BaseURL: provider.BaseURL, API: ai.API(provider.API)}
	result, _ := h.GetProviderAuth(ctx)
	if result != nil {
		config.APIKey = literalConfigValue(result.Auth.APIKey)
		config.BaseURL = result.Auth.BaseURL
	}
	config.Models = []extension.ProviderModelConfig{}
	for _, model := range provider.GetModels() {
		entry := extension.ProviderModelConfig{
			ID:            model.ID,
			Name:          model.Name,
			API:           ai.API(model.API),
			Reasoning:     model.Reasoning,
			Input:         model.Input,
			Cost:          extension.ProviderModelCost(model.Cost),
			ContextWindow: model.ContextWindow,
			MaxTokens:     model.MaxTokens,
			Compat:        model.Compat,
		}
		// Request auth's baseUrl replaces the model's own, as in pi-ai
		// Models.stream; without resolved auth the model keeps its URL.
		if result == nil {
			entry.BaseURL = model.BaseURL
		}
		if model.ThinkingLevelMap != nil {
			entry.ThinkingLevelMap = model.ThinkingLevelMap.levels()
		}
		config.Models = append(config.Models, entry)
	}
	h.registry.SetProvider(LlamaProviderID, config)
}

func (m *ThinkingLevelMap) levels() ai.ThinkingLevelMap {
	return ai.ThinkingLevelMap{
		"off": m.Off, "minimal": m.Minimal, "low": m.Low,
		"medium": m.Medium, "high": m.High, "xhigh": m.XHigh,
	}
}

// literalConfigValue escapes a resolved key so the registry's config-value
// resolution returns it unchanged instead of expanding "$" or running "!".
func literalConfigValue(value string) string {
	value = strings.ReplaceAll(value, "$", "$$")
	if strings.HasPrefix(value, "!") {
		value = "$" + value
	}
	return value
}

func (h *Host) storedCredential() (*ai.Credential, error) {
	credential, ok, err := h.credentials.Get(LlamaProviderID)
	if err != nil || !ok {
		return nil, err
	}
	return &credential, nil
}

// GetProviderAuth mirrors ModelRegistry.getProviderAuth for llama.cpp: the
// stored api-key credential resolved with the ambient environment.
func (h *Host) GetProviderAuth(ctx context.Context) (*AuthResult, error) {
	credential, err := h.storedCredential()
	if err != nil {
		return nil, err
	}
	if credential != nil && credential.Type != ai.CredentialAPIKey {
		credential = nil
	}
	return h.controller.Provider.APIKey.Resolve(ctx, h.authContext, credential)
}

// CheckAuth mirrors the side-effect-free availability check.
func (h *Host) CheckAuth(ctx context.Context) (*AuthCheck, error) {
	credential, err := h.storedCredential()
	if err != nil {
		return nil, err
	}
	if credential != nil && credential.Type != ai.CredentialAPIKey {
		credential = nil
	}
	return h.controller.Provider.APIKey.Check(ctx, h.authContext, credential)
}

// Login runs the provider's api-key login and stores the credential,
// mirroring ModelRuntime.login for an api-key method.
func (h *Host) Login(interaction AuthInteraction) error {
	credential, err := h.controller.Provider.APIKey.Login(interaction)
	if err != nil {
		return err
	}
	if err := h.credentials.Set(LlamaProviderID, credential); err != nil {
		return err
	}
	h.SyncRegistration(interaction.Ctx)
	return nil
}

// RefreshResult mirrors ModelsRefreshResult for this provider.
type RefreshResult struct {
	Aborted bool
	Err     error
}

// Refresh mirrors Models.refresh for the llama.cpp provider: a cache-only
// phase restores the stored catalog, then, when allowNetwork is set, a
// network phase runs with the resolved credential. A newer refresh supersedes
// an older one, whose publications are then dropped.
func (h *Host) Refresh(ctx context.Context, allowNetwork bool) RefreshResult {
	if ctx.Err() != nil {
		return RefreshResult{Aborted: true}
	}
	generation, providerCtx := h.beginRefresh(ctx)
	defer h.endRefresh(generation)
	err := h.runRefresh(providerCtx, generation, allowNetwork)
	if providerCtx.Err() != nil {
		err = nil
	}
	return RefreshResult{Aborted: ctx.Err() != nil, Err: err}
}

func (h *Host) runRefresh(ctx context.Context, generation int, allowNetwork bool) error {
	stored, credentialErr := h.storedCredential()
	// Restore cached provider state before auth resolution or network access.
	if err := h.runRefreshPhase(ctx, generation, stored, false); err != nil {
		return err
	}
	if credentialErr != nil {
		return credentialErr
	}
	if !allowNetwork || ctx.Err() != nil {
		return nil
	}
	credential, err := h.resolveRefreshCredential(ctx, stored)
	if err != nil || credential == nil {
		return err
	}
	return h.runRefreshPhase(ctx, generation, credential, true)
}

func (h *Host) resolveRefreshCredential(ctx context.Context, stored *ai.Credential) (*ai.Credential, error) {
	if stored != nil && stored.Type != ai.CredentialAPIKey {
		return nil, nil
	}
	result, err := h.controller.Provider.APIKey.Resolve(ctx, h.authContext, stored)
	if err != nil || result == nil {
		return nil, err
	}
	return &ai.Credential{Type: ai.CredentialAPIKey, Key: result.Auth.APIKey, Env: result.Env}, nil
}

func (h *Host) runRefreshPhase(ctx context.Context, generation int, credential *ai.Credential, allowNetwork bool) error {
	stored, err := h.store.Read(ctx, LlamaProviderID)
	if err != nil {
		return err
	}
	return h.controller.Provider.RefreshModels(RefreshModelsContext{
		Ctx:          ctx,
		Credential:   credential,
		Stored:       stored,
		Publish:      func(publication ModelsPublication) (bool, error) { return h.publish(ctx, generation, publication) },
		AllowNetwork: allowNetwork,
	})
}

func (h *Host) beginRefresh(ctx context.Context) (int, context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel != nil {
		h.cancel()
	}
	h.generation++
	providerCtx, cancel := context.WithCancel(ctx)
	h.cancel = cancel
	return h.generation, providerCtx
}

func (h *Host) endRefresh(generation int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.generation == generation && h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
}

func (h *Host) current(ctx context.Context, generation int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return ctx.Err() == nil && h.generation == generation
}

// publish mirrors publishProviderModels: publications are serialized, and a
// superseded or aborted refresh neither persists nor updates.
func (h *Host) publish(ctx context.Context, generation int, publication ModelsPublication) (bool, error) {
	h.publishMu.Lock()
	defer h.publishMu.Unlock()
	if !h.current(ctx, generation) {
		return false, nil
	}
	if publication.Persist != nil {
		if err := h.store.Write(ctx, LlamaProviderID, *publication.Persist); err != nil {
			return false, err
		}
	}
	if !h.current(ctx, generation) {
		return false, nil
	}
	if publication.Update != nil {
		publication.Update()
	}
	h.SyncRegistration(ctx)
	return true, nil
}
