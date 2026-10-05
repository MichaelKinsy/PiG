package ai

import (
	"cmp"
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

// StreamSimple invokes a direct API implementation with model metadata and simple options. Omitted Anthropic reasoning explicitly disables thinking where the selected model permits it. Bedrock leaves authentication to the AWS credential chain or an optional bearer token. Other APIs reject missing request authentication before an event stream exists.
func StreamSimple(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	if model == nil {
		return nil, fmt.Errorf("model is nil")
	}
	key, err := directSimpleAPIKey(model.ProviderMeta, options)
	if err != nil {
		return nil, err
	}
	provider, err := directAPIProvider(model, key, options.Env)
	if err != nil {
		return nil, err
	}
	options.IsReasoning = model.ProviderMeta.Reasoning
	// upstream: packages/ai/src/api/anthropic-messages.ts:streamSimple
	if model.ProviderMeta.API == APIAnthropicMessages && options.Thinking == "" {
		options.ThinkingEnabled = new(false)
	}
	options.ModelCost = model.CostRates()
	if options.MaxTokens == 0 {
		options.MaxTokens = model.Capabilities.MaxOutputTokens
	}
	options.MaxTokens = ClampMaxTokensToContext(model, transcript, options.MaxTokens)
	// upstream: packages/ai/src/api/simple-options.ts:buildBaseOptions resolves the simple reasoning level's sampling parameters.
	options.SamplingParams = ResolveSamplingParams(model, cmp.Or(options.Thinking, ThinkingOff), options.SamplingParams)
	if model.ProviderMeta.API == APIGoogleGenerativeAI && options.GoogleThinking == nil && options.Thinking == "" {
		// upstream: packages/ai/src/api/google-generative-ai.ts:streamSimple distinguishes omitted simple reasoning from omitted native thinking.
		options.Thinking = ThinkingOff
	}
	return provider.Stream(ctx, transcript, options)
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
	meta := model.ProviderMeta
	switch meta.API {
	case APIAnthropicMessages:
		return NewAnthropicProvider(AnthropicConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, ExtraHeaders: meta.Headers, Compat: meta.Compat, Env: env}), nil
	case APIOpenAICompletions:
		return NewOpenAIProvider(OpenAIConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, ExtraHeaders: meta.Headers, Compat: meta.Compat, Env: env, ThinkingLevelMap: model.ThinkingLevelMap, SamplingParams: model.SamplingParams, SamplingParamsByThinkingLevel: model.SamplingParamsByThinkingLevel}), nil
	case APIOpenAIResponses:
		return NewOpenAIResponsesProvider(OpenAIResponsesConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, ExtraHeaders: meta.Headers, Compat: meta.Compat, Env: env, ThinkingLevelMap: model.ThinkingLevelMap, SamplingParams: model.SamplingParams, SamplingParamsByThinkingLevel: model.SamplingParamsByThinkingLevel, IsReasoning: meta.Reasoning}), nil
	case APIAzureOpenAIResponses:
		return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, ExtraHeaders: meta.Headers, Compat: meta.Compat, Env: env, ThinkingLevelMap: model.ThinkingLevelMap, SamplingParams: model.SamplingParams, SamplingParamsByThinkingLevel: model.SamplingParamsByThinkingLevel}), nil
	case APIOpenAICodexResponses:
		return NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, Compat: meta.Compat, ThinkingLevelMap: model.ThinkingLevelMap}), nil
	case APIBedrockConverseStream:
		return NewBedrockProviderWithModel(*model), nil
	case APIGoogleGenerativeAI:
		return NewGoogleProvider(GoogleConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, ExtraHeaders: meta.Headers, ThinkingLevelMap: model.ThinkingLevelMap}), nil
	case APIMistralConversations:
		return NewMistralProvider(MistralConfig{ModelMetadata: model, APIKey: key, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, ExtraHeaders: meta.Headers, Reasoning: meta.Reasoning}), nil
	default:
		return nil, fmt.Errorf("Provider %s has no API implementation for %s", meta.ProviderID, meta.API)
	}
}
