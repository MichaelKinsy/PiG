//go:build !pig_strip_bedrock_converse_stream

package ai_test

import (
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/ai/providers/bedrock"
)

func init() {
	ai.SetBedrockTestProviderFactory(func(model, modelName, baseURL string) ai.Provider {
		return bedrock.NewBedrockProviderWithName(model, modelName, baseURL)
	})
}
