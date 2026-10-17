package ai

// Mirrors upstream .upstream/current/packages/ai/src/providers/radius-config.ts.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// DefaultRadiusGateway is the Radius gateway origin Pi uses by default.
const DefaultRadiusGateway = "https://radius.pi.dev"

// RadiusGatewayModel is one model advertised by a Radius gateway's /v1/config.
type RadiusGatewayModel struct {
	// Type is Model.type, the optional "chat" a gateway may advertise (types.ts:1119); it travels through the spread of getRadiusModelsFromConfig.
	Type             ModelType         `json:"type,omitempty"`
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Reasoning        bool              `json:"reasoning"`
	ThinkingLevelMap ThinkingLevelMap  `json:"thinkingLevelMap,omitempty"`
	Input            []string          `json:"input"`
	InputLimits      *ModelInputLimits `json:"inputLimits,omitempty"`
	Cost             ModelCost         `json:"cost"`
	PromptCache      ModelPromptCache  `json:"promptCache,omitempty"`
	ContextWindow    int               `json:"contextWindow"`
	MaxTokens        int               `json:"maxTokens"`
	SamplingParams   map[string]any    `json:"samplingParams,omitempty"`
	// SamplingParamsByThinkingLevel is Model.samplingParamsByThinkingLevel: radius-config.ts sanitizeRadiusGatewayConfig and
	// getRadiusModelsFromConfig spread each gateway model, so the field a gateway advertises reaches the Model<"pi-messages">.
	//portlint:allow emptydrop a level missing from the map selects no sampling params, so an explicit empty map and an absent one resolve alike
	SamplingParamsByThinkingLevel SamplingParamsByThinkingLevel `json:"samplingParamsByThinkingLevel,omitempty"`
	Headers                       map[string]string             `json:"headers,omitempty"`
	Compat                        *ModelCompat                  `json:"compat,omitempty"`
}

// RadiusGatewayConfig is the sanitized /v1/config document of a gateway.
type RadiusGatewayConfig struct {
	BaseURL string               `json:"baseUrl"`
	Models  []RadiusGatewayModel `json:"models"`
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
	case 'n':
		return 'z'
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return 'n'
	}
	return trimmed[0]
}

// decodeRadiusGatewayModel mirrors upstream isRadiusGatewayModel and the spread that copies an accepted model: a value
// qualifies only when every required field has the upstream JSON type, and every other member is carried through.
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
	return radiusGatewayModelMembers(fields), true
}

// radiusGatewayModelMembers decodes a gateway model one member at a time, reading each member by its exact key as the JavaScript
// property access does (encoding/json would also match "ID" or "contextwindow" to a field). isRadiusGatewayModel checks only that id,
// name, reasoning, input, cost, contextWindow and maxTokens have the right JavaScript type; the spread then carries every other member
// through untyped. A number member keeps the integer part of Pi's number, clamped to the int range (Infinity is the largest int). An
// input entry that is not a string is skipped. Any other member the Go model cannot represent (a cost tier that is not an object,
// headers that are not a string map) keeps its zero value instead of rejecting a model Pi accepts.
func radiusGatewayModelMembers(fields map[string]json.RawMessage) RadiusGatewayModel {
	var model RadiusGatewayModel
	value := reflect.ValueOf(&model).Elem()
	for i := range value.NumField() {
		name, _, _ := strings.Cut(value.Type().Field(i).Tag.Get("json"), ",")
		raw, present := fields[name]
		if !present {
			continue
		}
		field := value.Field(i)
		switch field.Kind() {
		case reflect.Int:
			if jsonKind(raw) == 'n' {
				field.SetInt(int64(jsNumberToInt(jsnumber.FromJSON(raw))))
			}
		case reflect.Slice:
			if field.Type().Elem().Kind() == reflect.String {
				var items []any
				if json.Unmarshal(raw, &items) == nil {
					strs := []string{}
					for _, item := range items {
						if text, ok := item.(string); ok {
							strs = append(strs, text)
						}
					}
					field.Set(reflect.ValueOf(strs))
					continue
				}
			}
			fallthrough
		default:
			decoded := reflect.New(field.Type())
			if json.Unmarshal(raw, decoded.Interface()) == nil {
				field.Set(decoded.Elem())
			}
		}
	}
	return model
}

// jsNumberToInt keeps the integer part of a JavaScript number, clamped to the int range. NaN is 0.
func jsNumberToInt(number float64) int {
	switch {
	case math.IsNaN(number):
		return 0
	case number >= math.MaxInt:
		return math.MaxInt
	case number <= math.MinInt:
		return math.MinInt
	}
	return int(number)
}

// sanitizeRadiusGatewayConfig mirrors upstream sanitizeRadiusGatewayConfig: it
// requires a string baseUrl and a models array, and drops invalid models. It reads
// baseUrl and models by their exact keys.
func sanitizeRadiusGatewayConfig(raw json.RawMessage) (RadiusGatewayConfig, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || jsonKind(fields["models"]) != '[' || jsonKind(fields["baseUrl"]) != '"' {
		return RadiusGatewayConfig{}, false
	}
	var candidates []json.RawMessage
	if json.Unmarshal(fields["models"], &candidates) != nil {
		return RadiusGatewayConfig{}, false
	}
	config := RadiusGatewayConfig{Models: []RadiusGatewayModel{}}
	if json.Unmarshal(fields["baseUrl"], &config.BaseURL) != nil {
		return RadiusGatewayConfig{}, false
	}
	for _, candidate := range candidates {
		if model, ok := decodeRadiusGatewayModel(candidate); ok {
			config.Models = append(config.Models, model)
		}
	}
	return config, true
}

// NormalizeRadiusGatewayURL adds an https scheme when absent and strips
// trailing slashes. Mirrors upstream normalizeRadiusGatewayUrl.
func NormalizeRadiusGatewayURL(value string) string {
	if !hasRadiusScheme(value) {
		value = "https://" + value
	}
	return strings.TrimRight(value, "/")
}

// hasRadiusScheme is radius-config.ts's /^https?:\/\//iu.test(value): each pattern character matches the characters in its simple case-folding orbit, so ſ matches s. It is not a regular expression because the OAuth registry normalizes the default gateway while the package initializes.
func hasRadiusScheme(value string) bool {
	rest, ok := cutFoldPrefix(value, "http")
	if !ok {
		return false
	}
	if after, ok := cutFoldPrefix(rest, "s"); ok {
		rest = after
	}
	return strings.HasPrefix(rest, "://")
}

// cutFoldPrefix removes from s a prefix that equals prefix under simple case folding, rune by rune.
func cutFoldPrefix(s, prefix string) (string, bool) {
	for _, want := range prefix {
		got, size := utf8.DecodeRuneInString(s)
		if size == 0 || !strings.EqualFold(string(got), string(want)) {
			return s, false
		}
		s = s[size:]
	}
	return s, true
}

// GetRadiusCredentialConfig returns the sanitized legacy gateway catalog that
// pre-ModelsStore Radius builds cached on the OAuth credential.
func GetRadiusCredentialConfig(credential *Credential) (RadiusGatewayConfig, bool) {
	if credential == nil || len(credential.GatewayConfig) == 0 {
		return RadiusGatewayConfig{}, false
	}
	return sanitizeRadiusGatewayConfig(credential.GatewayConfig)
}

// GetRadiusModelsFromConfig binds a gateway catalog to providerID: Model<"pi-messages">[] (radius-config.ts:61).
func GetRadiusModelsFromConfig(providerID string, config RadiusGatewayConfig) []*Model {
	models := make([]*Model, 0, len(config.Models))
	for _, model := range config.Models {
		models = append(models, piMessagesModel(providerID, config.BaseURL, model))
	}
	return models
}

// GetRadiusModels returns the legacy credential catalog bound to providerID (radius-config.ts:70).
func GetRadiusModels(providerID string, credential *Credential) []*Model {
	config, ok := GetRadiusCredentialConfig(credential)
	if !ok {
		return []*Model{}
	}
	return GetRadiusModelsFromConfig(providerID, config)
}

// piMessagesModel is the spread of getRadiusModelsFromConfig: the gateway model with api "pi-messages", the provider and the gateway base URL.
func piMessagesModel(providerID, baseURL string, gateway RadiusGatewayModel) *Model {
	return ownedPiMessagesModel(providerID, baseURL, cloneRadiusGatewayModel(gateway))
}

// ownedPiMessagesModel is piMessagesModel for a gateway model the caller owns: its slices and maps move into the model uncopied.
func ownedPiMessagesModel(providerID, baseURL string, gateway RadiusGatewayModel) *Model {
	return chatModelFromRecord(modelsCatalogRecord{
		Type: gateway.Type, ID: gateway.ID, Name: gateway.Name, API: APIPiMessages, Provider: providerID, BaseURL: baseURL,
		Reasoning: gateway.Reasoning, ThinkingLevelMap: gateway.ThinkingLevelMap, Input: gateway.Input, InputLimits: gateway.InputLimits,
		Cost: gateway.Cost, PromptCache: gateway.PromptCache, ContextWindow: gateway.ContextWindow, MaxTokens: gateway.MaxTokens,
		SamplingParams: gateway.SamplingParams, SamplingParamsByThinkingLevel: gateway.SamplingParamsByThinkingLevel,
		Headers: gateway.Headers, Compat: gateway.Compat,
	})
}

// radiusGatewayModel is the gateway-model view of a Model<"pi-messages">: a deep copy, so no alias reaches the caller.
func radiusGatewayModel(model *Model) RadiusGatewayModel {
	capabilities := model.Capabilities
	return cloneRadiusGatewayModel(RadiusGatewayModel{
		Type: model.Type, ID: model.ID, Name: model.DisplayName, Reasoning: model.ProviderMeta.Reasoning,
		ThinkingLevelMap: model.ThinkingLevelMap, Input: model.Input, InputLimits: model.InputLimits,
		Cost:        ModelCost{Input: capabilities.InputCostPer1M, Output: capabilities.OutputCostPer1M, CacheRead: capabilities.CacheReadCostPer1M, CacheWrite: capabilities.CacheWriteCostPer1M, Tiers: capabilities.CostTiers},
		PromptCache: model.PromptCache, ContextWindow: capabilities.ContextWindow, MaxTokens: capabilities.MaxOutputTokens,
		SamplingParams: model.SamplingParams, SamplingParamsByThinkingLevel: model.SamplingParamsByThinkingLevel,
		Headers: model.ProviderMeta.Headers, Compat: model.ProviderMeta.Compat,
	})
}

// cloneRadiusModel deep-copies a Model<"pi-messages">.
func cloneRadiusModel(model *Model) *Model {
	clone := ownedPiMessagesModel(model.ProviderMeta.ProviderID, model.ProviderMeta.BaseURL, radiusGatewayModel(model))
	// The record the model came from stays with it, so its RPC object keeps the record's members and order (CatalogModelJSON).
	clone.catalogRecord, clone.catalog = model.catalogRecord, model.catalog
	return clone
}

func cloneRadiusGatewayModel(model RadiusGatewayModel) RadiusGatewayModel {
	model.Input = append([]string(nil), model.Input...)
	model.ThinkingLevelMap = cloneThinkingLevelMap(model.ThinkingLevelMap)
	model.InputLimits = model.InputLimits.Clone()
	model.Cost.Tiers = slices.Clone(model.Cost.Tiers)
	model.PromptCache = maps.Clone(model.PromptCache)
	model.SamplingParams = maps.Clone(model.SamplingParams)
	if model.SamplingParamsByThinkingLevel != nil {
		levels := make(SamplingParamsByThinkingLevel, len(model.SamplingParamsByThinkingLevel))
		for level, params := range model.SamplingParamsByThinkingLevel {
			levels[level] = maps.Clone(params)
		}
		model.SamplingParamsByThinkingLevel = levels
	}
	model.Headers = cloneStringMap(model.Headers)
	model.Compat = cloneCompat(model.Compat)
	return model
}

func truncateHTTPBody(body string) string {
	trimmed := trimJSWhitespace(body)
	if utf16Length(trimmed) <= 512 {
		return trimmed
	}
	return truncateUTF16(trimmed, 512) + "…"
}

// utf16Length is JavaScript's string.length: UTF-16 units, with a lone surrogate carried as WTF-8 counting as one unit.
func utf16Length(value string) int {
	return jsstring.Length(value)
}

// truncateUTF16 is value.slice(0, limit) in UTF-16 units. A cut inside a surrogate pair keeps the lone high surrogate as WTF-8, as every PiG string carries a JavaScript string's unpaired unit.
func truncateUTF16(value string, limit int) string {
	return jsstring.Slice(value, 0, limit)
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
	request, err := newRadiusRequest(ctx, http.MethodGet, endpoint, nil)
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
	// response.text() and response.json() read the body as fetchBodyText decodes it.
	body = webTextBytes(body)
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

// resolveGatewayPath mirrors `new URL(path, gateway).toString()` for an absolute path in serialized form. Its error is the constructor's TypeError message, "Invalid URL".
func resolveGatewayPath(gateway, path string) (string, error) {
	base, err := nodeurl.ParseURL(gateway)
	if err != nil {
		return "", err
	}
	return base.ResolveAbsolutePath(path)
}

// newRadiusRequest is the Request that `fetch(new URL(path, gateway), init)` constructs. Like fetch, it refuses a URL with a username or password instead of sending
// them: the serialized href has userinfo exactly when the URL includes credentials.
func newRadiusRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if request.URL.User != nil {
		return nil, fmt.Errorf("Request cannot be constructed from a URL that includes credentials: %s", endpoint)
	}
	return request, nil
}
