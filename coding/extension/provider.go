package extension

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
)

// ProviderConfig is the registration payload for [API.RegisterProvider].
// Mirrors upstream's ProviderConfig 1:1.
//
// Field semantics (from upstream JSDoc):
//   - If Models is provided: replaces all existing models for this provider.
//   - If only BaseURL is provided: overrides the URL for existing models.
//   - If OAuth is provided: registers OAuth provider for /login support.
//   - If StreamSimple is provided: registers a custom API stream handler.
type ProviderConfig struct {
	Name         string               `json:"name,omitempty"`
	BaseURL      string               `json:"baseUrl,omitempty"`
	APIKey       string               `json:"apiKey,omitempty"`
	API          ai.API               `json:"api,omitempty"`
	StreamSimple ProviderStreamSimple `json:"-"`
	// RefreshModels refreshes this provider's model list; the returned models replace the extension-provided models. A
	// callback publishes a persisted catalog through context.Publish. Ports ProviderConfig.refreshModels
	// (core/extensions/types.ts:1930).
	RefreshModels func(context RefreshModelsContext) ([]ProviderModelConfig, error) `json:"-"`
	// Images are the image-generation implementations keyed by image API.
	// upstream: types.ts:1896 (ProviderConfig.images)
	Images map[ai.ImageAPI]*ai.ProviderImages `json:"-"`
	// Classifiers are the classifier implementations keyed by classifier API.
	// upstream: types.ts:1898 (ProviderConfig.classifiers)
	Classifiers   map[ai.ClassifierAPI]*ai.ProviderClassifier `json:"-"`
	Headers       map[string]string                           `json:"headers,omitempty"`
	headerEntries []providerHeaderEntry
	AuthHeader    bool                  `json:"authHeader,omitempty"`
	Models        []ProviderModelConfig `json:"models,omitempty"`
	OAuth         *ProviderOAuth        `json:"oauth,omitempty"`
	// Insecure skips TLS certificate verification for this provider's
	// endpoint. Opt-in only, for self-signed/internal-CA on-prem gateways.
	// pig additive (D36): additive optional field; no upstream per-provider TLS-skip.
	Insecure bool `json:"insecure,omitempty"`
}

// RefreshModelsContext is the context a RefreshModels callback receives: the credential and stored catalog, whether the
// network may be used, the cancellation signal and Publish for persisting a refreshed catalog.
type RefreshModelsContext = ai.RefreshModelsContext

// ProviderStreamSimple mirrors upstream's optional streamSimple callback. The
// callback's model, request context, options, and event stream remain opaque at
// this dynamic extension boundary.
type ProviderStreamSimple = func(model Model, ctx AIContext, opts SimpleStreamOptions) AssistantMessageEventStream

// ProviderModelConfig mirrors upstream ProviderModelConfig, the union of
// ProviderChatModelConfig, ProviderImageModelConfig and
// ProviderClassifierModelConfig (types.ts:1929-1991).
//
// Go mechanic (not a divergence): one struct carries the discriminator and the
// fields of all three variants, so an entry round-trips through the extension
// wire and the registries unchanged. Type omitted is normalized to "chat".
// A chat entry uses ID, Name, API, BaseURL, Reasoning, ThinkingLevelMap, Input,
// InputLimits, Cost, PromptCache, SamplingParams, SamplingParamsByThinkingLevel,
// ContextWindow, MaxTokens, Headers and Compat. An image entry uses ID, Name, API (an image API), BaseURL,
// Input, InputLimits, Cost, Headers and Output. A classifier entry uses ID,
// Name, API (a classifier API), BaseURL, Input, InputLimits, Cost, Headers and
// ContextWindow. A field outside its variant is not marshalled.
type ProviderModelConfig struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Type is "chat", "image" or "classifier". Empty is "chat".
	// upstream: types.ts:1953-1976 (type)
	Type ai.ModelType `json:"type,omitempty"`
	// API is a chat API, or for an image or classifier entry the image or classifier API id.
	API ai.API `json:"api,omitempty"`
	// Output is the output types of an image entry: it always includes "image"; "text" means the model can also return text blocks.
	// upstream: types.ts:1969 (ProviderImageModelConfig.output)
	Output           []string            `json:"output,omitempty"`
	BaseURL          string              `json:"baseUrl,omitempty"`
	Reasoning        bool                `json:"reasoning"`
	ThinkingLevelMap ai.ThinkingLevelMap `json:"thinkingLevelMap,omitempty"`
	Input            []string            `json:"input"`
	// InputLimits is upstream's provider input limits and cache-safe image
	// preprocessing metadata.
	InputLimits *ai.ModelInputLimits `json:"inputLimits,omitempty"`
	Cost        ProviderModelCost    `json:"cost"`
	// PromptCache is upstream's best-effort prompt cache lifetime in seconds
	// per retention tier.
	PromptCache    ai.ModelPromptCache `json:"promptCache,omitempty"`
	SamplingParams map[string]any      `json:"samplingParams,omitempty"`
	// SamplingParamsByThinkingLevel overrides SamplingParams for the effective Pi thinking level of a chat entry.
	// upstream: packages/coding-agent/src/core/provider-composer.ts:72 (ProviderChatModelConfig.samplingParamsByThinkingLevel), carried by extensionModelFromDefinition's spread.
	SamplingParamsByThinkingLevel ai.SamplingParamsByThinkingLevel `json:"samplingParamsByThinkingLevel,omitempty"`
	ContextWindow                 int                              `json:"contextWindow"`
	MaxTokens                     int                              `json:"maxTokens"`
	Headers                       map[string]string                `json:"headers,omitempty"`
	headerEntries                 []providerHeaderEntry
	Compat                        any `json:"compat,omitempty"`
}

type providerHeaderEntry struct {
	Name  string
	Value string
}

type providerConfigJSON ProviderConfig

type providerModelConfigJSON ProviderModelConfig

func (config *ProviderConfig) UnmarshalJSON(data []byte) error {
	var decoded providerConfigJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*config = ProviderConfig(decoded)
	entries, err := decodeProviderHeaderEntries(data)
	if err != nil {
		return err
	}
	config.headerEntries = entries
	return nil
}

func (config ProviderConfig) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(providerConfigJSON(config))
	if err != nil || config.headerEntries == nil {
		return data, err
	}
	return replaceProviderHeaders(data, config.headerEntries)
}

func (config *ProviderModelConfig) UnmarshalJSON(data []byte) error {
	var decoded providerModelConfigJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*config = ProviderModelConfig(decoded)
	entries, err := decodeProviderHeaderEntries(data)
	if err != nil {
		return err
	}
	config.headerEntries = entries
	return nil
}

func (config ProviderModelConfig) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(providerModelConfigJSON(config))
	if err != nil {
		return data, err
	}
	if dropped := nonChatOmittedFields(config.Type); dropped != nil {
		if data, err = omitJSONFields(data, dropped); err != nil {
			return nil, err
		}
	}
	if config.headerEntries == nil {
		return data, nil
	}
	return replaceProviderHeaders(data, config.headerEntries)
}

// nonChatOmittedFields lists the chat-only fields an image or classifier entry does not carry. upstream: types.ts:1929-1991 (ProviderImageModelConfig, ProviderClassifierModelConfig extend only the base config)
func nonChatOmittedFields(modelType ai.ModelType) []string {
	switch modelType {
	case ai.ModelTypeImage:
		return []string{"reasoning", "thinkingLevelMap", "promptCache", "samplingParams", "samplingParamsByThinkingLevel", "contextWindow", "maxTokens", "compat"}
	case ai.ModelTypeClassifier:
		return []string{"reasoning", "thinkingLevelMap", "promptCache", "samplingParams", "samplingParamsByThinkingLevel", "maxTokens", "compat"}
	}
	return nil
}

func omitJSONFields(data []byte, fields []string) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	for _, field := range fields {
		delete(object, field)
	}
	return json.Marshal(object)
}

func decodeProviderHeaderEntries(data []byte) ([]providerHeaderEntry, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, err
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if key != "headers" {
			var discard json.RawMessage
			if err := decoder.Decode(&discard); err != nil {
				return nil, err
			}
			continue
		}
		return decodeProviderHeaderObject(decoder)
	}
	return nil, nil
}

func decodeProviderHeaderObject(decoder *json.Decoder) ([]providerHeaderEntry, error) {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, err
	}
	var entries []providerHeaderEntry
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		entries = append(entries, providerHeaderEntry{Name: name.(string), Value: value})
	}
	_, err = decoder.Token()
	return entries, err
}

func replaceProviderHeaders(data []byte, entries []providerHeaderEntry) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	var headers bytes.Buffer
	headers.WriteByte('{')
	for index, entry := range entries {
		if index > 0 {
			headers.WriteByte(',')
		}
		name, _ := json.Marshal(entry.Name)
		value, _ := json.Marshal(entry.Value)
		headers.Write(name)
		headers.WriteByte(':')
		headers.Write(value)
	}
	headers.WriteByte('}')
	object["headers"] = headers.Bytes()
	return json.Marshal(object)
}

// ProviderModelCost mirrors upstream ProviderModelConfig.cost.
type ProviderModelCost struct {
	Input      float64       `json:"input"`
	Output     float64       `json:"output"`
	CacheRead  float64       `json:"cacheRead"`
	CacheWrite float64       `json:"cacheWrite"`
	Tiers      []ai.CostTier `json:"tiers,omitzero"`
}

// ProviderOAuth mirrors upstream ProviderConfig.oauth.
type ProviderOAuth struct {
	Name string `json:"name"`
	// IsSubscription marks access through this OAuth method as subscription-backed.
	IsSubscription bool `json:"isSubscription,omitempty"`
	// UsesCallbackServer is retained for source compatibility; canonical auth flows ignore it (types.ts:1937-1938).
	UsesCallbackServer bool                                                          `json:"usesCallbackServer,omitempty"`
	Login              func(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) `json:"-"`
	// RefreshToken refreshes expired credentials; ctx is the refresh's AbortSignal (types.ts:1944 refreshToken(credentials, signal)).
	RefreshToken func(ctx context.Context, creds OAuthCredentials) (OAuthCredentials, error) `json:"-"`
	GetAPIKey    func(creds OAuthCredentials) string                                         `json:"-"`
	ModifyModels func(models []Model, creds OAuthCredentials) []Model                        `json:"-"`
}
