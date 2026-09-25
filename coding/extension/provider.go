package extension

import (
	"bytes"
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
	Name          string               `json:"name,omitempty"`
	BaseURL       string               `json:"baseUrl,omitempty"`
	APIKey        string               `json:"apiKey,omitempty"`
	API           ai.API               `json:"api,omitempty"`
	StreamSimple  ProviderStreamSimple `json:"-"`
	Headers       map[string]string    `json:"headers,omitempty"`
	headerEntries []providerHeaderEntry
	AuthHeader    bool                  `json:"authHeader,omitempty"`
	Models        []ProviderModelConfig `json:"models,omitempty"`
	OAuth         *ProviderOAuth        `json:"oauth,omitempty"`
	// Insecure skips TLS certificate verification for this provider's
	// endpoint. Opt-in only, for self-signed/internal-CA on-prem gateways.
	// pig additive (D36): additive optional field; no upstream per-provider TLS-skip.
	Insecure bool `json:"insecure,omitempty"`
}

// ProviderStreamSimple mirrors upstream's optional streamSimple callback. The
// callback's model, request context, options, and event stream remain opaque at
// this dynamic extension boundary.
type ProviderStreamSimple = func(model Model, ctx AIContext, opts SimpleStreamOptions) AssistantMessageEventStream

// ProviderModelConfig mirrors upstream ProviderModelConfig 1:1.
type ProviderModelConfig struct {
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	API              ai.API              `json:"api,omitempty"`
	BaseURL          string              `json:"baseUrl,omitempty"`
	Reasoning        bool                `json:"reasoning"`
	ThinkingLevelMap ai.ThinkingLevelMap `json:"thinkingLevelMap,omitempty"`
	Input            []string            `json:"input"`
	Cost             ProviderModelCost   `json:"cost"`
	ContextWindow    int                 `json:"contextWindow"`
	MaxTokens        int                 `json:"maxTokens"`
	Headers          map[string]string   `json:"headers,omitempty"`
	headerEntries    []providerHeaderEntry
	Compat           any `json:"compat,omitempty"`
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
	if err != nil || config.headerEntries == nil {
		return data, err
	}
	return replaceProviderHeaders(data, config.headerEntries)
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
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// ProviderOAuth mirrors upstream ProviderConfig.oauth.
type ProviderOAuth struct {
	Name string `json:"name"`
	// IsSubscription marks access through this OAuth method as subscription-backed.
	IsSubscription bool                                                          `json:"isSubscription,omitempty"`
	Login          func(callbacks OAuthLoginCallbacks) (OAuthCredentials, error) `json:"-"`
	RefreshToken   func(creds OAuthCredentials) (OAuthCredentials, error)        `json:"-"`
	GetAPIKey      func(creds OAuthCredentials) string                           `json:"-"`
	ModifyModels   func(models []Model, creds OAuthCredentials) []Model          `json:"-"`
}
