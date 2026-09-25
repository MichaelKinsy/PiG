package ai

// Mirrors upstream .upstream/current/packages/ai/src/providers/radius-config.ts.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// DefaultRadiusGateway is the Radius gateway origin Pi uses by default.
const DefaultRadiusGateway = "https://radius.pi.dev"

// RadiusModelCost is the per-million-token pricing carried by a Radius model.
type RadiusModelCost struct {
	Input      float64    `json:"input"`
	Output     float64    `json:"output"`
	CacheRead  float64    `json:"cacheRead"`
	CacheWrite float64    `json:"cacheWrite"`
	Tiers      []CostTier `json:"tiers,omitempty"`
}

// RadiusGatewayModel is one model advertised by a Radius gateway's /v1/config.
type RadiusGatewayModel struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Reasoning        bool              `json:"reasoning"`
	ThinkingLevelMap ThinkingLevelMap  `json:"thinkingLevelMap,omitempty"`
	Input            []string          `json:"input"`
	InputLimits      *ModelInputLimits `json:"inputLimits,omitempty"`
	Cost             RadiusModelCost   `json:"cost"`
	PromptCache      ModelPromptCache  `json:"promptCache,omitempty"`
	ContextWindow    int               `json:"contextWindow"`
	MaxTokens        int               `json:"maxTokens"`
	SamplingParams   map[string]any    `json:"samplingParams,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	Compat           *ModelCompat      `json:"compat,omitempty"`
}

// RadiusGatewayConfig is the sanitized /v1/config document of a gateway.
type RadiusGatewayConfig struct {
	BaseURL string               `json:"baseUrl"`
	Models  []RadiusGatewayModel `json:"models"`
}

// PiMessagesModel is Pi's Model<"pi-messages"> as served by a Radius provider:
// a gateway model bound to its provider ID, API, and request base URL.
type PiMessagesModel struct {
	RadiusGatewayModel
	API      API    `json:"api"`
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
}

// radiusModelFields names the upstream isRadiusGatewayModel type checks. Each
// field must decode as the named JSON kind before a model is accepted.
var radiusModelFields = []struct {
	name string
	kind byte
}{
	{"id", '"'}, {"name", '"'}, {"reasoning", 'b'}, {"input", '['}, {"cost", '{'}, {"contextWindow", 'n'}, {"maxTokens", 'n'},
}

func jsonKind(raw json.RawMessage) byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return 0
	}
	switch trimmed[0] {
	case 't', 'f':
		return 'b'
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return 'n'
	}
	return trimmed[0]
}

// decodeRadiusGatewayModel mirrors upstream isRadiusGatewayModel: a value
// qualifies only when every required field has the upstream JSON type.
func decodeRadiusGatewayModel(raw json.RawMessage) (RadiusGatewayModel, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return RadiusGatewayModel{}, false
	}
	for _, field := range radiusModelFields {
		if jsonKind(fields[field.name]) != field.kind {
			return RadiusGatewayModel{}, false
		}
	}
	var model RadiusGatewayModel
	if json.Unmarshal(raw, &model) != nil {
		return RadiusGatewayModel{}, false
	}
	return model, true
}

// sanitizeRadiusGatewayConfig mirrors upstream sanitizeRadiusGatewayConfig: it
// requires a string baseUrl and a models array, and drops invalid models.
func sanitizeRadiusGatewayConfig(raw json.RawMessage) (RadiusGatewayConfig, bool) {
	var document struct {
		BaseURL json.RawMessage   `json:"baseUrl"`
		Models  []json.RawMessage `json:"models"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || jsonKind(fields["models"]) != '[' || jsonKind(fields["baseUrl"]) != '"' {
		return RadiusGatewayConfig{}, false
	}
	if json.Unmarshal(raw, &document) != nil {
		return RadiusGatewayConfig{}, false
	}
	config := RadiusGatewayConfig{Models: []RadiusGatewayModel{}}
	if json.Unmarshal(document.BaseURL, &config.BaseURL) != nil {
		return RadiusGatewayConfig{}, false
	}
	for _, candidate := range document.Models {
		if model, ok := decodeRadiusGatewayModel(candidate); ok {
			config.Models = append(config.Models, model)
		}
	}
	return config, true
}

var radiusSchemePattern = regexp.MustCompile(`(?i)^https?://`)

// NormalizeRadiusGatewayURL adds an https scheme when absent and strips
// trailing slashes. Mirrors upstream normalizeRadiusGatewayUrl.
func NormalizeRadiusGatewayURL(value string) string {
	if !radiusSchemePattern.MatchString(value) {
		value = "https://" + value
	}
	return strings.TrimRight(value, "/")
}

// GetRadiusCredentialConfig returns the sanitized legacy gateway catalog that
// pre-ModelsStore Radius builds cached on the OAuth credential.
func GetRadiusCredentialConfig(credential *Credential) (RadiusGatewayConfig, bool) {
	if credential == nil || len(credential.GatewayConfig) == 0 {
		return RadiusGatewayConfig{}, false
	}
	return sanitizeRadiusGatewayConfig(credential.GatewayConfig)
}

// GetRadiusModelsFromConfig binds a gateway catalog to providerID.
func GetRadiusModelsFromConfig(providerID string, config RadiusGatewayConfig) []PiMessagesModel {
	models := make([]PiMessagesModel, 0, len(config.Models))
	for _, model := range config.Models {
		models = append(models, PiMessagesModel{RadiusGatewayModel: cloneRadiusGatewayModel(model), API: APIPiMessages, Provider: providerID, BaseURL: config.BaseURL})
	}
	return models
}

// GetRadiusModels returns the legacy credential catalog bound to providerID.
func GetRadiusModels(providerID string, credential *Credential) []PiMessagesModel {
	config, ok := GetRadiusCredentialConfig(credential)
	if !ok {
		return []PiMessagesModel{}
	}
	return GetRadiusModelsFromConfig(providerID, config)
}

func cloneRadiusGatewayModel(model RadiusGatewayModel) RadiusGatewayModel {
	model.Input = append([]string(nil), model.Input...)
	model.ThinkingLevelMap = cloneThinkingLevelMap(model.ThinkingLevelMap)
	model.InputLimits = model.InputLimits.Clone()
	model.Cost.Tiers = append([]CostTier(nil), model.Cost.Tiers...)
	model.PromptCache = maps.Clone(model.PromptCache)
	model.SamplingParams = maps.Clone(model.SamplingParams)
	model.Headers = cloneStringMap(model.Headers)
	model.Compat = cloneCompat(model.Compat)
	return model
}

func truncateHTTPBody(body string) string {
	trimmed := strings.TrimSpace(body)
	if utf16Length(trimmed) <= 512 {
		return trimmed
	}
	return truncateUTF16(trimmed, 512) + "…"
}

// utf16Length and truncateUTF16 keep JavaScript string.length semantics for
// upstream's 512-unit truncation.
func utf16Length(value string) int {
	length := 0
	for _, r := range value {
		length += utf16Units(r)
	}
	return length
}

func truncateUTF16(value string, limit int) string {
	used := 0
	for index, r := range value {
		units := utf16Units(r)
		if used+units > limit {
			return value[:index]
		}
		used += units
	}
	return value
}

func utf16Units(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// LoadRadiusGatewayConfig fetches and sanitizes <gateway>/v1/config.
// Mirrors upstream loadRadiusGatewayConfig. PiG sets no User-Agent here, so
// net/http sends its default (owner question Q4 tracks Pi's User-Agent).
func LoadRadiusGatewayConfig(ctx context.Context, gateway, apiKey string) (RadiusGatewayConfig, error) {
	endpoint, err := resolveGatewayPath(gateway, "/v1/config")
	if err != nil {
		return RadiusGatewayConfig{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return RadiusGatewayConfig{}, err
	}
	request.Header.Set("accept", "application/json")
	if apiKey != "" {
		request.Header.Set("authorization", "Bearer "+apiKey)
	}
	response, err := radiusHTTPClient.Do(request)
	if err != nil {
		return RadiusGatewayConfig{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return RadiusGatewayConfig{}, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return RadiusGatewayConfig{}, fmt.Errorf("Could not load Radius config from %s: %d: %s", gateway, response.StatusCode, truncateHTTPBody(string(body)))
	}
	config, ok := sanitizeRadiusGatewayConfig(body)
	if !ok {
		return RadiusGatewayConfig{}, fmt.Errorf("Invalid Radius config from %s", gateway)
	}
	return config, nil
}

// radiusHTTPClient serves Radius catalog and OAuth requests. Like upstream
// fetch, it has no client-level timeout; callers bound work with ctx.
var radiusHTTPClient = &http.Client{Transport: http.DefaultTransport}

// resolveGatewayPath mirrors `new URL(path, gateway)` for an absolute path.
func resolveGatewayPath(gateway, path string) (string, error) {
	base, err := parseGatewayURL(gateway)
	if err != nil {
		return "", err
	}
	reference, err := base.Parse(path)
	if err != nil {
		return "", err
	}
	return reference.String(), nil
}

func parseGatewayURL(gateway string) (*url.URL, error) {
	base, err := url.Parse(gateway)
	if err != nil {
		return nil, err
	}
	if !base.IsAbs() || base.Host == "" {
		return nil, fmt.Errorf("Invalid URL: %s", gateway)
	}
	return base, nil
}
