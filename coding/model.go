package coding

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// BuildModel constructs an *ai.Model from a "provider/model" spec
// string, using the given Services for auth and registry lookup. The test-faux backend reuses one provider per Services container so tool-call counters survive request preparation.
//
// Recognised provider prefixes:
//
//   - "github-copilot/<model>"  : uses the OAuth token in auth.json
//   - "openai/<model>"          : OPENAI_API_KEY (or registry override)
//   - "openrouter/<model>"      : OPENROUTER_API_KEY
//   - "groq/<model>"            : GROQ_API_KEY
//   - "ollama/<model>"          : OLLAMA_HOST or http://localhost:11434/v1
//   - any other "<id>/<model>"  : treated as OpenAI-compatible;
//     API key from <ID_UPPER>_API_KEY env var
//
// A bare "<model>" with no slash is interpreted as "openai/<model>"
// for backward compatibility with environments that hardcode OpenAI.
//
// The catalog (context window, cost, capabilities) is resolved via
// ai.LookupModel; if the spec isn't in the codegen registry, the
// returned Model still works but has empty cost/window data
// (status-line just shows blank context %).
//
// Library consumers can use this to construct models without
// hand-rolling provider switching:
//
//	svcs, _ := coding.CreateAgentSessionServices(...)
//	defer svcs.Close()
//	model, _ := coding.BuildModel("github-copilot/gpt-4o-mini", svcs)
//	rt, _ := coding.NewRuntime(coding.RuntimeOptions{Services: svcs})
//	sess, _ := rt.New(coding.SessionStartOptions{Model: model})
func BuildModel(spec string, svcs *AgentSessionServices) (*ai.Model, error) {
	return buildModel(spec, svcs, "")
}

// buildModel builds spec's model; a non-empty apiKey replaces the resolved
// credential, as upstream providers use options.apiKey when one is given.
func buildModel(spec string, svcs *AgentSessionServices, apiKey string) (*ai.Model, error) {
	if svcs == nil {
		return nil, fmt.Errorf("coding: BuildModel: Services is required")
	}
	parts := strings.SplitN(spec, "/", 2)
	if len(parts) == 1 {
		// No provider prefix: assume openai for back-compat.
		parts = []string{"openai", parts[0]}
	}
	providerID := parts[0]
	modelID := parts[1]

	registry := svcs.Registry()
	if registry.GetProvider(providerID) != nil {
		model := svcs.ModelRuntime().GetModel(providerID, modelID)
		if model == nil {
			return nil, fmt.Errorf("model not found: %s/%s", providerID, modelID)
		}
		return model, nil
	}
	generated, hasGenerated := lookupGeneratedModel(providerID, modelID)
	var entry icodingagent.ModelEntry
	if hasGenerated {
		entry = registry.ResolveGeneratedModel(providerID, modelID, generated)
	} else {
		entry, _ = registry.Resolve(providerID, modelID)
	}
	return buildModelFromEntry(providerID, modelID, entry, svcs, apiKey)
}

// BuildModelFromEntry constructs the Model for an already-resolved entry. The
// CLI model resolver resolves the entry itself (catalog match, models.json, or
// the provider's default-model fallback) and shares this construction with
// BuildModel, so both paths branch on the API kind and carry the catalog base
// URL and credentials the same way.
func BuildModelFromEntry(providerID, modelID string, entry icodingagent.ModelEntry, svcs *AgentSessionServices) (*ai.Model, error) {
	return buildModelFromEntry(providerID, modelID, entry, svcs, "")
}

func buildModelFromEntry(providerID, modelID string, entry icodingagent.ModelEntry, svcs *AgentSessionServices, apiKey string) (*ai.Model, error) {
	if svcs == nil {
		return nil, fmt.Errorf("coding: BuildModelFromEntry: Services is required")
	}
	if model := BuildNativeModel(svcs.Registry().ModelRegistry, providerID, modelID); model != nil {
		return model, nil
	}
	entry.ProviderID = providerID
	entry.ModelID = modelID
	apiKind := ai.API(entry.API)
	provider, err := buildProviderForEntry(providerID, modelID, apiKind, entry, svcs, apiKey, false, false)
	if err != nil {
		return nil, err
	}
	provider = newRequestOptionsProvider(provider, providerID, entry.Headers)

	if providerID == "test-faux" {
		entry.Input = []string{"text", "image"}
		entry.ContextWindow = ai.TestFauxContextWindow
		entry.MaxTokens = ai.TestFauxMaxTokens
		entry.DisplayName = "Test Faux"
		entry.API = "test-faux"
		entry.BaseURL = "http://localhost:0"
	}
	return modelFromEntry(entry, provider), nil
}

func modelFromEntry(entry icodingagent.ModelEntry, provider ai.Provider) *ai.Model {
	displayName := entry.DisplayName
	if displayName == "" {
		displayName = entry.ModelID
	}
	input := append([]string(nil), entry.Input...)
	if entry.Input != nil {
		input = append([]string{}, entry.Input...)
	}
	// A built-in model starts from its catalog entry, so it keeps the entry's member order when it is encoded.
	model := &ai.Model{}
	if entry.CatalogRecord != "" {
		model = (&ai.GeneratedModel{Record: entry.CatalogRecord}).ToModel()
	}
	model.Type = entry.Type
	model.ID = entry.ModelID
	model.DisplayName = displayName
	model.Provider = provider
	model.Capabilities = ai.ModelCapabilities{
		MaxThinking:         thinkingMaxLevelForEntry(entry),
		SupportsImages:      slices.Contains(entry.Input, "image"),
		SupportsToolUse:     true,
		ContextWindow:       entry.ContextWindow,
		MaxOutputTokens:     entry.MaxTokens,
		InputCostPer1M:      entry.InputCost,
		OutputCostPer1M:     entry.OutputCost,
		CacheReadCostPer1M:  entry.CacheReadCost,
		CacheWriteCostPer1M: entry.CacheWriteCost,
		CostTiers:           slices.Clone(entry.CostTiers),
	}
	model.Input = input
	model.InputLimits = entry.InputLimits.Clone()
	model.ThinkingLevelMap = cloneThinkingLevelMap(entry.ThinkingLevelMap)
	model.SamplingParams = maps.Clone(entry.SamplingParams)
	model.SamplingParamsByThinkingLevel = cloneSamplingParamsByThinkingLevel(entry.SamplingParamsByThinkingLevel)
	model.PromptCache = maps.Clone(entry.PromptCache)
	model.ProviderMeta = ai.ProviderMetadata{
		ProviderID: entry.ProviderID,
		API:        ai.API(entry.API),
		BaseURL:    entry.BaseURL,
		Headers:    cloneStringMap(entry.ModelHeaders),
		Compat:     cloneCompat(entry.Compat),
		Reasoning:  entry.Reasoning,
	}
	return model
}

func thinkingMaxLevelForEntry(entry icodingagent.ModelEntry) ai.ThinkingLevel {
	if !entry.Reasoning {
		return ""
	}
	model := &ai.Model{
		Capabilities:     ai.ModelCapabilities{MaxThinking: ai.ThinkingLevelHigh},
		ThinkingLevelMap: entry.ThinkingLevelMap,
	}
	levels := ai.GetSupportedThinkingLevels(model)
	return ai.ThinkingLevel(levels[len(levels)-1])
}

func lookupGeneratedModel(providerID, modelID string) (*ai.GeneratedModel, bool) {
	if generated, ok := ai.LookupModel(providerID + "/" + modelID); ok {
		return generated, true
	}
	return ai.LookupModel(modelID)
}

// buildProviderForEntry builds the provider for entry. A non-empty explicitKey
// is upstream's options.apiKey: it owns the request ahead of runtime, stored,
// configured, and environment credentials. The Anthropic leaf retains the selected model's reasoning metadata and limits instead of resolving it again from the built-in catalog.
// apiLeaf selects the pi-ai API implementation for apiKind itself. Pi's compat stream() and streamSimple() dispatch on model.api through the api registry and never consult the provider an extension registered under the model's provider id (packages/ai/src/compat.ts:278-293, stream at 252), so an API leaf must not re-enter that provider's streamSimple callback.
// A built-in provider whose upstream constructor wraps its API streams (cloudflareStreams for the Cloudflare providers) has the wrapper
// applied, as when its models run through the registered provider. An API leaf is the API implementation alone, which sends the model's
// endpoint as given (openai-completions.ts:795), so it is not wrapped.
func buildProviderForEntry(providerID, modelID string, apiKind ai.API, entry icodingagent.ModelEntry, svcs *AgentSessionServices, explicitKey string, resolvedAuth, apiLeaf bool) (ai.Provider, error) {
	provider, err := buildLeafProviderForEntry(providerID, modelID, apiKind, entry, svcs, explicitKey, resolvedAuth, apiLeaf)
	if err != nil {
		return nil, err
	}
	if apiLeaf {
		return provider, nil
	}
	return ai.WrapBuiltinProvider(providerID, provider), nil
}

func buildLeafProviderForEntry(providerID, modelID string, apiKind ai.API, entry icodingagent.ModelEntry, svcs *AgentSessionServices, explicitKey string, resolvedAuth, apiLeaf bool) (ai.Provider, error) {
	apiKey := entry.APIKey
	if explicitKey != "" {
		apiKey = explicitKey
	}
	baseURL := entry.BaseURL
	extraHeaders := cloneStringMap(entry.Headers)

	if providerID == "test-faux" && !resolvedAuth {
		if os.Getenv("PIG_TEST_FAUX") != "1" {
			return nil, fmt.Errorf("coding: BuildModel: test-faux is disabled")
		}
		return svcs.testFauxProvider(), nil
	}

	if providerID == "github-copilot" && !resolvedAuth {
		// Re-open auth from disk so this provider has its own handle
		// (the Services-owned handle is shared and may be in-use).
		auth, err := ai.NewAuthStorage(filepath.Join(svcs.AgentDir(), "auth.json"))
		if err != nil {
			return nil, fmt.Errorf("coding: BuildModel: github-copilot auth: %w", err)
		}
		return ai.NewCopilotProvider(ai.CopilotProviderConfig{
			Auth:      auth,
			Model:     modelID,
			API:       apiKind,
			Reasoning: entry.Reasoning,
			// EnvToken carries the env-resolved API key (entry.APIKey is set
			// from COPILOT_GITHUB_TOKEN for github-copilot)
			// so the provider can fall back to it when auth.json has no OAuth
			// credential, matching upstream auth-storage.getApiKey.
			EnvToken: entry.APIKey,
			// A resolved key sends the request model's base URL, which the
			// ModelRuntime sets from the credential's derived auth base URL.
			BaseURL:       baseURL,
			ModelMetadata: modelFromEntry(entry, nil),
			RuntimeToken: func() (string, bool) {
				if explicitKey != "" {
					return explicitKey, true
				}
				return svcs.Registry().RuntimeAPIKey(providerID)
			},
		})
	}

	// A runtime or stored credential owns the provider ahead of the static
	// registry or env key resolved above. Providers that accept a per-request
	// key callback resolve it there so an OAuth refresh follows the request
	// context; the others resolve it here, once per BuildModel.
	resolveAPIKey := requestAPIKey(svcs, providerID, apiKey)
	if explicitKey != "" || resolvedAuth {
		resolveAPIKey = func(context.Context) (string, error) { return explicitKey, nil }
	}
	if callback := svcs.Registry().ProviderStreamSimple(providerID, apiKind); callback != nil && !apiLeaf {
		provider := &registeredStreamProvider{id: providerID, streamSimple: callback, apiKey: resolveAPIKey}
		provider.model = modelFromEntry(entry, provider)
		return provider, nil
	}
	if !acceptsRequestAPIKey(apiKind) {
		var err error
		if apiKey, err = resolveAPIKey(context.Background()); err != nil {
			return nil, fmt.Errorf("coding: BuildModel: %s: %w", providerID, err)
		}
	}
	switch apiKind {
	case ai.APIOpenAICompletions:
		if providerID == "ollama" && baseURL == "" {
			baseURL = firstNonEmpty(os.Getenv("OLLAMA_HOST"), "http://localhost:11434/v1")
		}
		return ai.NewOpenAIProvider(ai.OpenAIConfig{
			ModelMetadata:                 modelFromEntry(entry, nil),
			BaseURL:                       baseURL,
			APIKey:                        apiKey,
			GetAPIKey:                     resolveAPIKey,
			Model:                         modelID,
			ProviderID:                    providerID,
			ExtraHeaders:                  extraHeaders,
			SamplingParams:                maps.Clone(entry.SamplingParams),
			SamplingParamsByThinkingLevel: cloneSamplingParamsByThinkingLevel(entry.SamplingParamsByThinkingLevel),
			Env:                           ai.ProviderEnv(maps.Clone(entry.Env)),
			Compat:                        cloneCompat(entry.Compat),
			Insecure:                      entry.Insecure,
		}), nil
	case ai.APIOpenAIResponses:
		return ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{
			ModelMetadata:                 modelFromEntry(entry, nil),
			BaseURL:                       baseURL,
			APIKey:                        apiKey,
			GetAPIKey:                     resolveAPIKey,
			Model:                         modelID,
			ProviderID:                    providerID,
			ExtraHeaders:                  extraHeaders,
			SamplingParams:                maps.Clone(entry.SamplingParams),
			SamplingParamsByThinkingLevel: cloneSamplingParamsByThinkingLevel(entry.SamplingParamsByThinkingLevel),
			Env:                           ai.ProviderEnv(maps.Clone(entry.Env)),
			Compat:                        cloneCompat(entry.Compat),
			IsReasoning:                   entry.Reasoning,
		}), nil
	case ai.APIOpenAICodexResponses:
		return ai.NewOpenAICodexResponsesProvider(ai.OpenAICodexResponsesConfig{
			ModelMetadata: modelFromEntry(entry, nil),
			Compat:        cloneCompat(entry.Compat),
			BaseURL:       baseURL,
			APIKey:        apiKey,
			Model:         modelID,
			ProviderID:    providerID,
			ExtraHeaders:  extraHeaders,
		}), nil
	case ai.APIAzureOpenAIResponses:
		return ai.NewAzureOpenAIResponsesProvider(ai.AzureOpenAIResponsesConfig{
			ModelMetadata:                 modelFromEntry(entry, nil),
			Compat:                        cloneCompat(entry.Compat),
			BaseURL:                       baseURL,
			APIKey:                        apiKey,
			Model:                         modelID,
			ProviderID:                    providerID,
			ExtraHeaders:                  extraHeaders,
			SamplingParams:                maps.Clone(entry.SamplingParams),
			SamplingParamsByThinkingLevel: cloneSamplingParamsByThinkingLevel(entry.SamplingParamsByThinkingLevel),
			Env:                           ai.ProviderEnv(maps.Clone(entry.Env)),
			IsReasoning:                   entry.Reasoning,
		}), nil
	case ai.APIAnthropicMessages:
		config := ai.AnthropicConfig{
			ModelMetadata: modelFromEntry(entry, nil),
			BaseURL:       baseURL,
			APIKey:        apiKey,
			Model:         modelID,
			ProviderID:    providerID,
			ExtraHeaders:  extraHeaders,
			Compat:        cloneCompat(entry.Compat),
			GetAPIKey:     resolveAPIKey,
			Env:           ai.ProviderEnv(maps.Clone(entry.Env)),
		}
		return ai.NewAnthropicProvider(config), nil
	case ai.APIGoogleGenerativeAI:
		return ai.NewGoogleProvider(ai.GoogleConfig{
			ModelMetadata:    modelFromEntry(entry, nil),
			BaseURL:          baseURL,
			APIKey:           apiKey,
			Model:            modelID,
			ProviderID:       providerID,
			APIVersion:       googleAPIVersionForBaseURL(baseURL),
			ExtraHeaders:     extraHeaders,
			ThinkingLevelMap: cloneThinkingLevelMap(entry.ThinkingLevelMap),
		}), nil
	case ai.APIGoogleVertex:
		// pig additive (D92): the google-vertex, bedrock-converse-stream and mistral-conversations constructors report a stripped API (strip.apis).
		return ai.NewGoogleVertexAPIProvider(ai.GoogleVertexConfig{
			ModelMetadata:    modelFromEntry(entry, nil),
			BaseURL:          baseURL,
			APIKey:           apiKey,
			Model:            modelID,
			ProviderID:       providerID,
			Headers:          extraHeaders,
			ThinkingLevelMap: cloneThinkingLevelMap(entry.ThinkingLevelMap),
		})
	case ai.APIBedrockConverseStream:
		// Amazon Bedrock uses AWS native auth (SigV4 via the default
		// credentials chain, or AWS_BEARER_TOKEN_BEDROCK). No API key
		// from env or registry is required at construction time; the
		// AWS SDK resolves credentials at request time. The selected model
		// carries the configured provider identity (bedrock-converse-stream.ts:130
		// reports model.provider), the base URL and the limits.
		return ai.NewBedrockAPIProvider(*modelFromEntry(entry, nil))
	case ai.APIMistralConversations:
		return ai.NewMistralAPIProvider(ai.MistralConfig{
			ModelMetadata: modelFromEntry(entry, nil),
			BaseURL:       baseURL,
			APIKey:        apiKey,
			Model:         modelID,
			ProviderID:    providerID,
			ExtraHeaders:  extraHeaders,
			Reasoning:     entry.Reasoning,
		})
	case ai.APIPiMessages:
		config := ai.PiMessagesConfig{
			ModelMetadata: modelFromEntry(entry, nil),
			BaseURL:       baseURL,
			APIKey:        apiKey,
			Model:         modelID,
			ProviderID:    providerID,
			ExtraHeaders:  extraHeaders,
		}
		if _, radius := svcs.Registry().RadiusOAuth(providerID); radius && !resolvedAuth {
			registry := svcs.Registry().ModelRegistry
			config.GetAPIKey = func(ctx context.Context) (string, error) { return registry.RadiusAPIKey(ctx, providerID) }
		} else {
			config.GetAPIKey = resolveAPIKey
		}
		return ai.NewPiMessagesProvider(config), nil
	default:
		return ai.NewOpenAIProvider(ai.OpenAIConfig{
			ModelMetadata:                 modelFromEntry(entry, nil),
			BaseURL:                       baseURL,
			APIKey:                        apiKey,
			GetAPIKey:                     resolveAPIKey,
			Model:                         modelID,
			ProviderID:                    providerID,
			ExtraHeaders:                  extraHeaders,
			SamplingParams:                maps.Clone(entry.SamplingParams),
			SamplingParamsByThinkingLevel: cloneSamplingParamsByThinkingLevel(entry.SamplingParamsByThinkingLevel),
			Env:                           ai.ProviderEnv(maps.Clone(entry.Env)),
			Compat:                        cloneCompat(entry.Compat),
			Insecure:                      entry.Insecure,
		}), nil
	}
}

// acceptsRequestAPIKey reports whether BuildModel's provider for apiKind
// resolves its key through a per-request callback.
func acceptsRequestAPIKey(apiKind ai.API) bool {
	switch apiKind {
	case ai.APIOpenAICodexResponses, ai.APIAzureOpenAIResponses, ai.APIGoogleGenerativeAI, ai.APIGoogleVertex, ai.APIBedrockConverseStream, ai.APIMistralConversations:
		return false
	default:
		return true
	}
}

// requestAPIKey resolves a provider's request key: a non-persistent runtime
// key (--api-key) first, then a stored auth.json credential, then fallback,
// the configured or environment key. Mirrors upstream resolveProviderAuth
// over RuntimeCredentials.
func requestAPIKey(svcs *AgentSessionServices, providerID, fallback string) func(context.Context) (string, error) {
	stored := storedAPIKey(filepath.Join(svcs.AgentDir(), "auth.json"), providerID, fallback)
	registry := svcs.Registry().ModelRegistry
	return func(ctx context.Context) (string, error) {
		if key, ok := registry.RuntimeAPIKey(providerID); ok {
			return key, nil
		}
		return stored(ctx)
	}
}

// storedAPIKey resolves request auth per request the way upstream
// resolveProviderAuth does: a stored OAuth login (refreshed when expiring) or
// stored api_key credential owns the provider, ahead of the configured or
// environment key fallback. An unopenable auth store yields the fallback; a
// failed read or refresh is returned to the request.
func storedAPIKey(authPath, providerID, fallback string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		auth, err := ai.NewAuthStorage(authPath)
		if err != nil {
			return fallback, nil
		}
		key, ok, err := ai.ResolveStoredAPIKeyFromStorageContext(ctx, auth, providerID)
		if err != nil || ok {
			return key, err
		}
		return fallback, nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func googleAPIVersionForBaseURL(baseURL string) string {
	trimmed := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(trimmed, "/v1") || strings.HasSuffix(trimmed, "/v1beta") {
		return ""
	}
	return "v1beta"
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func cloneCompat(in *ai.ModelCompat) *ai.ModelCompat {
	if in == nil {
		return nil
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out ai.ModelCompat
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return &out
}

// cloneSamplingParamsByThinkingLevel copies each level's sampling parameters.
func cloneSamplingParamsByThinkingLevel(in ai.SamplingParamsByThinkingLevel) ai.SamplingParamsByThinkingLevel {
	if in == nil {
		return nil
	}
	out := make(ai.SamplingParamsByThinkingLevel, len(in))
	for level, params := range in {
		out[level] = maps.Clone(params)
	}
	return out
}

func cloneThinkingLevelMap(in ai.ThinkingLevelMap) ai.ThinkingLevelMap {
	if len(in) == 0 {
		return nil
	}
	out := make(ai.ThinkingLevelMap, len(in))
	for k, v := range in {
		if v == nil {
			out[k] = nil
			continue
		}
		value := *v
		out[k] = &value
	}
	return out
}

// silence "imported and not used" if any sub-helper drifts; keeps
// icodingagent referenceable from this file as the source-of-truth
// import path for tests using ModelEntry (settings registry).
var _ = icodingagent.ModelEntry{}

// Stream delegates to the Services-owned ModelRuntime.
func (registry *ModelRegistry) Stream(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessageEventStream {
	return registry.runtime.Stream(ctx, model, request, options)
}

// StreamSimple delegates to the same runtime preparation and streaming path.
func (registry *ModelRegistry) StreamSimple(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessageEventStream {
	return registry.runtime.StreamSimple(ctx, model, request, options)
}

// Complete delegates to ModelRuntime.Complete and returns its terminal pointer.
func (registry *ModelRegistry) Complete(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessage {
	return registry.runtime.Complete(ctx, model, request, options)
}

// requestOptionsProvider wraps a built provider with the request preparation ModelRuntime applies to a registry-built provider.
type requestOptionsProvider struct {
	ai.Provider
	providerID string
	configured map[string]string
}

func newRequestOptionsProvider(provider ai.Provider, providerID string, configured map[string]string) ai.Provider {
	return &requestOptionsProvider{Provider: provider, providerID: providerID, configured: maps.Clone(configured)}
}

// SimpleStreamOptions prepares the request options of a simple stream the way ModelRuntime.StreamSimple does for a registry-built provider (start): an omitted reasoning level sends the API's native thinking off, and the output budget stays within the room the context window leaves (simple-options.ts buildBaseOptions). A provider of extension-registered stream callbacks or a native provider prepares its own options.
func (provider *requestOptionsProvider) SimpleStreamOptions(model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) ai.StreamOptions {
	switch provider.Provider.(type) {
	case *registeredStreamProvider, *nativeModelProvider:
		return options
	}
	ai.ApplyOmittedReasoning(model, &options)
	options.MaxTokens = ai.ClampMaxTokensToContext(model, transcript, cmp.Or(options.MaxTokens, model.Capabilities.MaxOutputTokens))
	return options
}

// Stream merges the configured headers under the request's and runs the request's header transform (before_provider_headers) on the result, as ModelRuntime applyAuth does for a registry-built provider. The built-in OpenCode providers add the session header below both, as providers/opencode.ts withOpenCodeSessionHeader does; a direct pi-ai call that the extension bridge streams through BuildModel carries no attribution, as under Pi.
func (provider *requestOptionsProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	configured := ai.ProviderHeadersFromStrings(provider.configured)
	configured = ai.MergeProviderHeaders(ai.GetOpenCodeSessionHeaders(ai.ClientIdentityTarget{Provider: provider.providerID}, options.SessionID), configured)
	options.Headers = ai.MergeProviderHeaders(configured, options.Headers)
	// The request's header transform (before_provider_headers) runs on the merged headers.
	if options.TransformHeaders != nil {
		if options.Headers == nil {
			options.Headers = ai.ProviderHeaders{}
		}
		transformed, err := options.TransformHeaders(ctx, options.Headers)
		if err != nil {
			return nil, err
		}
		options.Headers = transformed
		options.TransformHeaders = nil
	}
	return provider.Provider.Stream(ctx, transcript, options)
}

// withOpenCodeSessionHeaders adds OpenCode's per-conversation routing header, including for custom providers pointing at OpenCode, below the auth and request headers and before the request's header transform sees them (model-runtime.ts applyAuth getOpenCodeSessionHeaders).
func withOpenCodeSessionHeaders(providerID, baseURL, sessionID string, headers ai.ProviderHeaders) ai.ProviderHeaders {
	return ai.MergeProviderHeaders(ai.GetOpenCodeSessionHeaders(ai.ClientIdentityTarget{Provider: providerID, BaseURL: baseURL}, sessionID), headers)
}
