package ai

// bedrockTestProviderFactory builds the Bedrock provider of the tests in this directory. Package ai cannot import ai/providers/bedrock, which imports ai, so the external test package (bedrock_test_provider_ai_test.go) sets it from init.
var bedrockTestProviderFactory func(model, modelName, baseURL string) Provider

// SetBedrockTestProviderFactory is the hook of the external test package that links ai/providers/bedrock.
func SetBedrockTestProviderFactory(factory func(model, modelName, baseURL string) Provider) {
	bedrockTestProviderFactory = factory
}

// NewBedrockTestProvider builds the Bedrock provider for multi-provider tests (package ai and ai_test).
func NewBedrockTestProvider(model, modelName, baseURL string) Provider {
	if bedrockTestProviderFactory == nil {
		panic("ai tests: ai/providers/bedrock is not linked (bedrock_test_provider_ai_test.go sets the factory)")
	}
	return bedrockTestProviderFactory(model, modelName, baseURL)
}
