package codingagent

// Request-auth resolution surface of upstream ModelRuntime
// (packages/coding-agent/src/core/model-runtime.ts: getProviders, getProvider,
// getModels, checkAuth, getAuth, listCredentials, getError, hasConfiguredAuth,
// refresh) composed with models.json the way
// packages/coding-agent/src/core/provider-composer.ts composes provider auth
// (composeApiKeyAuth, composeOAuthAuth, withConfiguredAuth,
// resolveConfiguredModelHeaders). Extensions are not loaded on this path,
// matching the upstream `pi auth` commands, which run before extensions.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/configvalue"
	"github.com/MichaelKinsy/PiG/internal/text"
)

// RuntimeModel is the model identity request-auth resolution needs from
// upstream Model<Api>.
type RuntimeModel struct {
	Provider string
	ID       string
	Name     string
	// Headers are the model's intrinsic catalog headers.
	Headers ai.ProviderHeaders
}

// RuntimeProvider is a composed provider: its id and auth methods. Mirrors
// the auth-bearing part of upstream Provider.
type RuntimeProvider struct {
	ID   string
	Auth ai.ProviderAuth

	models []RuntimeModel
}

// RequestAuthRuntimeOptions mirrors the CreateModelRuntimeOptions this
// surface honours.
type RequestAuthRuntimeOptions struct {
	Credentials ai.CredentialStore
	// AgentDir holds models.json; empty means no models.json, as upstream
	// modelsPath null.
	AgentDir string
	// RefreshOnCreate computes configured-provider availability at creation.
	RefreshOnCreate bool
	// AuthContext overrides the process environment and filesystem.
	AuthContext *ai.AuthContext
	// ModelsStore holds refreshed dynamic catalogs; nil means
	// <AgentDir>/models-store.json, or an in-memory store without AgentDir.
	ModelsStore ai.ModelsStore
}

// RequestAuthRuntime resolves provider credentials the way upstream
// ModelRuntime does for request auth.
type RequestAuthRuntime struct {
	credentials ai.CredentialStore
	authContext ai.AuthContext

	config      *modelsConfig
	configError string
	configOrder []string

	// radius holds the built-in Radius provider and one per models.json
	// "oauth": "radius" gateway, as upstream configureRadiusProviders.
	radius      map[string]*ai.RadiusProvider
	modelsStore ai.ModelsStore

	providers         []*RuntimeProvider
	providerByID      map[string]*RuntimeProvider
	compositionErrors []compositionError

	configured        map[string]bool
	availabilityError string
}

type compositionError struct {
	providerID string
	message    string
}

// NewRequestAuthRuntime composes built-in providers with models.json.
func NewRequestAuthRuntime(ctx context.Context, options RequestAuthRuntimeOptions) (*RequestAuthRuntime, error) {
	if options.Credentials == nil {
		return nil, errors.New("request auth runtime requires credentials")
	}
	runtime := &RequestAuthRuntime{
		credentials:  options.Credentials,
		authContext:  ai.DefaultProviderAuthContext(),
		providerByID: map[string]*RuntimeProvider{},
		configured:   map[string]bool{},
	}
	if options.AuthContext != nil {
		runtime.authContext = *options.AuthContext
	}
	runtime.modelsStore = options.ModelsStore
	if runtime.modelsStore == nil {
		runtime.modelsStore = defaultModelsStore(options.AgentDir)
	}
	if options.AgentDir != "" {
		registry := &ModelRegistry{agentDir: options.AgentDir}
		runtime.config, runtime.configError = registry.readConfig()
		runtime.configOrder = modelsJSONProviderOrder(filepath.Join(options.AgentDir, "models.json"))
	}
	runtime.configureRadiusProviders()
	runtime.rebuildProviders()
	if options.RefreshOnCreate {
		runtime.Refresh(ctx)
	}
	return runtime, nil
}

func (r *RequestAuthRuntime) configProvider(providerID string) (providerConfig, bool) {
	if r.config == nil {
		return providerConfig{}, false
	}
	config, ok := r.config.Providers[providerID]
	return config, ok
}

// configureRadiusProviders mirrors upstream configureRadiusProviders.
func (r *RequestAuthRuntime) configureRadiusProviders() {
	r.radius = map[string]*ai.RadiusProvider{RadiusProviderID: ai.NewRadiusProvider(ai.RadiusProviderOptions{ID: RadiusProviderID})}
	if r.config == nil {
		return
	}
	for providerID, config := range r.config.Providers {
		if config.OAuth == nil || config.OAuth.Kind != "radius" || config.BaseURL == "" {
			continue
		}
		name := config.Name
		if name == "" {
			name = providerID
		}
		r.radius[providerID] = ai.NewRadiusProvider(ai.RadiusProviderOptions{ID: providerID, Name: name, Gateway: radiusBaseURLVersionSuffix.ReplaceAllString(config.BaseURL, "")})
	}
}

// rebuildProviders composes built-ins first, in catalog order, then
// models.json providers in file order.
func (r *RequestAuthRuntime) rebuildProviders() {
	r.providers, r.providerByID, r.compositionErrors = nil, map[string]*RuntimeProvider{}, nil
	configOrder := r.configOrder
	ids := ai.ListProviders()
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}
	if r.config != nil {
		for _, id := range configOrder {
			if _, ok := r.config.Providers[id]; ok && !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
		for _, id := range slices.Sorted(maps.Keys(r.config.Providers)) {
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
	}
	for _, id := range ids {
		if provider := r.composeProvider(id); provider != nil {
			r.providers = append(r.providers, provider)
			r.providerByID[id] = provider
		}
	}
}

// composeProvider mirrors upstream recomposeProvider for built-in and
// models.json layers.
func (r *RequestAuthRuntime) composeProvider(providerID string) *RuntimeProvider {
	base, baseErr := ai.BuiltinProviderAuth(providerID)
	hasBase := baseErr == nil
	baseModels := builtinRuntimeModels(providerID)
	if radius := r.radius[providerID]; radius != nil {
		base, hasBase, baseModels = ai.RadiusProviderAuth(radius), true, radiusRuntimeModels(radius)
	}
	config, hasConfig := r.configProvider(providerID)
	if !hasBase && !hasConfig {
		return nil
	}
	if !hasConfig {
		return &RuntimeProvider{ID: providerID, Auth: base, models: baseModels}
	}
	fallback := func(message string) *RuntimeProvider {
		r.compositionErrors = append(r.compositionErrors, compositionError{providerID: providerID, message: message})
		if hasBase {
			return &RuntimeProvider{ID: providerID, Auth: base, models: baseModels}
		}
		return nil
	}
	if config.OAuth != nil && config.BaseURL == "" {
		return fallback(fmt.Sprintf(`Provider %s: "baseUrl" is required when "oauth" is set.`, providerID))
	}
	auth := ai.ProviderAuth{
		APIKey: r.composeAPIKeyAuth(providerID, base, config),
		OAuth:  composeOAuthAuth(providerID, base.OAuth, config),
	}
	if auth.APIKey == nil && auth.OAuth == nil {
		return fallback(fmt.Sprintf("Provider %s: no authentication method configured.", providerID))
	}
	return &RuntimeProvider{ID: providerID, Auth: auth, models: applyModelsJSONToRuntimeModels(providerID, baseModels, config)}
}

func builtinRuntimeModels(providerID string) []RuntimeModel {
	catalog := ai.ListModels(providerID)
	models := make([]RuntimeModel, 0, len(catalog))
	for _, model := range catalog {
		models = append(models, RuntimeModel{
			Provider: model.Provider,
			ID:       model.ID,
			Name:     model.DisplayName,
			Headers:  ai.ProviderHeadersFromStrings(model.Headers),
		})
	}
	return models
}

func radiusRuntimeModels(provider *ai.RadiusProvider) []RuntimeModel {
	catalog := provider.GetModels()
	models := make([]RuntimeModel, 0, len(catalog))
	for _, model := range catalog {
		models = append(models, RuntimeModel{Provider: model.Provider, ID: model.ID, Name: model.Name})
	}
	return models
}

// applyModelsJSONToRuntimeModels upserts models.json definitions by id and
// applies model override names. Mirrors upstream applyModelsJson,
// modelFromJson, and applyModelOverride for model identity.
func applyModelsJSONToRuntimeModels(providerID string, base []RuntimeModel, config providerConfig) []RuntimeModel {
	models := slices.Clone(base)
	for _, definition := range config.Models {
		name := definition.Name
		if name == "" {
			name = definition.ID
		}
		model := RuntimeModel{Provider: providerID, ID: definition.ID, Name: name}
		if index := slices.IndexFunc(models, func(existing RuntimeModel) bool { return existing.ID == definition.ID }); index >= 0 {
			models[index] = model
		} else {
			models = append(models, model)
		}
	}
	for index, model := range models {
		if override, ok := config.ModelOverrides[model.ID]; ok && override.Name != "" {
			models[index].Name = override.Name
		}
	}
	return models
}

// orderedHeaders returns headers in their models.json order.
func orderedHeaders(headers map[string]*string, entries []orderedHeaderEntry) []orderedHeaderEntry {
	if len(headers) == 0 {
		return nil
	}
	ordered := make([]orderedHeaderEntry, 0, len(headers))
	seen := make(map[string]bool, len(headers))
	for _, entry := range entries {
		if value, ok := headers[entry.Name]; ok && !seen[entry.Name] {
			ordered = append(ordered, orderedHeaderEntry{Name: entry.Name, Value: value})
			seen[entry.Name] = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(headers)) {
		if !seen[name] {
			ordered = append(ordered, orderedHeaderEntry{Name: name, Value: headers[name]})
		}
	}
	return ordered
}

// overlayHeaders mirrors an object spread of later header sets over earlier
// ones, keeping first-insertion order.
func overlayHeaders(layers ...[]orderedHeaderEntry) []orderedHeaderEntry {
	var merged []orderedHeaderEntry
	for _, layer := range layers {
		for _, entry := range layer {
			if index := slices.IndexFunc(merged, func(existing orderedHeaderEntry) bool { return existing.Name == entry.Name }); index >= 0 {
				merged[index].Value = entry.Value
			} else {
				merged = append(merged, entry)
			}
		}
	}
	return merged
}

// configContextEnv collects the environment values referenced by config
// values, preferring explicit entries. Mirrors upstream configContextEnv.
func configContextEnv(values []string, authContext ai.AuthContext, explicit map[string]string) map[string]string {
	env := maps.Clone(explicit)
	if env == nil {
		env = map[string]string{}
	}
	for _, value := range values {
		for _, name := range configvalue.GetConfigValueEnvVarNames(value) {
			if _, ok := env[name]; ok {
				continue
			}
			if resolved, ok := authContext.Env(name); ok {
				env[name] = resolved
			}
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func headerValues(headers []orderedHeaderEntry) []string {
	values := make([]string, 0, len(headers))
	for _, entry := range headers {
		if entry.Value != nil {
			values = append(values, *entry.Value)
		}
	}
	return values
}

// resolveHeadersOrError mirrors upstream resolveHeadersOrThrow. A nil value is
// a deletion marker and is kept.
func resolveHeadersOrError(headers []orderedHeaderEntry, description string, env map[string]string) (ai.ProviderHeaders, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	resolved := make(ai.ProviderHeaders, len(headers))
	for _, entry := range headers {
		if entry.Value == nil {
			resolved[entry.Name] = nil
			continue
		}
		text, err := configvalue.ResolveOrError(*entry.Value, fmt.Sprintf(`%s header "%s"`, description, entry.Name), env)
		if err != nil {
			return nil, err
		}
		resolved[entry.Name] = new(text)
	}
	return resolved, nil
}

// withConfiguredAuth mirrors upstream withConfiguredAuth.
func withConfiguredAuth(auth ai.ModelAuth, headers ai.ProviderHeaders, authHeader bool) (ai.ModelAuth, error) {
	var merged ai.ProviderHeaders
	if auth.Headers != nil || headers != nil {
		merged = make(ai.ProviderHeaders, len(auth.Headers)+len(headers))
		maps.Copy(merged, auth.Headers)
		maps.Copy(merged, headers)
	}
	if authHeader {
		if auth.APIKey == "" {
			return ai.ModelAuth{}, errors.New("authHeader requires a resolved API key")
		}
		if merged == nil {
			merged = ai.ProviderHeaders{}
		}
		merged["Authorization"] = new("Bearer " + auth.APIKey)
	}
	auth.Headers = merged
	return auth, nil
}

func authHeaderEnabled(config providerConfig) bool {
	return config.AuthHeader != nil && *config.AuthHeader
}

// composeAPIKeyAuth mirrors upstream composeApiKeyAuth for the built-in and
// models.json layers.
func (r *RequestAuthRuntime) composeAPIKeyAuth(providerID string, base ai.ProviderAuth, config providerConfig) *ai.APIKeyAuth {
	inherited := base.APIKey
	rawKey := config.APIKey
	// OAuth-only providers get no fabricated API-key method.
	if inherited == nil && rawKey == "" && base.OAuth != nil {
		return nil
	}
	rawHeaders := orderedHeaders(config.Headers, config.headerEntries)
	authHeader := authHeaderEnabled(config)
	name := "API key"
	if inherited != nil {
		name = inherited.Name
	}
	resolveInherited := func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		if inherited == nil {
			return nil, nil
		}
		return inherited.Resolve(ctx, input)
	}
	checkInherited := func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
		if inherited != nil && inherited.Check != nil {
			return inherited.Check(ctx, input)
		}
		resolved, err := resolveInherited(ctx, input)
		if err != nil || resolved == nil {
			return nil, err
		}
		return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: resolved.Source}, nil
	}
	return &ai.APIKeyAuth{
		Name: name,
		Check: func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
			if input.Credential != nil {
				if inherited != nil && inherited.Check != nil {
					return inherited.Check(ctx, input)
				}
				if input.Credential.Key != "" {
					return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "stored credential"}, nil
				}
				return checkInherited(ctx, input)
			}
			if rawKey != "" {
				if configvalue.IsCommandConfigValue(rawKey) {
					return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "configured API key"}, nil
				}
				for _, envName := range configvalue.GetConfigValueEnvVarNames(rawKey) {
					if _, ok := input.Ctx.Env(envName); !ok {
						return nil, nil
					}
				}
				return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "configured API key"}, nil
			}
			return checkInherited(ctx, input)
		},
		Resolve: func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
			var result *ai.AuthResult
			var err error
			switch {
			case input.Credential != nil:
				if inherited != nil {
					result, err = inherited.Resolve(ctx, input)
				} else if input.Credential.Key != "" {
					result = &ai.AuthResult{Auth: ai.ModelAuth{APIKey: input.Credential.Key}, Env: input.Credential.Env, Source: "stored credential"}
				}
			case rawKey != "":
				env := configContextEnv([]string{rawKey}, input.Ctx, nil)
				key, keyErr := configvalue.ResolveOrError(rawKey, fmt.Sprintf(`API key for provider "%s"`, providerID), env)
				if keyErr != nil {
					return nil, keyErr
				}
				if inherited != nil {
					result, err = inherited.Resolve(ctx, ai.APIKeyAuthInput{Ctx: input.Ctx, Credential: &ai.Credential{Type: ai.CredentialAPIKey, Key: key}})
				} else {
					result = &ai.AuthResult{Auth: ai.ModelAuth{APIKey: key}, Source: "configured API key"}
				}
			default:
				result, err = resolveInherited(ctx, input)
			}
			if err != nil || result == nil {
				return nil, err
			}
			explicitEnv := map[string]string{}
			if input.Credential != nil {
				maps.Copy(explicitEnv, input.Credential.Env)
			}
			maps.Copy(explicitEnv, result.Env)
			headerEnv := configContextEnv(headerValues(rawHeaders), input.Ctx, explicitEnv)
			headers, err := resolveHeadersOrError(rawHeaders, fmt.Sprintf(`provider "%s"`, providerID), headerEnv)
			if err != nil {
				return nil, err
			}
			auth, err := withConfiguredAuth(result.Auth, headers, authHeader)
			if err != nil {
				return nil, err
			}
			out := *result
			out.Auth = auth
			return &out, nil
		},
	}
}

// composeOAuthAuth mirrors upstream composeOAuthAuth: configured headers and
// authHeader apply to the derived OAuth auth.
func composeOAuthAuth(providerID string, oauth *ai.OAuthAuth, config providerConfig) *ai.OAuthAuth {
	if oauth == nil {
		return nil
	}
	rawHeaders := orderedHeaders(config.Headers, config.headerEntries)
	authHeader := authHeaderEnabled(config)
	composed := *oauth
	composed.ToAuth = func(credential ai.Credential) (ai.ModelAuth, error) {
		auth, err := oauth.ToAuth(credential)
		if err != nil {
			return ai.ModelAuth{}, err
		}
		headers, err := resolveHeadersOrError(rawHeaders, fmt.Sprintf(`provider "%s"`, providerID), credential.Env)
		if err != nil {
			return ai.ModelAuth{}, err
		}
		return withConfiguredAuth(auth, headers, authHeader)
	}
	return &composed
}

// modelsJSONProviderOrder returns models.json provider ids in file order, the
// order upstream ModelConfig.getProviderIds reports.
func modelsJSONProviderOrder(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var top map[string]json.RawMessage
	if json.Unmarshal([]byte(stripJSONComments(text.StripBom(string(data)))), &top) != nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(top["providers"]))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil
	}
	var order []string
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return order
		}
		key, _ := token.(string)
		order = append(order, key)
		var skip json.RawMessage
		if decoder.Decode(&skip) != nil {
			return order
		}
	}
	return order
}

// GetProviders returns composed providers in registration order.
func (r *RequestAuthRuntime) GetProviders() []*RuntimeProvider {
	return slices.Clone(r.providers)
}

// GetProvider returns a composed provider by exact id, or nil.
func (r *RequestAuthRuntime) GetProvider(providerID string) *RuntimeProvider {
	return r.providerByID[providerID]
}

// GetModels returns every provider's models, or one provider's when
// providerID is non-empty.
func (r *RequestAuthRuntime) GetModels(providerID string) []RuntimeModel {
	if providerID != "" {
		if provider := r.providerByID[providerID]; provider != nil {
			return slices.Clone(provider.models)
		}
		return nil
	}
	var models []RuntimeModel
	for _, provider := range r.providers {
		models = append(models, provider.models...)
	}
	return models
}

// CheckAuth reports configured auth for a provider without refreshing OAuth.
func (r *RequestAuthRuntime) CheckAuth(ctx context.Context, providerID string) (*ai.AuthCheck, error) {
	provider := r.providerByID[providerID]
	if provider == nil {
		return nil, ctx.Err()
	}
	return ai.CheckProviderAuth(ctx, providerID, provider.Auth, r.credentials, r.authContext)
}

// GetAuth resolves request auth for a provider, refreshing and persisting
// OAuth credentials that are about to expire.
func (r *RequestAuthRuntime) GetAuth(ctx context.Context, providerID string, overrides ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	provider := r.providerByID[providerID]
	if provider == nil {
		return nil, nil
	}
	return ai.ResolveProviderAuth(ctx, providerID, provider.Auth, r.credentials, r.authContext, overrides)
}

// GetModelAuth resolves request auth for a model: provider auth plus the
// model's intrinsic and configured headers.
func (r *RequestAuthRuntime) GetModelAuth(ctx context.Context, model RuntimeModel, overrides ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
	result, err := r.GetAuth(ctx, model.Provider, overrides)
	if err != nil || result == nil {
		return nil, err
	}
	if model.Headers != nil {
		result.Auth.Headers = ai.MergeProviderHeaders(result.Auth.Headers, model.Headers)
	}
	env := maps.Clone(result.Env)
	if env == nil && overrides.Env != nil {
		env = map[string]string{}
	}
	maps.Copy(env, overrides.Env)
	configured, err := resolveHeadersOrError(r.rawModelHeaders(model), fmt.Sprintf(`model "%s"`, model.Provider+"/"+model.ID), env)
	if err != nil {
		return nil, err
	}
	result.Auth.Headers = ai.MergeProviderHeaders(result.Auth.Headers, configured)
	return result, nil
}

// rawModelHeaders mirrors upstream rawModelHeaders for models.json.
func (r *RequestAuthRuntime) rawModelHeaders(model RuntimeModel) []orderedHeaderEntry {
	config, ok := r.configProvider(model.Provider)
	if !ok {
		return nil
	}
	override := config.ModelOverrides[model.ID]
	layers := [][]orderedHeaderEntry{orderedHeaders(override.Headers, override.headerEntries)}
	if index := slices.IndexFunc(config.Models, func(definition modelDefinition) bool { return definition.ID == model.ID }); index >= 0 {
		definition := config.Models[index]
		layers = append(layers, orderedHeaders(definition.Headers, definition.headerEntries))
	}
	return overlayHeaders(layers...)
}

// ListCredentials lists stored credential metadata.
func (r *RequestAuthRuntime) ListCredentials(ctx context.Context) ([]ai.CredentialInfo, error) {
	return r.credentials.List(ctx)
}

// HasConfiguredAuth reports the provider's availability from the last
// refresh.
func (r *RequestAuthRuntime) HasConfiguredAuth(providerID string) bool {
	return r.configured[providerID]
}

// GetError reports models.json, composition, and availability errors.
func (r *RequestAuthRuntime) GetError() string {
	var errs []string
	if r.configError != "" {
		errs = append(errs, r.configError)
	}
	for _, composition := range r.compositionErrors {
		errs = append(errs, fmt.Sprintf(`Provider "%s": %s`, composition.providerID, composition.message))
	}
	if r.availabilityError != "" {
		errs = append(errs, "Availability refresh: "+r.availabilityError)
	}
	return strings.Join(errs, "\n\n")
}

// Refresh restores dynamic catalogs from the models store without network
// access, then recomputes configured-provider availability. An availability
// failure keeps the previous availability and is reported through GetError.
func (r *RequestAuthRuntime) Refresh(ctx context.Context) {
	r.refreshCatalogs(ctx)
	configured := map[string]bool{}
	for _, provider := range r.providers {
		check, err := r.CheckAuth(ctx, provider.ID)
		if err != nil {
			r.availabilityError = err.Error()
			return
		}
		if check != nil {
			configured[provider.ID] = true
		}
	}
	if _, err := r.credentials.List(ctx); err != nil {
		r.availabilityError = err.Error()
		return
	}
	r.configured = configured
	r.availabilityError = ""
}

// refreshCatalogs mirrors the offline part of upstream Models.refresh for
// Radius providers: the stored catalog, or a legacy catalog imported from a
// stored OAuth credential, replaces the dynamic models. Upstream
// ModelRuntime.create drops the per-provider refresh errors, and so does this.
func (r *RequestAuthRuntime) refreshCatalogs(ctx context.Context) {
	for _, providerID := range slices.Sorted(maps.Keys(r.radius)) {
		provider := r.radius[providerID]
		stored, err := r.modelsStore.Read(ctx, providerID)
		if err != nil {
			continue
		}
		credential, err := r.credentials.Read(ctx, providerID)
		if err != nil {
			continue
		}
		_ = provider.RefreshModels(ctx, ai.RefreshModelsContext{Credential: credential, Stored: stored, Publish: func(publication ai.ModelsPublication) (bool, error) {
			if publication.Persist != nil {
				if err := r.modelsStore.Write(ctx, providerID, *publication.Persist); err != nil {
					return false, err
				}
			}
			publication.Update()
			return true, nil
		}})
	}
	r.rebuildProviders()
}
