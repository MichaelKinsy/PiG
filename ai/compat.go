package ai

// Ports packages/ai/src/compat.ts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// StreamFunction is a provider stream for one request (types.ts StreamFunction). Its options are the one StreamOptions.
type StreamFunction = ModelsStreamFunction

// APIStreamFunction is a registered API's stream (compat.ts ApiStreamFunction).
type APIStreamFunction = ModelsStreamFunction

// APIStreamSimpleFunction is a registered API's simple stream (compat.ts ApiStreamSimpleFunction).
type APIStreamSimpleFunction = ModelsStreamFunction

// APIProvider is one API implementation in the global API registry (compat.ts ApiProvider).
type APIProvider struct {
	API          API
	Stream       StreamFunction
	StreamSimple StreamFunction
}

type registeredAPIProvider struct {
	provider *APIProvider
	sourceID string
	hasSrc   bool
}

// compatRegistry is compat.ts's apiProviderRegistry: a Map, so an entry keeps its position when its API is registered again.
var compatRegistry struct {
	mu        sync.Mutex
	order     []API
	entries   map[API]*registeredAPIProvider
	builtins  map[API]*APIProvider
	compatMod *Models
}

// mismatchedAPIError is the error the registry's wrapped streams return for a model of another API (compat.ts wrapStream).
func mismatchedAPIError(model *Model, api API) error {
	return fmt.Errorf("Mismatched api: %s expected %s", model.ProviderMeta.API, api)
}

func wrapAPIStream(api API, stream StreamFunction) StreamFunction {
	return func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
		if model.ProviderMeta.API != api {
			return nil, mismatchedAPIError(model, api)
		}
		return stream(ctx, model, transcript, options)
	}
}

// RegisterAPIProvider registers an API implementation under an optional source id, replacing any earlier one for the same API (compat.ts registerApiProvider). The registry holds a wrapped copy whose streams reject a model of another API.
// The built-in implementations register first, as compat.ts registers them when it loads, so an override replaces the built-in.
func RegisterAPIProvider(provider APIProvider, sourceID ...string) {
	compatRegistry.mu.Lock()
	defer compatRegistry.mu.Unlock()
	ensureBuiltInAPIProvidersLocked()
	registerAPIProviderLocked(provider, sourceID...)
}

func registerAPIProviderLocked(provider APIProvider, sourceID ...string) {
	if compatRegistry.entries == nil {
		compatRegistry.entries = map[API]*registeredAPIProvider{}
		compatRegistry.builtins = map[API]*APIProvider{}
	}
	entry := &registeredAPIProvider{provider: &APIProvider{API: provider.API, Stream: wrapAPIStream(provider.API, provider.Stream), StreamSimple: wrapAPIStream(provider.API, provider.StreamSimple)}}
	if len(sourceID) > 0 {
		entry.sourceID, entry.hasSrc = sourceID[0], true
	}
	if _, exists := compatRegistry.entries[provider.API]; !exists {
		compatRegistry.order = append(compatRegistry.order, provider.API)
	}
	compatRegistry.entries[provider.API] = entry
}

// GetAPIProvider returns the registered implementation of an API, or nil (compat.ts getApiProvider).
func GetAPIProvider(api API) *APIProvider {
	compatRegistry.mu.Lock()
	defer compatRegistry.mu.Unlock()
	return getAPIProviderLocked(api)
}

func getAPIProviderLocked(api API) *APIProvider {
	ensureBuiltInAPIProvidersLocked()
	if entry := compatRegistry.entries[api]; entry != nil {
		return entry.provider
	}
	return nil
}

// GetAPIProviders lists the registered implementations in registration order (compat.ts getApiProviders).
func GetAPIProviders() []*APIProvider {
	compatRegistry.mu.Lock()
	defer compatRegistry.mu.Unlock()
	ensureBuiltInAPIProvidersLocked()
	out := make([]*APIProvider, 0, len(compatRegistry.order))
	for _, api := range compatRegistry.order {
		out = append(out, compatRegistry.entries[api].provider)
	}
	return out
}

// UnregisterAPIProviders removes every implementation registered under a source id (compat.ts unregisterApiProviders).
func UnregisterAPIProviders(sourceID string) {
	compatRegistry.mu.Lock()
	defer compatRegistry.mu.Unlock()
	ensureBuiltInAPIProvidersLocked()
	for _, api := range slices.Clone(compatRegistry.order) {
		if entry := compatRegistry.entries[api]; entry.hasSrc && entry.sourceID == sourceID {
			delete(compatRegistry.entries, api)
			compatRegistry.order = slices.DeleteFunc(compatRegistry.order, func(candidate API) bool { return candidate == api })
		}
	}
}

// builtinAPIs lists the built-in APIs in compat.ts BUILTIN_APIS order.
var builtinAPIs = []struct {
	api     API
	streams func() *ProviderStreams
}{
	{APIAnthropicMessages, AnthropicMessagesAPI},
	{APIOpenAICompletions, OpenAICompletionsAPI},
	{APIOpenAIResponses, OpenAIResponsesAPI},
	{APIOpenAICodexResponses, OpenAICodexResponsesAPI},
	{APIAzureOpenAIResponses, AzureOpenAIResponsesAPI},
	{APIGoogleGenerativeAI, GoogleGenerativeAIAPI},
	{APIGoogleVertex, GoogleVertexAPI},
	{APIMistralConversations, MistralConversationsAPI},
	{APIBedrockConverseStream, BedrockConverseStreamAPI},
	{APIPiMessages, PiMessagesAPI},
}

// RegisterBuiltInAPIProviders registers the built-in API implementations without replacing an entry that is already registered, so an override registered earlier survives (compat.ts registerBuiltInApiProviders). The registry runs it once at first use, as compat.ts runs it on import.
func RegisterBuiltInAPIProviders() {
	compatRegistry.mu.Lock()
	defer compatRegistry.mu.Unlock()
	ensureBuiltInAPIProvidersLocked()
	registerBuiltInAPIProvidersLocked()
}

func registerBuiltInAPIProvidersLocked() {
	if compatRegistry.entries == nil {
		compatRegistry.entries = map[API]*registeredAPIProvider{}
		compatRegistry.builtins = map[API]*APIProvider{}
	}
	for _, builtin := range builtinAPIs {
		if compatRegistry.entries[builtin.api] == nil {
			streams := builtin.streams()
			registerAPIProviderLocked(APIProvider{API: builtin.api, Stream: streams.Stream, StreamSimple: streams.StreamSimple})
		}
		compatRegistry.builtins[builtin.api] = compatRegistry.entries[builtin.api].provider
	}
}

var compatBuiltInsRegistered bool

func ensureBuiltInAPIProvidersLocked() {
	if !compatBuiltInsRegistered {
		compatBuiltInsRegistered = true
		registerBuiltInAPIProvidersLocked()
	}
}

// ResetAPIProviders clears the registry and registers the built-in implementations again (compat.ts resetApiProviders).
func ResetAPIProviders() {
	compatRegistry.mu.Lock()
	defer compatRegistry.mu.Unlock()
	compatBuiltInsRegistered = true
	compatRegistry.order = nil
	compatRegistry.entries = map[API]*registeredAPIProvider{}
	compatRegistry.builtins = map[API]*APIProvider{}
	registerBuiltInAPIProvidersLocked()
}

func compatModels() *Models {
	compatRegistry.mu.Lock()
	defer compatRegistry.mu.Unlock()
	if compatRegistry.compatMod == nil {
		compatRegistry.compatMod = BuiltinModels()
	}
	return compatRegistry.compatMod
}

func hasExplicitAPIKey(apiKey string) bool { return strings.TrimSpace(apiKey) != "" }

// withEnvAPIKey fills the request key from the provider's environment variables unless the request has one, as compat.ts withEnvApiKey does. An ambient-authentication marker is not a key.
func withEnvAPIKey(model *Model, options StreamOptions) StreamOptions {
	if hasExplicitAPIKey(options.APIKey) {
		return options
	}
	apiKey := GetEnvAPIKey(modelProviderID(model), options.Env)
	if apiKey == "" || apiKey == envAuthenticated {
		return options
	}
	options.APIKey = apiKey
	return options
}

func hasResolvedCloudflareAuth(options StreamOptions) bool {
	return hasExplicitAPIKey(options.APIKey) || options.Headers["cf-aig-authorization"] != nil
}

// builtinProviderForModel returns the catalog provider that serves a model through the built-in API implementation, or nil when the API's registered implementation was replaced.
func builtinProviderForModel(model *Model) *ModelsProvider {
	compatRegistry.mu.Lock()
	ensureBuiltInAPIProvidersLocked()
	current, builtin := getAPIProviderLocked(model.ProviderMeta.API), compatRegistry.builtins[model.ProviderMeta.API]
	compatRegistry.mu.Unlock()
	if current != builtin {
		return nil
	}
	provider := compatModels().GetProvider(modelProviderID(model))
	if provider == nil || provider.GetModels == nil {
		return nil
	}
	models, err := provider.GetModels()
	if err != nil || !slices.ContainsFunc(models, func(candidate *Model) bool { return candidate.ProviderMeta.API == model.ProviderMeta.API }) {
		return nil
	}
	return provider
}

func resolveAPIProvider(api API) (*APIProvider, error) {
	provider := GetAPIProvider(api)
	if provider == nil {
		return nil, fmt.Errorf("No API provider registered for api: %s", api)
	}
	return provider, nil
}

func compatStream(ctx context.Context, model *Model, request Context, simple bool, options []StreamOptions) (*AssistantMessageEventStream, error) {
	if model == nil {
		return nil, errors.New("model is nil")
	}
	var opts StreamOptions
	if len(options) > 0 {
		opts = options[0]
	}
	transcript := NormalizeContext(request)
	if transcript.err != nil {
		return nil, transcript.err
	}
	if builtin := builtinProviderForModel(model); builtin != nil {
		// upstream: packages/ai/src/compat.ts:stream special-cases the cloudflare- provider prefix: without a resolved key or gateway header the catalog's own auth resolution runs.
		if strings.HasPrefix(modelProviderID(model), "cloudflare-") && !hasResolvedCloudflareAuth(opts) {
			if simple {
				return compatModels().StreamSimple(ctx, model, request, opts), nil
			}
			return compatModels().Stream(ctx, model, request, opts), nil
		}
		stream := builtin.Stream
		if simple {
			stream = builtin.StreamSimple
		}
		return stream(ctx, model, transcript, withEnvAPIKey(model, opts))
	}
	provider, err := resolveAPIProvider(model.ProviderMeta.API)
	if err != nil {
		return nil, err
	}
	stream := provider.Stream
	if simple {
		stream = provider.StreamSimple
	}
	return stream(ctx, model, transcript, withEnvAPIKey(model, opts))
}

// Stream sends a request through the global API registry, filling the API key from the environment (compat.ts stream). A model whose API has no registered implementation is an error.
func Stream(ctx context.Context, model *Model, request Context, options ...StreamOptions) (*AssistantMessageEventStream, error) {
	return compatStream(ctx, model, request, false, options)
}

// Complete is Stream followed by its final message (compat.ts complete).
func Complete(ctx context.Context, model *Model, request Context, options ...StreamOptions) (*AssistantMessage, error) {
	stream, err := Stream(ctx, model, request, options...)
	if err != nil {
		return nil, err
	}
	return stream.Result(), nil
}

// StreamSimpleContext is compat.ts streamSimple: Stream through the registered simple stream function of the model's API. It takes a Context as Pi's does; StreamSimple is the direct API implementation, which takes the normalized transcript.
func StreamSimpleContext(ctx context.Context, model *Model, request Context, options ...StreamOptions) (*AssistantMessageEventStream, error) {
	return compatStream(ctx, model, request, true, options)
}

// CompleteSimple is the simple-options form of Complete (compat.ts completeSimple).
func CompleteSimple(ctx context.Context, model *Model, request Context, options ...StreamOptions) (*AssistantMessage, error) {
	stream, err := StreamSimpleContext(ctx, model, request, options...)
	if err != nil {
		return nil, err
	}
	return stream.Result(), nil
}

// GetBuiltinModel reads one built-in chat model, or nil (providers/all.ts getBuiltinModel; compat.ts getModel).
func GetBuiltinModel(provider, modelID string) *Model {
	for _, model := range GetBuiltinModels(provider) {
		if model.ID == modelID {
			return model
		}
	}
	return nil
}
