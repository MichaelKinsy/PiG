//go:build pig_strip_bedrock_converse_stream

package ai

// NewBedrockTestProvider panics: this build strips bedrock-converse-stream.
func NewBedrockTestProvider(model, _, _ string) Provider {
	panic(strippedBedrockError(&Model{ID: model}))
}
