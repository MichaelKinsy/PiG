//go:build !pig_strip_bedrock_converse_stream

package ai

// NewBedrockTestProvider builds the Bedrock provider for multi-provider tests (package ai and ai_test) that compile in builds with and without Bedrock.
func NewBedrockTestProvider(model, modelName, baseURL string) Provider {
	return NewBedrockProviderWithName(model, modelName, baseURL)
}
