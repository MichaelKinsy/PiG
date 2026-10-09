package ai

// Mirrors upstream .upstream/current/packages/ai/src/providers/radius.ts.

import (
	"context"
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

// RadiusProvider is the Go handle of a Radius provider: the Pi provider object radiusProvider returns, with the gateway and the OAuth flow the object's auth was built from.
type RadiusProvider struct {
	provider *ModelsProvider
	gateway  string
	oauth    *RadiusOAuth
}

// NewRadiusProvider ports radius.ts radiusProvider(options = {}) (radius.ts:22): the Radius gateway Provider<"pi-messages"> with a persisted, dynamically refreshed catalog. options is optional, and no argument is the empty options. It serves the published static catalog for the default gateway until the account's gateway catalog is known, which then replaces it (organization owners can disable models, so the shipped baseline only covers the time before any catalog exists).
func NewRadiusProvider(radiusOptions ...RadiusProviderOptions) *ModelsProvider {
	var options RadiusProviderOptions
	if len(radiusOptions) > 0 {
		options = radiusOptions[0]
	}
	return newRadiusParts(options).provider
}

// NewRadiusGatewayProvider is NewRadiusProvider with the gateway and OAuth flow its auth was built from, for callers that configure Radius gateways from models.json.
func NewRadiusGatewayProvider(options RadiusProviderOptions) *RadiusProvider {
	return newRadiusParts(options)
}

func newRadiusParts(options RadiusProviderOptions) *RadiusProvider {
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
	oauth := CreateRadiusOAuth(RadiusOAuthOptions{ID: id, Name: name, Gateway: gateway})
	var baseline []*Model
	if gateway == NormalizeRadiusGatewayURL(DefaultRadiusGateway) {
		baseline = publishedRadiusModels(id)
	}
	var mu sync.RWMutex
	// dynamic is the account's catalog; nil until one is known, and empty when the organization disabled every model.
	var dynamic []*Model
	setDynamic := func(models []*Model) func() {
		if models == nil {
			models = []*Model{} // an empty catalog is still a known one
		}
		return func() {
			mu.Lock()
			dynamic = models
			mu.Unlock()
		}
	}
	streams := PiMessagesAPI()
	provider := &ModelsProvider{
		ID:   id,
		Name: name,
		Auth: ProviderAuth{
			APIKey: EnvAPIKeyAuth(builtinAPIKeyNames[RadiusProviderID], getAPIKeyEnvVars(RadiusProviderID)...),
			OAuth: &OAuthAuth{
				Name:    name,
				Refresh: oauthRefresh(oauth),
				ToAuth:  oauthToAuth(id, oauth),
			},
		},
		GetModels: func() ([]*Model, error) {
			mu.RLock()
			defer mu.RUnlock()
			source := baseline
			if dynamic != nil {
				source = dynamic
			}
			models := make([]*Model, 0, len(source))
			for _, model := range source {
				models = append(models, cloneRadiusModel(model))
			}
			return models, nil
		},
		Stream:       streams.Stream,
		StreamSimple: streams.StreamSimple,
	}
	provider.RefreshModels = func(refresh RefreshModelsContext) error {
		ctx := refresh.Signal
		if ctx == nil {
			ctx = context.Background()
		}
		if refresh.Stored != nil {
			if ok, err := refresh.Publish(ModelsPublication{Update: setDynamic(storedRadiusModels(id, *refresh.Stored))}); !ok || err != nil {
				return err
			}
		}
		// Import catalogs cached by the pre-ModelsStore Radius implementation.
		if refresh.Stored == nil && refresh.Credential != nil && refresh.Credential.Type == CredentialOAuth {
			if legacy := GetRadiusModels(id, refresh.Credential); len(legacy) > 0 {
				if ok, err := refresh.Publish(ModelsPublication{Persist: radiusStoreEntry(legacy), Update: setDynamic(legacy)}); !ok || err != nil {
					return err
				}
			}
		}
		if !refresh.AllowNetwork || ctx.Err() != nil {
			return nil
		}
		config, err := LoadRadiusGatewayConfig(ctx, gateway, radiusCredentialKey(refresh.Credential))
		if err != nil || ctx.Err() != nil {
			return err
		}
		refreshed := GetRadiusModelsFromConfig(id, config)
		_, err = refresh.Publish(ModelsPublication{Persist: radiusStoreEntry(refreshed), Update: setDynamic(refreshed)})
		return err
	}
	return &RadiusProvider{provider: provider, gateway: gateway, oauth: oauth}
}

// publishedRadiusModels is RADIUS_MODELS (the generated radius shard) bound to id.
func publishedRadiusModels(id string) []*Model {
	var models []*Model
	for _, generated := range GeneratedModels {
		if generated.Provider != RadiusProviderID {
			continue
		}
		model := generated.ToModel()
		model.ProviderMeta.ProviderID = id
		models = append(models, cloneRadiusModel(model))
	}
	return models
}

func (p *RadiusProvider) ID() string          { return p.provider.ID }
func (p *RadiusProvider) Name() string        { return p.provider.Name }
func (p *RadiusProvider) Gateway() string     { return p.gateway }
func (p *RadiusProvider) OAuth() *RadiusOAuth { return p.oauth }

// Auth is the provider's `auth` property (radius.ts:33-40, models.ts Provider.auth): the RADIUS_API_KEY api-key method and the provider's gateway OAuth, for the built-in provider and every models.json "oauth": "radius" gateway.
func (p *RadiusProvider) Auth() ProviderAuth { return p.provider.Auth }

// GetModels returns the account's catalog once it is known and the static baseline before (radius.ts getModels: dynamicModels ?? baselineModels).
func (p *RadiusProvider) GetModels() []*Model {
	models, _ := p.provider.GetModels() // the Radius catalog read cannot fail
	return models
}

// FindModel returns the effective model with id.
func (p *RadiusProvider) FindModel(id string) (*Model, bool) {
	for _, model := range p.GetModels() {
		if model.ID == id {
			return model, true
		}
	}
	return nil, false
}

// RefreshModels restores the stored catalog, imports a legacy credential
// catalog, and, when network access is allowed, loads the gateway's current
// catalog with the effective credential. ctx is the refresh's abort signal.
func (p *RadiusProvider) RefreshModels(ctx context.Context, refresh RefreshModelsContext) error {
	refresh.Signal = ctx
	return p.provider.RefreshModels(refresh)
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

// storedRadiusModels keeps the stored chat models that belong to provider id (radius.ts: stored.models.filter(model.provider === id)).
func storedRadiusModels(id string, entry ModelsStoreEntry) []*Model {
	restored := []*Model{}
	for _, stored := range entry.Models {
		if chat, ok := stored.(*Model); ok && chat.ProviderID() == id {
			restored = append(restored, chat)
		}
	}
	return restored
}

func radiusStoreEntry(models []*Model) *ModelsStoreEntry {
	checkedAt := float64(time.Now().UnixMilli())
	entry := &ModelsStoreEntry{Models: make([]AnyModel, 0, len(models)), CheckedAt: &checkedAt}
	for _, model := range models {
		entry.Models = append(entry.Models, model)
	}
	return entry
}
