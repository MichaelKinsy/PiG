package ai

// Go links provider factories statically instead of using dynamic imports.

// ProviderFactory creates a Provider from an API key and model spec.
// Used by the built-in provider registry to construct providers on demand.
type ProviderFactory func(apiKey, model, baseURL string) Provider

// builtInProviders maps API identifiers to their provider constructors.
var builtInProviders = map[API]ProviderFactory{
	APIOpenAICompletions: func(apiKey, model, baseURL string) Provider {
		return NewOpenAIProvider(OpenAIConfig{
			APIKey:  apiKey,
			Model:   model,
			BaseURL: baseURL,
		})
	},
	APIMistralConversations: func(apiKey, model, baseURL string) Provider {
		return NewMistralProvider(MistralConfig{
			APIKey:  apiKey,
			Model:   model,
			BaseURL: baseURL,
		})
	},
	APIOpenAIResponses: func(apiKey, model, baseURL string) Provider {
		return NewOpenAIResponsesProvider(OpenAIResponsesConfig{
			APIKey:  apiKey,
			Model:   model,
			BaseURL: baseURL,
		})
	},
	APIAnthropicMessages: func(apiKey, model, baseURL string) Provider {
		return NewAnthropicProvider(AnthropicConfig{
			APIKey:  apiKey,
			Model:   model,
			BaseURL: baseURL,
		})
	},
	APIGoogleGenerativeAI: func(apiKey, model, baseURL string) Provider {
		return NewGoogleProvider(GoogleConfig{
			APIKey:     apiKey,
			Model:      model,
			BaseURL:    baseURL,
			ProviderID: string(APIGoogleGenerativeAI),
		})
	},
	APIAzureOpenAIResponses: func(apiKey, model, baseURL string) Provider {
		return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{
			APIKey:     apiKey,
			Model:      model,
			BaseURL:    baseURL,
			ProviderID: string(APIAzureOpenAIResponses),
		})
	},
	APIOpenAICodexResponses: func(apiKey, model, baseURL string) Provider {
		return NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{
			APIKey:  apiKey,
			Model:   model,
			BaseURL: baseURL,
		})
	},
	APIPiMessages: func(apiKey, model, baseURL string) Provider {
		return NewPiMessagesProvider(PiMessagesConfig{
			APIKey:     apiKey,
			Model:      model,
			BaseURL:    baseURL,
			ProviderID: string(APIPiMessages),
		})
	},
	APIGoogleVertex: func(apiKey, model, baseURL string) Provider {
		return NewGoogleVertexProvider(GoogleVertexConfig{
			APIKey:  apiKey,
			Model:   model,
			BaseURL: baseURL,
		})
	},
}

// LookupBuiltInProvider returns the factory for a built-in API, if registered.
func LookupBuiltInProvider(api API) (ProviderFactory, bool) {
	f, ok := builtInProviders[api]
	return f, ok
}

// RegisteredAPIs returns the set of built-in API identifiers.
func RegisteredAPIs() []API {
	apis := make([]API, 0, len(builtInProviders))
	for api := range builtInProviders {
		apis = append(apis, api)
	}
	return apis
}
