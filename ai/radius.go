package ai

// Mirrors upstream .upstream/current/packages/ai/src/providers/radius.ts.

import (
	"context"
	"encoding/json"
	"maps"
	"sync"
	"time"
)

// RadiusProviderID is the built-in Radius provider ID.
const RadiusProviderID = "radius"

// RadiusProviderOptions configures NewRadiusProvider. Empty fields default to
// the built-in "radius" provider on DefaultRadiusGateway.
type RadiusProviderOptions struct {
	ID      string
	Name    string
	Gateway string
}

// RadiusProvider is the Radius gateway provider: the published static catalog
// for the default gateway, overlaid by a persisted, dynamically refreshed one.
type RadiusProvider struct {
	id       string
	name     string
	gateway  string
	baseline []PiMessagesModel
	oauth    *RadiusOAuth

	mu      sync.RWMutex
	dynamic []PiMessagesModel
}

// ModelsPublication is one refresh result handed to RefreshModelsContext.Publish.
// Persist, when non-nil, is written to the models store before Update runs.
type ModelsPublication struct {
	Persist *ModelsStoreEntry
	Update  func()
}

// RefreshModelsContext mirrors upstream RefreshModelsContext. Publish reports
// false when the refresh was superseded or cancelled; the provider then stops.
type RefreshModelsContext struct {
	Credential   *Credential
	Stored       *ModelsStoreEntry
	AllowNetwork bool
	Publish      func(ModelsPublication) (bool, error)
}

// NewRadiusProvider mirrors upstream radiusProvider.
func NewRadiusProvider(options RadiusProviderOptions) *RadiusProvider {
	id, name, gateway := options.ID, options.Name, options.Gateway
	if id == "" {
		id = RadiusProviderID
	}
	if name == "" {
		name = "Radius"
	}
	if gateway == "" {
		gateway = DefaultRadiusGateway
	}
	gateway = NormalizeRadiusGatewayURL(gateway)
	provider := &RadiusProvider{
		id:      id,
		name:    name,
		gateway: gateway,
		oauth:   CreateRadiusOAuth(RadiusOAuthOptions{ID: id, Name: name, Gateway: gateway}),
		dynamic: []PiMessagesModel{},
	}
	if gateway == NormalizeRadiusGatewayURL(DefaultRadiusGateway) {
		provider.baseline = publishedRadiusModels(id)
	}
	return provider
}

// publishedRadiusModels is RADIUS_MODELS (the generated radius shard) bound to id.
func publishedRadiusModels(id string) []PiMessagesModel {
	var models []PiMessagesModel
	for _, generated := range GeneratedModels {
		if generated.Provider != RadiusProviderID {
			continue
		}
		models = append(models, PiMessagesModel{
			RadiusGatewayModel: RadiusGatewayModel{
				ID: generated.ID, Name: generated.DisplayName, Reasoning: generated.Reasoning,
				ThinkingLevelMap: cloneThinkingLevelMap(generated.ThinkingLevelMap),
				Input:            append([]string(nil), generated.Capabilities...),
				InputLimits:      generated.InputLimits.Clone(),
				Cost: RadiusModelCost{
					Input: generated.InputCostPerMTokens, Output: generated.OutputCostPerMTokens,
					CacheRead: generated.CacheReadCost, CacheWrite: generated.CacheWriteCost,
					Tiers: append([]CostTier(nil), generated.Tiers...),
				},
				PromptCache:    maps.Clone(generated.PromptCache),
				ContextWindow:  generated.ContextWindow,
				MaxTokens:      generated.MaxOutputTokens,
				SamplingParams: maps.Clone(generated.SamplingParams),
				Headers:        cloneStringMap(generated.Headers),
				Compat:         cloneCompat(generated.Compat),
			},
			API: generated.API, Provider: id, BaseURL: generated.BaseURL,
		})
	}
	return models
}

func (p *RadiusProvider) ID() string          { return p.id }
func (p *RadiusProvider) Name() string        { return p.name }
func (p *RadiusProvider) Gateway() string     { return p.gateway }
func (p *RadiusProvider) OAuth() *RadiusOAuth { return p.oauth }

// GetModels returns the static catalog with refreshed models overlaid by ID.
func (p *RadiusProvider) GetModels() []PiMessagesModel {
	p.mu.RLock()
	defer p.mu.RUnlock()
	merged := make([]PiMessagesModel, 0, len(p.baseline)+len(p.dynamic))
	index := make(map[string]int, cap(merged))
	for _, model := range append(append([]PiMessagesModel(nil), p.baseline...), p.dynamic...) {
		model.RadiusGatewayModel = cloneRadiusGatewayModel(model.RadiusGatewayModel)
		if position, exists := index[model.ID]; exists {
			merged[position] = model
			continue
		}
		index[model.ID] = len(merged)
		merged = append(merged, model)
	}
	return merged
}

// FindModel returns the effective model with id.
func (p *RadiusProvider) FindModel(id string) (PiMessagesModel, bool) {
	for _, model := range p.GetModels() {
		if model.ID == id {
			return model, true
		}
	}
	return PiMessagesModel{}, false
}

func (p *RadiusProvider) setDynamic(models []PiMessagesModel) func() {
	return func() {
		p.mu.Lock()
		p.dynamic = models
		p.mu.Unlock()
	}
}

// RefreshModels restores the stored catalog, imports a legacy credential
// catalog, and, when network access is allowed, loads the gateway's current
// catalog with the effective credential.
func (p *RadiusProvider) RefreshModels(ctx context.Context, refresh RefreshModelsContext) error {
	if refresh.Stored != nil {
		if ok, err := refresh.Publish(ModelsPublication{Update: p.setDynamic(p.storedModels(*refresh.Stored))}); !ok || err != nil {
			return err
		}
	}
	// Import catalogs cached by the pre-ModelsStore Radius implementation.
	if refresh.Stored == nil && refresh.Credential != nil && refresh.Credential.Type == CredentialOAuth {
		if legacy := GetRadiusModels(p.id, refresh.Credential); len(legacy) > 0 {
			if ok, err := refresh.Publish(ModelsPublication{Persist: radiusStoreEntry(legacy), Update: p.setDynamic(legacy)}); !ok || err != nil {
				return err
			}
		}
	}
	if !refresh.AllowNetwork || ctx.Err() != nil {
		return nil
	}
	config, err := LoadRadiusGatewayConfig(ctx, p.gateway, radiusCredentialKey(refresh.Credential))
	if err != nil || ctx.Err() != nil {
		return err
	}
	refreshed := GetRadiusModelsFromConfig(p.id, config)
	_, err = refresh.Publish(ModelsPublication{Persist: radiusStoreEntry(refreshed), Update: p.setDynamic(refreshed)})
	return err
}

func radiusCredentialKey(credential *Credential) string {
	if credential == nil {
		return ""
	}
	if credential.Type == CredentialOAuth {
		return credential.Access
	}
	return credential.Key
}

// storedModels keeps the stored models that belong to this provider.
func (p *RadiusProvider) storedModels(entry ModelsStoreEntry) []PiMessagesModel {
	restored := []PiMessagesModel{}
	for _, raw := range entry.Models {
		var model PiMessagesModel
		if json.Unmarshal(raw, &model) == nil && model.Provider == p.id {
			restored = append(restored, model)
		}
	}
	return restored
}

func radiusStoreEntry(models []PiMessagesModel) *ModelsStoreEntry {
	checkedAt := float64(time.Now().UnixMilli())
	entry := &ModelsStoreEntry{Models: make([]json.RawMessage, 0, len(models)), CheckedAt: &checkedAt}
	for _, model := range models {
		if raw, err := json.Marshal(model); err == nil {
			entry.Models = append(entry.Models, raw)
		}
	}
	return entry
}
