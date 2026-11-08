package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/models/provider-config.ts (models.json composition adapted upstream from Pi's
// provider-composer.ts, MIT; extension compatibility is intentionally omitted). Models are composed as Pi's model JSON
// records (what a models store persists), so every merge is the upstream object spread, and decoded to typed models
// last.

type object = chordjson.Object

// get is object[key]; undefined (nil) for an absent key or a nil object.
func get(o *object, key string) any {
	if o == nil {
		return nil
	}
	return o.Value(key)
}

func objectOf(value any) *object {
	o, _ := value.(*object)
	return o
}

// firstOf is `a ?? b ?? ...`: the first value that is not undefined or null.
func firstOf(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// spread is `{...a, ...b, ...}`: a new object with every member, later objects overriding earlier ones in place.
func spread(sources ...*object) *object {
	merged := chordjson.NewObject(0)
	for _, source := range sources {
		if source == nil {
			continue
		}
		for key, value := range source.All() {
			merged.Set(key, value)
		}
	}
	return merged
}

// assign is `o[key] = value` as JSON.stringify sees it: an undefined (nil) value leaves no member.
func assign(o *object, key string, value any) {
	if value == nil {
		o.Delete(key)
		return
	}
	o.Set(key, value)
}

// spreadOrUndefined is `{...base, ...override}` of two objects that may be absent: absent when both are.
func spreadOrUndefined(base, override *object) any {
	if base == nil && override == nil {
		return nil
	}
	return spread(base, override)
}

func headersOf(value any) []Header {
	o := objectOf(value)
	if o == nil {
		return nil
	}
	headers := make([]Header, 0, o.Len())
	for name, header := range o.All() {
		text, _ := header.(string)
		headers = append(headers, Header{Name: name, Value: text})
	}
	return headers
}

func providerHeaders(headers map[string]string) ai.ProviderHeaders {
	if headers == nil {
		return nil
	}
	out := make(ai.ProviderHeaders, len(headers))
	for name, value := range headers {
		out[name] = &value
	}
	return out
}

var mergedCompatKeys = []string{"openRouterRouting", "vercelGatewayRouting", "chatTemplateKwargs", "chatTemplateArgs"}

// mergeCompat is the model's compat with override's members over it, the nested routing members merged one level deeper.
func mergeCompat(base, override *object) any {
	if override == nil {
		if base == nil {
			return nil
		}
		return base
	}
	merged := spread(base, override)
	for _, key := range mergedCompatKeys {
		baseValue, overrideValue := objectOf(get(base, key)), objectOf(get(override, key))
		if baseValue != nil || overrideValue != nil {
			merged.Set(key, spread(baseValue, overrideValue))
		}
	}
	return merged
}

func mergeInputLimits(base, override *object) any {
	if override == nil {
		return orNil(base)
	}
	baseImages, overrideImages := objectOf(get(base, "images")), objectOf(get(override, "images"))
	merged := spread(base, override)
	if overrideImages != nil {
		images := spread(baseImages, overrideImages)
		if overrideResize := objectOf(get(overrideImages, "resize")); overrideResize != nil {
			images.Set("resize", spread(objectOf(get(baseImages, "resize")), overrideResize))
		} else {
			assign(images, "resize", orNil(objectOf(get(baseImages, "resize"))))
		}
		merged.Set("images", images)
	} else {
		assign(merged, "images", orNil(baseImages))
	}
	return merged
}

// orNil is o as an undefined-or-object value (a nil *object is not a nil any).
func orNil(o *object) any {
	if o == nil {
		return nil
	}
	return o
}

func isChat(record *object) bool {
	kind := record.Value("type")
	return kind == nil || kind == "chat"
}

func applyModelOverride(model, override *object) *object {
	result := spread(model)
	assign(result, "name", firstOf(get(override, "name"), get(model, "name")))
	assign(result, "reasoning", firstOf(get(override, "reasoning"), get(model, "reasoning")))
	if levels := objectOf(get(override, "thinkingLevelMap")); levels != nil {
		assign(result, "thinkingLevelMap", spread(objectOf(get(model, "thinkingLevelMap")), levels))
	} else {
		assign(result, "thinkingLevelMap", get(model, "thinkingLevelMap"))
	}
	assign(result, "input", firstOf(get(override, "input"), get(model, "input")))
	assign(result, "inputLimits", mergeInputLimits(objectOf(get(model, "inputLimits")), objectOf(get(override, "inputLimits"))))
	if cost := objectOf(get(override, "cost")); cost != nil {
		base := objectOf(get(model, "cost"))
		merged := chordjson.NewObject(5)
		for _, key := range []string{"input", "output", "cacheRead", "cacheWrite", "tiers"} {
			if value := firstOf(get(cost, key), get(base, key)); value != nil {
				merged.Set(key, value)
			}
		}
		assign(result, "cost", merged)
	}
	if cache := objectOf(get(override, "promptCache")); cache != nil {
		assign(result, "promptCache", spread(objectOf(get(model, "promptCache")), cache))
	}
	assign(result, "contextWindow", firstOf(get(override, "contextWindow"), get(model, "contextWindow")))
	assign(result, "maxTokens", firstOf(get(override, "maxTokens"), get(model, "maxTokens")))
	if sampling := objectOf(get(override, "samplingParams")); sampling != nil {
		assign(result, "samplingParams", spread(objectOf(get(model, "samplingParams")), sampling))
	}
	assign(result, "compat", mergeCompat(objectOf(get(model, "compat")), objectOf(get(override, "compat"))))
	return result
}

func modelFromJSON(providerID string, definition, providerConfig, defaults *object) (*object, error) {
	api := firstOf(get(definition, "api"), get(providerConfig, "api"), get(defaults, "api"))
	if api == nil {
		return nil, fmt.Errorf(`Provider %s, model %s: no "api" specified. Set at provider or model level.`, providerID, get(definition, "id"))
	}
	baseURL := firstOf(get(definition, "baseUrl"), get(providerConfig, "baseUrl"), get(defaults, "baseUrl"))
	if baseURL == nil {
		return nil, fmt.Errorf(`Provider %s: "baseUrl" is required when defining custom models.`, providerID)
	}
	if window, ok := get(definition, "contextWindow").(float64); ok && window <= 0 {
		return nil, fmt.Errorf("Provider %s, model %s: invalid contextWindow", providerID, get(definition, "id"))
	}
	if tokens, ok := get(definition, "maxTokens").(float64); ok && tokens <= 0 {
		return nil, fmt.Errorf("Provider %s, model %s: invalid maxTokens", providerID, get(definition, "id"))
	}
	model := chordjson.NewObject(16)
	model.Set("id", get(definition, "id"))
	model.Set("name", firstOf(get(definition, "name"), get(definition, "id")))
	model.Set("api", api)
	model.Set("provider", providerID)
	model.Set("baseUrl", baseURL)
	model.Set("reasoning", firstOf(get(definition, "reasoning"), false))
	assign(model, "thinkingLevelMap", get(definition, "thinkingLevelMap"))
	model.Set("input", firstOf(get(definition, "input"), []any{"text"}))
	assign(model, "inputLimits", get(definition, "inputLimits"))
	model.Set("cost", firstOf(get(definition, "cost"), chordjson.ObjectOf("input", float64(0), "output", float64(0), "cacheRead", float64(0), "cacheWrite", float64(0))))
	assign(model, "promptCache", get(definition, "promptCache"))
	model.Set("contextWindow", firstOf(get(definition, "contextWindow"), float64(128000)))
	model.Set("maxTokens", firstOf(get(definition, "maxTokens"), float64(16384)))
	assign(model, "samplingParams", get(definition, "samplingParams"))
	assign(model, "compat", mergeCompat(objectOf(get(providerConfig, "compat")), objectOf(get(definition, "compat"))))
	return model, nil
}

func findModelDefaults(models []*object, modelID string, api any) *object {
	var chat []*object
	for _, model := range models {
		if isChat(model) {
			chat = append(chat, model)
		}
	}
	for _, model := range chat {
		if get(model, "id") == modelID {
			return model
		}
	}
	if api != nil {
		for _, model := range chat {
			if get(model, "api") == api {
				return model
			}
		}
	}
	for _, model := range chat {
		if get(model, "api") == "openai-completions" {
			return model
		}
	}
	if len(chat) > 0 {
		return chat[0]
	}
	return nil
}

func applyModelsJSON(providerID string, baseModels []*object, config *object) ([]*object, error) {
	if config == nil {
		return append([]*object(nil), baseModels...), nil
	}
	oauth := get(config, "oauth")
	if oauth != nil && get(config, "baseUrl") == nil {
		return nil, fmt.Errorf(`Provider %s: "baseUrl" is required when "oauth" is set.`, providerID)
	}
	overrides := objectOf(get(config, "modelOverrides"))
	models, _ := get(config, "models").([]any)
	if len(models) == 0 && get(config, "baseUrl") == nil && get(config, "headers") == nil && get(config, "compat") == nil &&
		(overrides == nil || overrides.Len() == 0) && get(config, "apiKey") == nil && oauth == nil && get(config, "authHeader") == nil {
		return nil, fmt.Errorf(`Provider %s: must specify "baseUrl", "headers", "compat", "modelOverrides", or "models".`, providerID)
	}
	composed := make([]*object, len(baseModels))
	for i, model := range baseModels {
		next := spread(model)
		baseURL := firstOf(get(config, "baseUrl"), get(model, "baseUrl"))
		if oauth == "radius" {
			baseURL = get(model, "baseUrl")
		}
		assign(next, "baseUrl", baseURL)
		if isChat(model) {
			assign(next, "compat", mergeCompat(objectOf(get(model, "compat")), objectOf(get(config, "compat"))))
		}
		composed[i] = next
	}
	for _, each := range models {
		definition := objectOf(each)
		existing := -1
		for i, model := range composed {
			if isChat(model) && get(model, "id") == get(definition, "id") {
				existing = i
				break
			}
		}
		defaults := findModelDefaults(composed, get(definition, "id").(string), firstOf(get(definition, "api"), get(config, "api")))
		model, err := modelFromJSON(providerID, definition, config, defaults)
		if err != nil {
			return nil, err
		}
		if existing >= 0 {
			composed[existing] = model
		} else {
			composed = append(composed, model)
		}
	}
	return composed, nil
}

func withConfiguredAuth(auth ai.ModelAuth, headers map[string]string, authHeader bool) (ai.ModelAuth, error) {
	merged := ai.MergeProviderHeaders(auth.Headers, providerHeaders(headers))
	if authHeader {
		if auth.APIKey == "" {
			return auth, errors.New("authHeader requires a resolved API key")
		}
		next := make(ai.ProviderHeaders, len(merged)+1)
		for name, value := range merged {
			next[name] = value
		}
		bearer := "Bearer " + auth.APIKey
		next["Authorization"] = &bearer
		merged = next
	}
	auth.Headers = merged
	return auth, nil
}

func recordOf(model ai.AnyModel) (*object, error) {
	encoded, err := ai.EncodeStoredModel(model)
	if err != nil {
		return nil, err
	}
	value, err := chordjson.Decode(encoded)
	if err != nil {
		return nil, err
	}
	return objectOf(value), nil
}

func modelOf(record *object) (ai.AnyModel, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	model, known, err := ai.DecodeStoredModel(encoded)
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, errors.New("Unsupported custom model type")
	}
	return model, nil
}

func baseRecords(base *ai.ModelsProvider) ([]*object, error) {
	if base == nil {
		return nil, nil
	}
	var models []ai.AnyModel
	switch {
	case base.GetAllModels != nil:
		all, err := base.GetAllModels()
		if err != nil {
			return nil, err
		}
		models = all
	case base.GetModels != nil:
		chat, err := base.GetModels()
		if err != nil {
			return nil, err
		}
		models = ai.AnyModels(chat)
	}
	records := make([]*object, len(models))
	for i, model := range models {
		record, err := recordOf(model)
		if err != nil {
			return nil, err
		}
		records[i] = record
	}
	return records, nil
}

var customAPIs = map[string]func() *ai.ProviderStreams{
	"openai-completions":   ai.OpenAICompletionsAPI,
	"openai-responses":     ai.OpenAIResponsesAPI,
	"anthropic-messages":   ai.AnthropicMessagesAPI,
	"google-generative-ai": ai.GoogleGenerativeAIAPI,
}

func apiOf(model ai.AnyModel) ai.API {
	if chat, ok := model.(*ai.Model); ok {
		return chat.ProviderMeta.API
	}
	return ""
}

func composeProvider(id string, base *ai.ModelsProvider, config *object) (*ai.ModelsProvider, error) {
	if get(config, "oauth") != nil {
		return nil, errors.New("Custom OAuth catalogs are not supported; use a built-in OAuth provider")
	}
	overrides := objectOf(get(config, "modelOverrides"))
	buildModels := func() ([]ai.AnyModel, error) {
		records, err := baseRecords(base)
		if err != nil {
			return nil, err
		}
		composed, err := applyModelsJSON(id, records, config)
		if err != nil {
			return nil, err
		}
		models := make([]ai.AnyModel, len(composed))
		for i, record := range composed {
			if override := objectOf(get(overrides, fmt.Sprint(get(record, "id")))); override != nil && isChat(record) {
				record = applyModelOverride(record, override)
			}
			if models[i], err = modelOf(record); err != nil {
				return nil, err
			}
		}
		return models, nil
	}
	models, err := buildModels()
	if err != nil {
		return nil, err
	}
	var inheritedModels []*ai.Model
	if base != nil && base.GetModels != nil {
		if inheritedModels, err = base.GetModels(); err != nil {
			return nil, err
		}
	}
	implementations := map[ai.API]*ai.ProviderStreams{}
	for _, model := range models {
		if model.ModelType() != ai.ModelTypeChat {
			continue
		}
		api := apiOf(model)
		inherited := false
		for _, entry := range inheritedModels {
			inherited = inherited || entry.ProviderMeta.API == api
		}
		switch {
		case inherited:
			implementations[api] = &ai.ProviderStreams{Stream: base.Stream, StreamSimple: base.StreamSimple}
		case customAPIs[string(api)] != nil:
			implementations[api] = customAPIs[string(api)]()
		default:
			return nil, errors.New("Unsupported custom model API")
		}
	}

	provider := ai.ModelsProvider{}
	if base != nil {
		provider = *base
	}
	provider.ID = id
	name, _ := get(config, "name").(string)
	if name == "" {
		if base != nil && base.Name != "" {
			name = base.Name
		} else {
			name = id
		}
	}
	provider.Name = name
	if baseURL, ok := get(config, "baseUrl").(string); ok {
		provider.BaseURL = baseURL
	}
	provider.Auth = composeAuth(base, config)
	provider.GetAllModels = buildModels
	provider.GetModels = func() ([]*ai.Model, error) {
		all, err := buildModels()
		if err != nil {
			return nil, err
		}
		var chat []*ai.Model
		for _, model := range all {
			if typed, ok := model.(*ai.Model); ok {
				chat = append(chat, typed)
			}
		}
		return chat, nil
	}
	streamOf := func(simple bool) ai.ModelsStreamFunction {
		return func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			api := implementations[model.ProviderMeta.API]
			if api == nil {
				return nil, errors.New("Unavailable model API")
			}
			if simple {
				return api.StreamSimple(ctx, model, transcript, options)
			}
			return api.Stream(ctx, model, transcript, options)
		}
	}
	provider.Stream, provider.StreamSimple = streamOf(false), streamOf(true)
	return &provider, nil
}

// composeAuth is the provider's auth methods with the configured API key, headers and authHeader applied.
func composeAuth(base *ai.ModelsProvider, config *object) ai.ProviderAuth {
	var auth ai.ProviderAuth
	var inherited *ai.APIKeyAuth
	var inheritedOAuth *ai.OAuthAuth
	if base != nil {
		inherited, inheritedOAuth = base.Auth.APIKey, base.Auth.OAuth
	}
	configuredKey, hasKey := get(config, "apiKey").(string)
	headers := headersOf(get(config, "headers"))
	authHeader, _ := get(config, "authHeader").(bool)

	if inherited != nil || hasKey || inheritedOAuth == nil {
		method := &ai.APIKeyAuth{Name: "API key"}
		if inherited != nil {
			method.Name = inherited.Name
		}
		method.Login = func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
			if inherited != nil && inherited.Login != nil {
				return inherited.Login(ctx, interaction)
			}
			key, err := interaction.Prompt(ctx, ai.AuthSecretPrompt{Message: "Enter API key"})
			if err != nil {
				return ai.Credential{}, err
			}
			return ai.Credential{Type: ai.CredentialAPIKey, Key: key}, nil
		}
		method.Check = func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
			if input.Credential != nil {
				if inherited != nil && inherited.Check != nil {
					return inherited.Check(ctx, input)
				}
				return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "stored credential"}, nil
			}
			if hasKey {
				if IsCommandConfigValue(configuredKey) {
					return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "configured API key"}, nil
				}
				for _, name := range GetConfigValueEnvVarNames(configuredKey) {
					if _, ok := input.Ctx.Env(name); !ok {
						return nil, nil
					}
				}
				return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "configured API key"}, nil
			}
			switch {
			case inherited != nil && inherited.Check != nil:
				return inherited.Check(ctx, input)
			case inherited != nil:
				resolved, err := inherited.Resolve(ctx, input)
				if err != nil || resolved == nil {
					return nil, err
				}
				return &ai.AuthCheck{Type: ai.CredentialAPIKey}, nil
			}
			return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "local"}, nil
		}
		method.Resolve = func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
			var result *ai.AuthResult
			var err error
			switch {
			case input.Credential != nil:
				if inherited != nil {
					result, err = inherited.Resolve(ctx, input)
				} else {
					result = &ai.AuthResult{Auth: ai.ModelAuth{APIKey: input.Credential.Key}, Env: input.Credential.Env, Source: "stored credential"}
				}
			case hasKey:
				env := map[string]string{}
				for _, name := range GetConfigValueEnvVarNames(configuredKey) {
					if value, ok := input.Ctx.Env(name); ok {
						env[name] = value
					}
				}
				key, keyErr := ResolveConfigValueOrError(configuredKey, "configured API key", env)
				if keyErr != nil {
					return nil, keyErr
				}
				if inherited != nil {
					result, err = inherited.Resolve(ctx, ai.APIKeyAuthInput{Ctx: input.Ctx, Credential: &ai.Credential{Type: ai.CredentialAPIKey, Key: key}})
				} else {
					result = &ai.AuthResult{Auth: ai.ModelAuth{APIKey: key}, Source: "configured API key"}
				}
			case inherited != nil:
				result, err = inherited.Resolve(ctx, input)
			default:
				result = &ai.AuthResult{Source: "local"}
			}
			if err != nil || result == nil {
				return nil, err
			}
			env := map[string]string{}
			if input.Credential != nil {
				for name, value := range input.Credential.Env {
					env[name] = value
				}
			}
			for name, value := range result.Env {
				env[name] = value
			}
			for _, header := range headers {
				for _, name := range GetConfigValueEnvVarNames(header.Value) {
					if _, ok := env[name]; ok {
						continue
					}
					if value, ok := input.Ctx.Env(name); ok {
						env[name] = value
					}
				}
			}
			resolved, err := ResolveHeadersOrError(headers, "configured provider", env)
			if err != nil {
				return nil, err
			}
			configured, err := withConfiguredAuth(result.Auth, resolved, authHeader)
			if err != nil {
				return nil, err
			}
			next := *result
			next.Auth = configured
			return &next, nil
		}
		auth.APIKey = method
	}
	if inheritedOAuth != nil {
		oauth := *inheritedOAuth
		original := inheritedOAuth.ToAuth
		oauth.ToAuth = func(credential ai.Credential) (ai.ModelAuth, error) {
			result, err := original(credential)
			if err != nil {
				return result, err
			}
			resolved, err := ResolveHeadersOrError(headers, "configured provider", credential.Env)
			if err != nil {
				return result, err
			}
			return withConfiguredAuth(result, resolved, authHeader)
		}
		auth.OAuth = &oauth
	}
	return auth
}

// ApplyModelConfig composes models.json's providers over models (each over the provider of its id, else alone), and adds
// the configured model-specific request headers to the auth of every chat model. It builds and validates completely
// before the caller publishes anything: a provider that cannot be composed is an error and the caller keeps its state.
func ApplyModelConfig(models *ai.Models, config *ModelConfig) error {
	for _, id := range config.ProviderIDs() {
		composed, err := composeProvider(id, models.GetProvider(id), config.Provider(id))
		if err != nil {
			return err
		}
		models.SetProvider(composed)
	}
	// Models resolves model auth for every request. Resolve model-specific headers here, before explicit request headers
	// and transformHeaders, not after provider dispatch.
	models.WrapModelAuth(func(original ai.ModelAuthFunc) ai.ModelAuthFunc {
		return func(ctx context.Context, model *ai.Model, overrides ai.AuthResolutionOverrides) (*ai.AuthResult, error) {
			result, err := original(ctx, model, overrides)
			if err != nil || result == nil {
				return result, err
			}
			provider := config.Provider(model.ProviderMeta.ProviderID)
			var raw []Header
			add := func(headers []Header) {
				for _, header := range headers {
					replaced := false
					for i := range raw {
						if raw[i].Name == header.Name {
							raw[i].Value, replaced = header.Value, true
						}
					}
					if !replaced {
						raw = append(raw, header)
					}
				}
			}
			for _, definition := range objectSlice(get(provider, "models")) {
				if get(definition, "id") == model.ID {
					add(headersOf(get(definition, "headers")))
					break
				}
			}
			add(headersOf(get(objectOf(get(objectOf(get(provider, "modelOverrides")), model.ID)), "headers")))
			env := map[string]string{}
			for name, value := range result.Env {
				env[name] = value
			}
			for name, value := range overrides.Env {
				env[name] = value
			}
			headers, err := ResolveHeadersOrError(raw, "configured model", env)
			if err != nil {
				return nil, err
			}
			next := *result
			next.Auth.Headers = ai.MergeProviderHeaders(result.Auth.Headers, providerHeaders(headers))
			return &next, nil
		}
	})
	return nil
}

func objectSlice(value any) []*object {
	items, _ := value.([]any)
	out := make([]*object, 0, len(items))
	for _, item := range items {
		if o := objectOf(item); o != nil {
			out = append(out, o)
		}
	}
	return out
}
