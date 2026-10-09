package ai

// Ports packages/ai/src/legacy-api-aliases.ts: the deprecated per-API stream and simple-stream functions.
// Each function returns the stream or simple stream of one API; the dynamic-import wrappers (api/*.lazy.ts) are designed out because Go links the providers statically.

import (
	"context"
	"errors"
	"fmt"
)

// apiStreams builds the streams of one API. The simple stream sends the simple reasoning and token-limit options. The plain stream sends the options as given, with the same request-authentication check, as the API module's own `stream` does.
func apiStreams(api API) *ProviderStreams {
	// api/lazy.ts lazyApi: the stream returns at once and the provider is built behind it, so a setup failure such as a missing API key ends the stream with an error event instead of failing the call. A nil model fails the call. Pi's model parameter cannot be undefined: an undefined model returns a stream that never ends, and reading model.api in lazy.ts createSetupErrorMessage rejects unhandled. The API module's stream and streamSimple do not compare model.api with their own: they build the request of their API from the model's fields.
	return &ProviderStreams{
		Stream: func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
			if model == nil {
				return nil, fmt.Errorf("model is nil")
			}
			return LazyStream(ctx, model, func(ctx context.Context) (*AssistantMessageEventStream, error) {
				return apiStream(ctx, api, model, transcript, options)
			}), nil
		},
		StreamSimple: func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
			if model == nil {
				return nil, fmt.Errorf("model is nil")
			}
			return LazyStream(ctx, model, func(ctx context.Context) (*AssistantMessageEventStream, error) {
				if api == APIPiMessages {
					return apiStream(ctx, api, model, transcript, options)
				}
				return streamSimpleAs(ctx, api, model, transcript, options)
			}), nil
		},
	}
}

// requestAuthError is a missing request authentication that the API module's stream asserts inside its own try block.
type requestAuthError struct{ err error }

func (e requestAuthError) Error() string { return e.err.Error() }

// apiStream is the API module's stream. Every module asserts request authentication inside the try block of its stream (for example openai-responses.ts:155-157, mistral-conversations.ts:137-138, pi-messages.ts:366-367), so a missing key ends the returned stream with the abort-aware stop reason (`options?.signal?.aborted ? "aborted" : "error"`) instead of failing the lazy setup.
func apiStream(ctx context.Context, api API, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	provider, err := apiStreamProvider(api, model, options)
	if auth, ok := errors.AsType[requestAuthError](err); ok {
		reason := StopReasonError
		if ctx.Err() != nil {
			reason = StopReasonAborted
		}
		builder := newObservedProviderBuilder(ctx, api, model.ProviderMeta.ProviderID, model.ID)
		builder.fail(reason, auth.err)
		return builder.stream, nil
	}
	if err != nil {
		return nil, err
	}
	return provider.Stream(ctx, transcript, options)
}

func apiStreamProvider(api API, model *Model, options StreamOptions) (Provider, error) {
	meta := model.ProviderMeta
	if api == APIPiMessages {
		if options.APIKey == "" {
			return nil, requestAuthError{fmt.Errorf("No API key provided for provider %q", meta.ProviderID)}
		}
		return NewPiMessagesProvider(PiMessagesConfig{ModelMetadata: model, APIKey: options.APIKey, Model: model.ID, ProviderID: meta.ProviderID, BaseURL: meta.BaseURL, ExtraHeaders: meta.Headers}), nil
	}
	meta.API = api
	key, err := directSimpleAPIKey(meta, options)
	if err != nil {
		if api != APIAnthropicMessages {
			return nil, requestAuthError{err}
		}
		// upstream: anthropic-messages.ts:611-613 stream asserts the request auth inside its own try block, so a missing key ends the stream with the abort-aware stop reason, unlike streamSimple (:933-935), whose assertion is a setup failure.
		key = ""
	}
	return directAPIProviderFor(api, model, key, options.Env)
}

// AnthropicMessagesAPI is the anthropic-messages API (api/anthropic-messages.lazy.ts anthropicMessagesApi).
func AnthropicMessagesAPI() *ProviderStreams { return apiStreams(APIAnthropicMessages) }

// AzureOpenAIResponsesAPI is the azure-openai-responses API (azureOpenAIResponsesApi).
func AzureOpenAIResponsesAPI() *ProviderStreams { return apiStreams(APIAzureOpenAIResponses) }

// BedrockConverseStreamAPI is the bedrock-converse-stream API (bedrockConverseStreamApi).
func BedrockConverseStreamAPI() *ProviderStreams { return apiStreams(APIBedrockConverseStream) }

// GoogleGenerativeAIAPI is the google-generative-ai API (googleGenerativeAIApi).
func GoogleGenerativeAIAPI() *ProviderStreams { return apiStreams(APIGoogleGenerativeAI) }

// GoogleVertexAPI is the google-vertex API (googleVertexApi).
func GoogleVertexAPI() *ProviderStreams { return apiStreams(APIGoogleVertex) }

// MistralConversationsAPI is the mistral-conversations API (mistralConversationsApi).
func MistralConversationsAPI() *ProviderStreams { return apiStreams(APIMistralConversations) }

// OpenAICodexResponsesAPI is the openai-codex-responses API (openAICodexResponsesApi).
func OpenAICodexResponsesAPI() *ProviderStreams { return apiStreams(APIOpenAICodexResponses) }

// OpenAICompletionsAPI is the openai-completions API (openAICompletionsApi).
func OpenAICompletionsAPI() *ProviderStreams { return apiStreams(APIOpenAICompletions) }

// OpenAIResponsesAPI is the openai-responses API (openAIResponsesApi).
func OpenAIResponsesAPI() *ProviderStreams { return apiStreams(APIOpenAIResponses) }

// PiMessagesAPI is the pi-messages API (piMessagesApi).
func PiMessagesAPI() *ProviderStreams { return apiStreams(APIPiMessages) }

// The deprecated per-API stream functions of legacy-api-aliases.ts: each is the stream or simple stream of the API's implementation.

// StreamAnthropic is the anthropic-messages stream (legacy-api-aliases.ts streamAnthropic).
func StreamAnthropic(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return AnthropicMessagesAPI().Stream(ctx, model, transcript, options)
}

// StreamSimpleAnthropic is the anthropic-messages simple stream (legacy-api-aliases.ts streamSimpleAnthropic).
func StreamSimpleAnthropic(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return AnthropicMessagesAPI().StreamSimple(ctx, model, transcript, options)
}

// StreamAzureOpenAIResponses is the azure-openai-responses stream (legacy-api-aliases.ts streamAzureOpenAIResponses).
func StreamAzureOpenAIResponses(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return AzureOpenAIResponsesAPI().Stream(ctx, model, transcript, options)
}

// StreamSimpleAzureOpenAIResponses is the azure-openai-responses simple stream (legacy-api-aliases.ts streamSimpleAzureOpenAIResponses).
func StreamSimpleAzureOpenAIResponses(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return AzureOpenAIResponsesAPI().StreamSimple(ctx, model, transcript, options)
}

// StreamGoogle is the google-generative-ai stream (legacy-api-aliases.ts streamGoogle).
func StreamGoogle(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return GoogleGenerativeAIAPI().Stream(ctx, model, transcript, options)
}

// StreamSimpleGoogle is the google-generative-ai simple stream (legacy-api-aliases.ts streamSimpleGoogle).
func StreamSimpleGoogle(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return GoogleGenerativeAIAPI().StreamSimple(ctx, model, transcript, options)
}

// StreamGoogleVertex is the google-vertex stream (legacy-api-aliases.ts streamGoogleVertex).
func StreamGoogleVertex(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return GoogleVertexAPI().Stream(ctx, model, transcript, options)
}

// StreamSimpleGoogleVertex is the google-vertex simple stream (legacy-api-aliases.ts streamSimpleGoogleVertex).
func StreamSimpleGoogleVertex(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return GoogleVertexAPI().StreamSimple(ctx, model, transcript, options)
}

// StreamMistral is the mistral-conversations stream (legacy-api-aliases.ts streamMistral).
func StreamMistral(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return MistralConversationsAPI().Stream(ctx, model, transcript, options)
}

// StreamSimpleMistral is the mistral-conversations simple stream (legacy-api-aliases.ts streamSimpleMistral).
func StreamSimpleMistral(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return MistralConversationsAPI().StreamSimple(ctx, model, transcript, options)
}

// StreamOpenAICodexResponses is the openai-codex-responses stream (legacy-api-aliases.ts streamOpenAICodexResponses).
func StreamOpenAICodexResponses(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return OpenAICodexResponsesAPI().Stream(ctx, model, transcript, options)
}

// StreamSimpleOpenAICodexResponses is the openai-codex-responses simple stream (legacy-api-aliases.ts streamSimpleOpenAICodexResponses).
func StreamSimpleOpenAICodexResponses(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return OpenAICodexResponsesAPI().StreamSimple(ctx, model, transcript, options)
}

// StreamOpenAICompletions is the openai-completions stream (legacy-api-aliases.ts streamOpenAICompletions).
func StreamOpenAICompletions(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return OpenAICompletionsAPI().Stream(ctx, model, transcript, options)
}

// StreamSimpleOpenAICompletions is the openai-completions simple stream (legacy-api-aliases.ts streamSimpleOpenAICompletions).
func StreamSimpleOpenAICompletions(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return OpenAICompletionsAPI().StreamSimple(ctx, model, transcript, options)
}

// StreamOpenAIResponses is the openai-responses stream (legacy-api-aliases.ts streamOpenAIResponses).
func StreamOpenAIResponses(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return OpenAIResponsesAPI().Stream(ctx, model, transcript, options)
}

// StreamSimpleOpenAIResponses is the openai-responses simple stream (legacy-api-aliases.ts streamSimpleOpenAIResponses).
func StreamSimpleOpenAIResponses(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	return OpenAIResponsesAPI().StreamSimple(ctx, model, transcript, options)
}
