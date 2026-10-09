package ai

import (
	"context"
	"fmt"
	"strings"
)

// Ports packages/ai/src/api/anthropic-messages.ts
// Ports packages/ai/src/api/azure-openai-responses.ts
// Ports packages/ai/src/api/bedrock-converse-stream.ts
// Ports packages/ai/src/api/google-generative-ai.ts
// Ports packages/ai/src/api/mistral-conversations.ts
// Ports packages/ai/src/api/openai-codex-responses.ts
// Ports packages/ai/src/api/openai-completions.ts
// Ports packages/ai/src/api/openai-responses.ts

// StreamSimple invokes a direct API implementation with model metadata and simple options. Omitted Anthropic reasoning explicitly disables thinking where the selected model permits it. Bedrock leaves authentication to the AWS credential chain or an optional bearer token, and Google Vertex to Application Default Credentials or an express-mode key. Other APIs reject missing request authentication before an event stream exists.
func StreamSimple(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	if model == nil {
		return nil, fmt.Errorf("model is nil")
	}
	return streamSimpleAs(ctx, model.ProviderMeta.API, model, transcript, options)
}

// streamSimpleAs is the simple stream of one API for a model: the API module's streamSimple builds its request from the model's
// fields whatever the model's own api says, so the legacy per-API aliases pass their API explicitly.
func streamSimpleAs(ctx context.Context, api API, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	meta := model.ProviderMeta
	meta.API = api
	key, err := directSimpleAPIKey(meta, options)
	if err != nil {
		return nil, err
	}
	provider, err := directAPIProviderFor(api, model, key, options.Env)
	if err != nil {
		return nil, err
	}
	options.IsReasoning = model.ProviderMeta.Reasoning
	if api == APIMistralConversations {
		// upstream: mistral-conversations.ts:streamSimple builds its MistralOptions from the base options and the reasoning level only: the raw promptMode and reasoningEffort of MistralOptions are not read.
		options.PromptMode, options.ReasoningEffort = "", ""
	}
	applyOmittedReasoningFor(api, &options)
	options.ModelCost = model.CostRates()
	return provider.Stream(ctx, transcript, BuildBaseOptions(model, transcript, options, ""))
}

func directSimpleAPIKey(meta ProviderMetadata, options StreamOptions) (string, error) {
	if options.APIKey != "" {
		return options.APIKey, nil
	}
	allowed := []string{}
	switch meta.API {
	case APIBedrockConverseStream:
		// upstream: packages/ai/src/api/bedrock-converse-stream.ts:streamSimple
		return "", nil
	case APIGoogleVertex:
		// upstream: packages/ai/src/api/google-vertex.ts:313-321 (streamSimple asserts no key) and :430-434 (resolveApiKey: no key selects Application Default Credentials)
		return "", nil
	case APIAnthropicMessages:
		allowed = []string{"authorization", "x-api-key", "cf-aig-authorization"}
	case APIOpenAICompletions, APIOpenAIResponses:
		allowed = []string{"authorization", "cf-aig-authorization"}
	}
	for name, value := range options.Headers {
		if value == nil || trimJSWhitespace(*value) == "" {
			continue
		}
		for _, accepted := range allowed {
			if strings.EqualFold(name, accepted) {
				if meta.API == APIAnthropicMessages {
					return "", nil
				}
				return "unused", nil
			}
		}
	}
	// upstream: packages/ai/src/api/anthropic-messages.ts:935-937 (streamSimple skips the assertion for federation)
	if meta.API == APIAnthropicMessages && getAnthropicFederation(meta.ProviderID, "", anthropicHeadersFromProviderHeaders(options.Headers), options.Env) != nil {
		return "", nil
	}
	return "", fmt.Errorf("No API key for provider: %s", meta.ProviderID)
}

func directAPIProvider(model *Model, key string, env ProviderEnv) (Provider, error) {
	return directAPIProviderFor(model.ProviderMeta.API, model, key, env)
}

// directAPIProviderFor builds the provider of api for the model's fields (base URL, headers, compat), whatever the model's own api says.
func directAPIProviderFor(api API, model *Model, key string, env ProviderEnv) (Provider, error) {
	meta := model.ProviderMeta
	switch api {
	case APIAnthropicMessages:
		return NewAnthropicProvider(AnthropicConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, ExtraHeaders: meta.Headers, Compat: meta.Compat, Env: env}), nil
	case APIOpenAICompletions:
		return NewOpenAIProvider(OpenAIConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, ExtraHeaders: meta.Headers, Compat: meta.Compat, Env: env, ThinkingLevelMap: model.ThinkingLevelMap, SamplingParams: model.SamplingParams, SamplingParamsByThinkingLevel: model.SamplingParamsByThinkingLevel}), nil
	case APIOpenAIResponses:
		return NewOpenAIResponsesProvider(OpenAIResponsesConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, ExtraHeaders: meta.Headers, Compat: meta.Compat, Env: env, ThinkingLevelMap: model.ThinkingLevelMap, SamplingParams: model.SamplingParams, SamplingParamsByThinkingLevel: model.SamplingParamsByThinkingLevel, IsReasoning: meta.Reasoning}), nil
	case APIAzureOpenAIResponses:
		return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, ExtraHeaders: meta.Headers, Compat: meta.Compat, Env: env, ThinkingLevelMap: model.ThinkingLevelMap, SamplingParams: model.SamplingParams, SamplingParamsByThinkingLevel: model.SamplingParamsByThinkingLevel}), nil
	case APIOpenAICodexResponses:
		return NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, ExtraHeaders: meta.Headers, Compat: meta.Compat, ThinkingLevelMap: model.ThinkingLevelMap}), nil
	case APIBedrockConverseStream:
		// pig additive (D92): the API constructors report a stripped API (strip.apis).
		return NewBedrockAPIProvider(*model)
	case APIGoogleGenerativeAI:
		return NewGoogleProvider(GoogleConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, ExtraHeaders: meta.Headers, ThinkingLevelMap: model.ThinkingLevelMap}), nil
	case APIGoogleVertex:
		return NewGoogleVertexAPIProvider(GoogleVertexConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, Headers: meta.Headers, ThinkingLevelMap: model.ThinkingLevelMap})
	case APIMistralConversations:
		return NewMistralAPIProvider(MistralConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, ExtraHeaders: meta.Headers, Reasoning: meta.Reasoning})
	default:
		return nil, fmt.Errorf("Provider %s has no API implementation for %s", meta.ProviderID, api)
	}
}

// ApplyOmittedReasoning sets the native thinking options a simple stream sends when `reasoning` is omitted: Anthropic disables thinking and Google and Vertex send `thinking: { enabled: false }` unless the caller supplied native thinking.
func ApplyOmittedReasoning(model *Model, options *StreamOptions) {
	applyOmittedReasoningFor(model.ProviderMeta.API, options)
}

func applyOmittedReasoningFor(api API, options *StreamOptions) {
	if options.Thinking != "" {
		return
	}
	switch api {
	case APIAnthropicMessages:
		// upstream: packages/ai/src/api/anthropic-messages.ts:streamSimple
		options.ThinkingEnabled = new(false)
	case APIGoogleGenerativeAI, APIGoogleVertex:
		// upstream: packages/ai/src/api/google-generative-ai.ts:streamSimple distinguishes omitted simple reasoning from omitted native thinking.
		if options.GoogleThinking == nil {
			options.GoogleThinking = &GoogleThinkingOptions{Enabled: false}
		}
	}
}
